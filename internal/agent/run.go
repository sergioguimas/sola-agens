package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"sola-agens/internal/agentes"
	"sola-agens/internal/conector"
	"sola-agens/internal/llm"
	"sola-agens/internal/mensagem"
	"sola-agens/internal/openapi"
	"sola-agens/internal/sistema"
	"sola-agens/internal/store"
)

// Valores de store.Turno.Resultado (specs/01 §8).
const (
	ResultadoOK            = "ok"
	ResultadoErroLLM       = "erro_llm"
	ResultadoErroSistema   = "erro_sistema"
	ResultadoErroEnvio     = "erro_envio"
	ResultadoTicketInvalid = "ticket_invalido"
	// ResultadoErroTranscricao: a transcrição falhou e o cliente recebeu a
	// falhaTecnica sem escalonamento (F5 passo 5). DECISÃO: valor novo; a coluna é texto livre.
	ResultadoErroTranscricao = "erro_transcricao"
)

const (
	nomeChamarHumano = "chamar_humano"
	maxResumo        = 1000
	textoFeito       = "Feito! Mais alguma coisa?"
	pedidoTextoFinal = "Escreva agora a resposta final ao cliente, só o texto. Não chame ferramentas."
	prefixoEquipe    = "[equipe] "
	prazoCauda       = 30 * time.Second // envio, escalonamento e gravação depois do loop
)

// motivosHumano é o enum de chamar_humano (specs/02 §4). "falha_tecnica" fica
// de fora: é só do motor (F2).
var motivosHumano = []string{"pedido_do_cliente", "cliente_ambiguo", "fora_da_politica", "nao_entendi", "reclamacao", "numero_novo", "outro"}

// codigosTicket: o ticket (ou o acesso) não vale mais (specs/03 F1 passo 13).
var codigosTicket = map[string]bool{
	"TICKET_MISSING": true, "TICKET_INVALID": true, "TICKET_EXPIRED": true,
	"ADDON_INACTIVE": true, "UNAUTHORIZED": true, "VERSION_MISMATCH": true,
}

func chamarHumanoDecl() llm.ToolDecl {
	enum := make([]any, len(motivosHumano))
	for i, m := range motivosHumano {
		enum[i] = m
	}
	return llm.ToolDecl{
		Name: nomeChamarHumano,
		Description: "Avisa a equipe do estabelecimento para assumir a conversa. Use quando o cliente pedir uma pessoa, " +
			"em caso de dúvida, reclamação ou quando as outras ferramentas não resolvem. Devolve equipeNotificada.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"motivo": map[string]any{"type": "string", "enum": enum, "description": "Por que a equipe precisa assumir."},
				"resumo": map[string]any{"type": "string", "description": "Resumo para a equipe, até 1000 caracteres."},
			},
			"required": []any{"motivo", "resumo"},
		},
	}
}

// falha é um turno que aborta (specs/03 F2).
type falha struct {
	etapa     string // texto do resumo da escalação; sem conteúdo do cliente
	resultado string // erro_llm | erro_sistema
	causa     error
}

// turnoExec é o estado de um turno em execução.
type turnoExec struct {
	m      *Motor
	t      Turno
	ag     *Agente
	id     store.ConversaID
	log    *slog.Logger
	ident  conector.Identidade
	ctx    context.Context // com o prazo do turno
	fim    context.Context // sem cancelamento: envio, memória e registro depois de timeout
	inicio time.Time

	dic      Dicionario
	nome     string // primeiro nome do cliente identificado
	textoMem string // texto do turno como a memória o grava (áudio: "[áudio Ns] <texto>")
	textoLLM string // texto do turno como o LLM o vê (áudio transcrito = texto digitado)
	fpTexto  string // texto para o fastpath: mensagem única de texto ou de áudio transcrito

	// Acumulado do turno (store.Turno).
	modelo string
	iter   int
	ferr   []store.FerramentaLog
	tin    int
	tout   int
	// transcrição: tokens já contados em tin/tout, custo calculado com o modelo dela
	tinTransc, toutTransc int
	custoTranscricao      int64
	escalou               bool
	escrita               bool // houve escrita (método ≠ GET) com sucesso
}

// Run executa o turno: F1 passos 10–18 e F2 (specs/03). Erros de regra e falhas
// técnicas são tratados dentro dele (resposta, escalonamento, registro); o erro
// devolvido é só de infraestrutura (não conseguiu registrar o turno).
func (m *Motor) Run(ctx context.Context, t Turno) error {
	ids := make([]string, len(t.Mensagens))
	for i, msg := range t.Mensagens {
		ids[i] = msg.ID
	}
	e := &turnoExec{
		m: m, t: t, ag: t.Agente, id: t.Conversa, ctx: ctx, fim: context.WithoutCancel(ctx),
		inicio: m.agora(), ident: conector.Identidade{Ticket: t.Ticket}, ferr: []store.FerramentaLog{},
		modelo: "",
	}
	e.log = m.log().With("agente", t.Conversa.Agente, "tenant", t.Conversa.Tenant,
		"conversa", mensagem.HashContato(t.Conversa.Contato), "turno", uuid.NewString())
	e.textoMem = juntarTextos(t.Mensagens)
	e.textoLLM = e.textoMem
	if len(t.Mensagens) == 1 && len(t.Mensagens[0].Anexos) == 0 {
		e.fpTexto = t.Mensagens[0].Texto
	}
	e.dic = m.padrao()
	return e.rodar()
}

func (e *turnoExec) rodar() error {
	ctx := e.ctx
	conv, err := e.m.Store.Conversa(ctx, e.id)
	if err != nil {
		return e.falhar(&falha{etapa: "memória", resultado: ResultadoErroSistema, causa: err})
	}
	e.dic = e.dicionarioDe(conv.ConfigJSON)

	// 10) pausada: só grava.
	if conv.PausadaAte != nil && conv.PausadaAte.After(e.m.agora()) {
		return e.pausado()
	}
	// 11–12) só mídia que não processamos.
	if e.soMidiaFixa() {
		return e.concluir(store.ModoFixa, e.respostaMidiaFixa())
	}

	// 12) áudio com política processar: baixa, transcreve e vira texto (F5).
	if e.temAudioProcessar() {
		d, f := e.processarAudios()
		if f != nil {
			return e.falhar(f)
		}
		if d != nil {
			return e.encerrarAudio(d)
		}
	}

	// 13) contexto do turno.
	c, err := e.ag.Sistema.Contexto(ctx, e.ident)
	if err != nil {
		var ab *conector.ErrAbortar
		if errors.As(err, &ab) && codigosTicket[ab.Code] {
			return e.ticketInvalido(ab.Code)
		}
		return e.falhar(&falha{etapa: "contexto", resultado: ResultadoErroSistema, causa: err})
	}
	cfg, _ := json.Marshal(c.Atendente) // struct simples: não falha
	if err := e.m.Store.GuardarConfig(ctx, e.id, cfg); err != nil {
		return e.falhar(&falha{etapa: "memória", resultado: ResultadoErroSistema, causa: err})
	}
	e.dic = Mesclar(e.m.padrao(), c.Atendente)
	if c.Identificacao.Situacao == sistema.SituacaoIdentificado && c.Identificacao.Cliente != nil {
		e.nome = c.Identificacao.Cliente.PrimeiroNome
	}

	// 14) fastpath.
	texto, aplicou, f := e.fastpath(c)
	if f != nil {
		return e.falhar(f)
	}
	if aplicou {
		return e.concluir(store.ModoFastpath, texto)
	}

	// 15–16) prompt, memória e loop.
	texto, f = e.loopLLM(c)
	if f != nil {
		return e.falhar(f)
	}
	return e.concluir(store.ModoLLM, texto)
}

// dicionarioDe usa a última configuração guardada da conversa (passos 10–12
// acontecem antes do contexto do turno).
func (e *turnoExec) dicionarioDe(cfgJSON []byte) Dicionario {
	d := e.m.padrao()
	if len(cfgJSON) == 0 {
		return d
	}
	var a sistema.Atendente
	if json.Unmarshal(cfgJSON, &a) != nil {
		return d
	}
	return Mesclar(d, a)
}

// --- passos 10 e 11 -------------------------------------------------------

func (e *turnoExec) pausado() error {
	for _, msg := range e.t.Mensagens {
		e.gravar(store.PapelCliente, textoMensagem(msg), msg.Anexos)
	}
	e.log.Info("conversa pausada: mensagem só gravada")
	return e.registrar(store.ModoPausado, ResultadoOK)
}

// politica é a política de mídia que vale hoje: "processar" só existe para
// áudio e só com Transcritor montado; fora disso vale como resposta fixa.
func (e *turnoExec) politica(tipo string) string {
	p := e.ag.Registro.PoliticaMidia(tipo)
	if p == agentes.MidiaProcessar && !(tipo == "audio" && e.m.Transcritor != nil) {
		return agentes.MidiaRespostaFixa
	}
	return p
}

// soMidiaFixa: todas as mensagens sem texto e todos os anexos com política
// resposta_fixa (passo 11).
func (e *turnoExec) soMidiaFixa() bool {
	for _, msg := range e.t.Mensagens {
		if msg.Texto != "" || len(msg.Anexos) == 0 {
			return false
		}
		for _, a := range msg.Anexos {
			if e.politica(a.Tipo) != agentes.MidiaRespostaFixa {
				return false
			}
		}
	}
	return len(e.t.Mensagens) > 0
}

func (e *turnoExec) respostaMidiaFixa() string {
	todosOutro := true
	for _, msg := range e.t.Mensagens {
		for _, a := range msg.Anexos {
			if a.Tipo != "outro" {
				todosOutro = false
			}
		}
	}
	if todosOutro {
		return Aplicar(e.dic.Respostas.NaoSuportado, "")
	}
	return Aplicar(e.dic.Respostas.MidiaNaoSuportada, "")
}

// --- passos 15 e 16: prompt, memória e loop -------------------------------

func (e *turnoExec) decls() []llm.ToolDecl {
	out := make([]llm.ToolDecl, 0, len(e.ag.Ferramentas)+1)
	for _, f := range e.ag.Ferramentas {
		out = append(out, f.Decl)
	}
	return append(out, chamarHumanoDecl())
}

func (e *turnoExec) historico() ([]llm.Turn, *falha) {
	lim := e.ag.Registro.Limites
	desde := e.m.agora().Add(-time.Duration(lim.MemoriaHoras) * time.Hour)
	hist, err := e.m.Store.Janela(e.ctx, e.id, lim.MemoriaMensagens, desde)
	if err != nil {
		return nil, &falha{etapa: "memória", resultado: ResultadoErroSistema, causa: err}
	}
	out := make([]llm.Turn, 0, len(hist)+1)
	for _, h := range hist {
		if h.Conteudo == nil || *h.Conteudo == "" {
			continue // expurgado pela retenção
		}
		role, texto := "user", *h.Conteudo
		switch h.Papel {
		case store.PapelAgente:
			role = "model"
		case store.PapelHumano:
			texto = prefixoEquipe + texto
		}
		out = append(out, llm.Turn{Role: role, Parts: []llm.Part{{Text: texto}}})
	}
	return out, nil
}

// gerar chama o LLM e soma tokens. Candidato vazio do provedor vira resposta
// vazia (tratada como no Smaug: uma nova chamada sem tools).
func (e *turnoExec) gerar(req llm.GenerateRequest) (*llm.GenerateResponse, error) {
	resp, err := e.m.LLM.Generate(e.ctx, req)
	if errors.Is(err, llm.ErrRespostaVazia) {
		return &llm.GenerateResponse{}, nil
	}
	if err != nil {
		return nil, err
	}
	e.tin += resp.InputTokens
	e.tout += resp.OutputTokens
	if resp.FinishReason != "" && resp.FinishReason != "STOP" {
		return nil, &llm.ErrFinishReason{Reason: resp.FinishReason}
	}
	return resp, nil
}

func falhaLLM(ctx context.Context, err error) *falha {
	etapa := "llm"
	if ctx.Err() != nil {
		etapa = "tempo esgotado"
	}
	return &falha{etapa: etapa, resultado: ResultadoErroLLM, causa: err}
}

func (e *turnoExec) loopLLM(c sistema.Contexto) (string, *falha) {
	hist, f := e.historico()
	if f != nil {
		return "", f
	}
	contents := append(hist, llm.Turn{Role: "user", Parts: []llm.Part{{Text: e.textoLLM}}})

	e.modelo = e.ag.Modelo
	tb := e.m.Config.ThinkingBudget
	// Os tokens de "pensamento" saem do mesmo teto de saída (como no Smaug).
	maxOut := 1024
	if tb > 0 {
		maxOut += tb
	}
	req := llm.GenerateRequest{
		Model:          e.modelo,
		System:         Prompt(c, e.dic, e.t.NomeExibicao),
		Tools:          e.decls(),
		Temperature:    e.m.Config.Temperature,
		MaxOutputTok:   maxOut,
		ThinkingBudget: &tb,
	}

	for e.iter = 0; e.iter < e.ag.Registro.Limites.MaxIter; {
		e.iter++
		req.Contents = contents
		resp, err := e.gerar(req)
		if err != nil {
			return "", falhaLLM(e.ctx, err)
		}
		var calls []llm.FunctionCall
		var texto []string
		for _, p := range resp.Parts {
			if p.FunctionCall != nil {
				calls = append(calls, *p.FunctionCall)
			}
			if p.Text != "" {
				texto = append(texto, p.Text)
			}
		}
		if len(calls) == 0 {
			if t := strings.TrimSpace(strings.Join(texto, "")); t != "" {
				return t, nil
			}
			return e.recuperarVazio(req, contents)
		}

		// O turno do modelo entra inteiro, ThoughtSignature inclusa.
		contents = append(contents, llm.Turn{Role: "model", Parts: resp.Parts})
		partes, f := e.executarChamadas(calls)
		if f != nil {
			return "", f
		}
		contents = append(contents, llm.Turn{Role: "user", Parts: partes})
	}
	return "", &falha{etapa: "limite de iterações", resultado: ResultadoErroLLM,
		causa: fmt.Errorf("max_iter (%d) atingido", e.ag.Registro.Limites.MaxIter)}
}

// recuperarVazio: o modelo não devolveu texto nem chamada (specs/03 F1 passo
// 16). Uma nova chamada SEM tools pedindo só o texto final; vazia de novo →
// houve escrita com sucesso? "Feito!" : F2.
func (e *turnoExec) recuperarVazio(req llm.GenerateRequest, contents []llm.Turn) (string, *falha) {
	e.log.Warn("resposta_vazia", "iteracao", e.iter)
	req.Tools = nil
	req.Contents = append(append([]llm.Turn{}, contents...),
		llm.Turn{Role: "user", Parts: []llm.Part{{Text: pedidoTextoFinal}}})
	resp, err := e.gerar(req)
	if err != nil {
		return "", falhaLLM(e.ctx, err)
	}
	var b strings.Builder
	for _, p := range resp.Parts {
		b.WriteString(p.Text)
	}
	if t := strings.TrimSpace(b.String()); t != "" {
		return t, nil
	}
	if e.escrita {
		e.log.Warn("resposta_vazia_com_escrita")
		return textoFeito, nil
	}
	return "", &falha{etapa: "resposta vazia do modelo", resultado: ResultadoErroLLM, causa: llm.ErrRespostaVazia}
}

// executarChamadas executa todas as chamadas do turno do modelo, em ordem, e
// devolve um functionResponse para cada uma.
// DECISÃO: as chamadas "paralelas" do Gemini rodam em sequência: escritas em
// ordem são mais seguras (a mesma ordem que o modelo pediu) e o ganho de
// latência não compensa na F1. Falha técnica interrompe o resto (F2 passo 1).
func (e *turnoExec) executarChamadas(calls []llm.FunctionCall) ([]llm.Part, *falha) {
	partes := make([]llm.Part, 0, len(calls))
	for _, call := range calls {
		var res map[string]any
		switch {
		case call.Name == nomeChamarHumano:
			res = e.chamarHumano(call.Args)
		default:
			f, ok := e.ag.Ferramenta(call.Name)
			if !ok {
				e.ferr = append(e.ferr, store.FerramentaLog{Nome: call.Name, Codigo: "FERRAMENTA_DESCONHECIDA"})
				res = map[string]any{"erro": "FERRAMENTA_DESCONHECIDA", "mensagem": "essa ferramenta não existe"}
				break
			}
			r, falha := e.executarFerramenta(f, call.Args)
			if falha != nil {
				return nil, falha
			}
			res = r.ParaLLM()
		}
		e.log.Debug("ferramenta", "nome", call.Name, "args", call.Args, "resultado", res)
		partes = append(partes, llm.Part{FunctionResponse: &llm.FunctionResponse{Name: call.Name, Response: res}})
	}
	return partes, nil
}

// executarFerramenta chama o conector, registra a chamada no log do turno (sem
// argumentos) e converte falha técnica em *falha.
func (e *turnoExec) executarFerramenta(f openapi.Ferramenta, args map[string]any) (conector.Resultado, *falha) {
	r, err := e.ag.Exec.Executar(e.ctx, f, args, e.ident)
	if err != nil {
		status, codigo := 0, "ERRO"
		var ab *conector.ErrAbortar
		if errors.As(err, &ab) {
			status, codigo = ab.Status, ab.Code
			if ab.RequestID != "" {
				e.log.Warn("sistema_abortou", "ferramenta", f.Decl.Name, "codigo", ab.Code, "request_id", ab.RequestID)
			}
		}
		e.ferr = append(e.ferr, store.FerramentaLog{Nome: f.Decl.Name, Status: status, Codigo: codigo})
		return r, &falha{etapa: "ferramenta", resultado: ResultadoErroSistema, causa: err}
	}
	entrada := store.FerramentaLog{Nome: f.Decl.Name, Status: r.Status}
	if r.ErroRegra != nil {
		entrada.Codigo = r.ErroRegra.Erro
	} else if f.Metodo != http.MethodGet {
		e.escrita = true
	}
	e.ferr = append(e.ferr, entrada)
	return r, nil
}

// chamarHumano executa a ferramenta interna (specs/02 §4, specs/03 F4). Erro do
// escalar nunca derruba o turno: volta ao LLM como equipeNotificada=false.
func (e *turnoExec) chamarHumano(args map[string]any) map[string]any {
	motivo, _ := args["motivo"].(string)
	resumo, _ := args["resumo"].(string)
	valido := false
	for _, m := range motivosHumano {
		valido = valido || m == motivo
	}
	if !valido {
		e.ferr = append(e.ferr, store.FerramentaLog{Nome: nomeChamarHumano, Codigo: "ARGUMENTO_INVALIDO"})
		return map[string]any{"erro": "ARGUMENTO_INVALIDO", "mensagem": "motivo inválido; use um dos valores permitidos"}
	}
	if r := []rune(strings.TrimSpace(resumo)); len(r) > maxResumo {
		resumo = string(r[:maxResumo])
	}
	notificada, err := e.ag.Sistema.Escalar(e.ctx, e.ident, motivo, strings.TrimSpace(resumo))
	if err != nil {
		status, codigo := codigoDe(err)
		// DECISÃO: status 0 quando o escalar dá certo — sistema.Escalar não expõe o status HTTP.
		e.ferr = append(e.ferr, store.FerramentaLog{Nome: nomeChamarHumano, Status: status, Codigo: codigo})
		e.log.Warn("escalar_falhou", "codigo", codigo)
		return map[string]any{"equipeNotificada": false}
	}
	e.escalou = true
	e.ferr = append(e.ferr, store.FerramentaLog{Nome: nomeChamarHumano})
	return map[string]any{"equipeNotificada": notificada}
}

// codigoDe extrai status e código de um erro do sistema (sem texto).
func codigoDe(err error) (int, string) {
	var ab *conector.ErrAbortar
	if errors.As(err, &ab) {
		return ab.Status, ab.Code
	}
	var es *sistema.ErrSistema
	if errors.As(err, &es) {
		return es.Status, es.Code
	}
	return 0, "ERRO"
}

// --- passos 17 e 18 -------------------------------------------------------

// enviar manda o texto em blocos ≤ 4000 runes, em ordem, e registra o eco de
// cada mensagemId devolvido (specs/01 §6). Para no primeiro erro.
func (e *turnoExec) enviar(ctx context.Context, texto string) (enviados int, err error) {
	for _, bloco := range Dividir(texto, LimiteMensagem) {
		id, err := e.ag.Sistema.Responder(ctx, e.ident, bloco)
		if err != nil {
			return enviados, err
		}
		enviados++
		if id == "" {
			e.log.Warn("eco_sem_id")
			continue
		}
		if err := e.m.Store.RegistrarEco(ctx, e.id.Agente, id); err != nil {
			e.log.Error("eco_nao_registrado", "erro", err.Error())
		}
	}
	return enviados, nil
}

func (e *turnoExec) cauda() (context.Context, context.CancelFunc) {
	return context.WithTimeout(e.fim, prazoCauda)
}

// concluir envia a resposta, grava a memória (cliente + agente) e registra o turno.
func (e *turnoExec) concluir(modo, texto string) error {
	return e.concluirCom(modo, texto, ResultadoOK)
}

// concluirCom é o concluir com o resultado do turno informado (ok, ou o
// resultado de um fim antecipado da F5).
func (e *turnoExec) concluirCom(modo, texto, resultado string) error {
	ctx, cancel := e.cauda()
	defer cancel()
	enviados, err := e.enviar(ctx, texto)
	if err != nil {
		// Não reenvia: a escrita, se houve, já está no sistema (specs/03 F1).
		resultado = ResultadoErroEnvio
		e.log.Error("erro_envio", "erro", err.Error())
	}
	e.gravarCliente()
	if enviados > 0 { // DECISÃO: resposta que não saiu não entra na memória como se tivesse saído.
		e.gravar(store.PapelAgente, texto, nil)
	}
	return e.registrar(modo, resultado)
}

func (e *turnoExec) ticketInvalido(codigo string) error {
	e.log.Error("ticket_invalido", "codigo", codigo)
	e.gravarCliente() // DECISÃO: a mensagem fica na memória para o próximo turno ter o contexto.
	return e.registrar(store.ModoErro, ResultadoTicketInvalid)
}

// falhar é o F2 (specs/03): resposta fixa, escalonamento, memória só do cliente,
// turno modo=erro com escalou=1 quando o escalonamento deu certo.
func (e *turnoExec) falhar(f *falha) error {
	e.log.Error("turno_falhou", "etapa", f.etapa, "resultado", f.resultado, "erro", f.causa.Error())
	ctx, cancel := e.cauda()
	defer cancel()

	if _, err := e.enviar(ctx, Aplicar(e.dic.Respostas.FalhaTecnica, e.nome)); err != nil {
		e.log.Error("erro_envio", "erro", err.Error()) // só log
	}

	resumo := "Falha técnica no atendimento automático: " + f.etapa
	_, err := e.ag.Sistema.Escalar(ctx, e.ident, "falha_tecnica", resumo)
	var es *sistema.ErrSistema
	if errors.As(err, &es) { // o sistema não conhece o motivo: tenta "outro"
		_, err = e.ag.Sistema.Escalar(ctx, e.ident, "outro", resumo)
	}
	if err != nil {
		_, codigo := codigoDe(err)
		e.log.Error("escalar_falhou", "codigo", codigo)
	}
	e.escalou = e.escalou || err == nil

	e.gravarCliente()
	return e.registrar(store.ModoErro, f.resultado)
}

// --- memória e registro ---------------------------------------------------

func (e *turnoExec) gravar(papel, texto string, anexos []mensagem.Anexo) {
	t := texto
	err := e.m.Store.AnexarMensagem(e.fim, e.id, store.MensagemMemoria{Papel: papel, Conteudo: &t, Anexos: anexos})
	if err != nil {
		e.log.Error("memoria_nao_gravada", "erro", err.Error())
	}
}

// gravarCliente grava o texto do turno (a rajada junta) como uma mensagem do cliente.
func (e *turnoExec) gravarCliente() {
	var anexos []mensagem.Anexo
	for _, msg := range e.t.Mensagens {
		anexos = append(anexos, msg.Anexos...)
	}
	e.gravar(store.PapelCliente, e.textoMem, anexos)
}

func (e *turnoExec) registrar(modo, resultado string) error {
	ids := make([]string, len(e.t.Mensagens))
	for i, msg := range e.t.Mensagens {
		ids[i] = msg.ID
	}
	t := store.Turno{
		ConversaID: e.id, Modo: modo, MensagemIDs: ids, Modelo: e.modelo, Iteracoes: e.iter,
		Ferramentas: e.ferr, TokensEntrada: e.tin, TokensSaida: e.tout,
		LatenciaMS: e.m.agora().Sub(e.inicio).Milliseconds(),
		Resultado:  resultado, Escalou: e.escalou,
	}
	if e.modelo != "" {
		t.CustoMicroUSD = llm.Custo(e.modelo, e.tin-e.tinTransc, e.tout-e.toutTransc)
	}
	t.CustoMicroUSD += e.custoTranscricao
	if err := e.m.Store.RegistrarTurno(e.fim, t); err != nil {
		e.log.Error("turno_nao_registrado", "erro", err.Error())
		return err
	}
	e.log.Info("turno", "modo", modo, "resultado", resultado, "iteracoes", e.iter, "ferramentas", len(e.ferr),
		"tokens_entrada", e.tin, "tokens_saida", e.tout, "custo_micro_usd", t.CustoMicroUSD,
		"latencia_ms", t.LatenciaMS, "escalou", e.escalou)
	return nil
}

// --- texto do turno -------------------------------------------------------

// textoMensagem é o que a mensagem vale para o LLM e para a memória: o texto
// e, para cada anexo, um marcador ("[áudio 12s]", "[imagem]"; specs/01 §7).
func textoMensagem(m mensagem.Mensagem) string {
	var partes []string
	if m.Texto != "" {
		partes = append(partes, m.Texto)
	}
	for _, a := range m.Anexos {
		partes = append(partes, marcador(a))
	}
	return strings.Join(partes, "\n")
}

func marcador(a mensagem.Anexo) string {
	switch a.Tipo {
	case "audio":
		if a.DuracaoS > 0 {
			return fmt.Sprintf("[áudio %ds]", a.DuracaoS)
		}
		return "[áudio]"
	case "imagem":
		return "[imagem]"
	case "documento":
		return "[documento]"
	case "video":
		return "[vídeo]"
	case "figurinha":
		return "[figurinha]"
	}
	return "[anexo]"
}

// juntarTextos junta as mensagens da rajada por quebra de linha: um único turno do cliente.
func juntarTextos(ms []mensagem.Mensagem) string {
	partes := make([]string, 0, len(ms))
	for _, m := range ms {
		if t := textoMensagem(m); t != "" {
			partes = append(partes, t)
		}
	}
	return strings.Join(partes, "\n")
}
