package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"sola-agens/internal/conector"
	"sola-agens/internal/entrada"
	"sola-agens/internal/llm"
	"sola-agens/internal/sistema"
	"sola-agens/internal/store"
)

const (
	nomePreencher   = "preencher_agendamento"
	maxRespostaWeb  = 2000 // Zod ChatWebResposta do Eliza
	textoPreenchido = "Preenchi o que você escolheu no formulário."
	textoRepetirWeb = "Pode repetir?"
)

// Erros do canal web, traduzidos em status pela API (specs/07 §3).
var (
	ErrTetoWeb         = errors.New("teto diário do canal web")
	ErrWebIndisponivel = errors.New("canal web indisponível")
)

var (
	// Documento e telefone: o visitante pode digitar mesmo sem ser pedido (W7, S36).
	reDocumento = regexp.MustCompile(`\b\d{3}[.\s]?\d{3}[.\s]?\d{3}[-.\s]?\d{2}\b`)
	reTelefone  = regexp.MustCompile(`\(?\b\d{2}\)?[\s-]?9?\d{4}[\s-]?\d{4}\b`)
)

// Mascarar tira documento e telefone do texto antes do LLM e da base (S36).
func Mascarar(s string) string {
	return reTelefone.ReplaceAllString(reDocumento.ReplaceAllString(s, "[documento]"), "[telefone]")
}

func preencherDecl() llm.ToolDecl {
	texto := func(d string) map[string]any { return map[string]any{"type": "string", "description": d} }
	return llm.ToolDecl{
		Name: nomePreencher,
		Description: "Preenche o formulário da página com o que o cliente já escolheu. Chame assim que houver qualquer escolha, " +
			"mesmo parcial. Não agenda nada: quem confirma é o cliente, no formulário.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"servicoId":      texto("id do serviço, de listarServicos."),
				"profissionalId": texto("id do profissional, de listarProfissionais."),
				"data":           texto("Dia, AAAA-MM-DD."),
				"hora":           texto("Horário livre, HH:MM, de consultarHorarios."),
			},
		},
	}
}

// TurnoWeb é o turno síncrono do canal web (specs/07 §4). O motor nunca grava
// no sistema aqui: só ferramentas públicas de leitura e a ação de preencher.
func (m *Motor) TurnoWeb(ctx context.Context, ag *Agente, msg entrada.MensagemWeb) (entrada.RespostaWeb, error) {
	web := ag.Registro.Web
	id := store.ConversaID{Agente: ag.Registro.ID, Tenant: msg.OrgID, Contato: msg.Contato}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(web.TurnoTimeoutS)*time.Second)
	defer cancel()

	feitos, err := m.Store.ContarTurnosWeb(ctx, id.Agente, id.Tenant, m.inicioDoDia())
	if err != nil {
		return entrada.RespostaWeb{}, err
	}
	if feitos >= web.TetoTurnosDiaOrg {
		return entrada.RespostaWeb{}, ErrTetoWeb
	}

	t := &turnoWeb{m: m, ag: ag, id: id, ctx: ctx, inicio: m.agora(), modelo: ag.Modelo,
		ident: conector.Identidade{Ticket: msg.Ticket.Token}, ferr: []store.FerramentaLog{}}
	texto := Mascarar(msg.Texto)

	resposta, err := t.rodar(texto, msg)
	if err != nil {
		t.registrar(msg.MensagemID, ResultadoErroLLM)
		m.log().Warn("web: turno falhou", "agente", id.Agente, "tenant", id.Tenant, "erro", err.Error())
		return entrada.RespostaWeb{}, fmt.Errorf("%w: %v", ErrWebIndisponivel, err)
	}

	fim, cancelar := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancelar()
	for _, p := range []struct{ papel, conteudo string }{{store.PapelCliente, texto}, {store.PapelAgente, resposta}} {
		if err := m.Store.AnexarMensagem(fim, id, store.MensagemMemoria{Papel: p.papel, Conteudo: &p.conteudo}); err != nil {
			m.log().Error("web: memória", "erro", err.Error())
		}
	}
	t.registrar(msg.MensagemID, ResultadoOK)

	acoes := []entrada.AcaoWeb{}
	if t.acao != nil {
		acoes = append(acoes, *t.acao)
	}
	return entrada.RespostaWeb{Resposta: resposta, Acoes: acoes}, nil
}

func (m *Motor) inicioDoDia() time.Time {
	agora := m.agora()
	if m.Config.Loc != nil {
		agora = agora.In(m.Config.Loc)
	}
	a, mes, d := agora.Date()
	return time.Date(a, mes, d, 0, 0, 0, 0, agora.Location())
}

type turnoWeb struct {
	m       *Motor
	ag      *Agente
	id      store.ConversaID
	ctx     context.Context
	ident   conector.Identidade
	inicio  time.Time
	modelo  string
	iter    int
	tin     int
	tout    int
	ferr    []store.FerramentaLog
	acao    *entrada.AcaoWeb
	fechada bool // agenda fechada neste turno (JanelaMaximaDias == 0)
}

func (t *turnoWeb) rodar(texto string, msg entrada.MensagemWeb) (string, error) {
	c, err := t.ag.Sistema.Contexto(t.ctx, t.ident)
	if err != nil {
		return "", fmt.Errorf("contexto: %w", err)
	}
	if c.Identificacao.Situacao != sistema.SituacaoVisitante {
		return "", fmt.Errorf("canal web com situação %q", c.Identificacao.Situacao)
	}
	lim := t.ag.Registro.Limites
	janela, err := t.m.Store.Janela(t.ctx, t.id, lim.MemoriaMensagens, t.m.agora().Add(-time.Duration(lim.MemoriaHoras)*time.Hour))
	if err != nil {
		return "", fmt.Errorf("memória: %w", err)
	}
	contents := make([]llm.Turn, 0, len(janela)+1)
	for _, j := range janela {
		if j.Conteudo == nil {
			continue
		}
		role := "user"
		if j.Papel == store.PapelAgente {
			role = "model"
		}
		contents = append(contents, llm.Turn{Role: role, Parts: []llm.Part{{Text: *j.Conteudo}}})
	}
	contents = append(contents, llm.Turn{Role: "user", Parts: []llm.Part{{Text: texto}}})

	t.fechada = agendaFechada(c)
	// Agenda fechada: o chat não preenche formulário nem consulta horário.
	decls := []llm.ToolDecl{}
	if !t.fechada {
		decls = append(decls, preencherDecl())
	}
	for _, f := range t.ag.Ferramentas {
		if f.Publica && visivel(f, t.fechada) {
			decls = append(decls, f.Decl)
		}
	}
	tb := t.m.Config.ThinkingBudget
	maxOut := 1024
	if tb > 0 {
		maxOut += tb
	}
	req := llm.GenerateRequest{Model: t.modelo, System: PromptWeb(c, Mesclar(t.m.padrao(), c.Atendente), msg.Formulario),
		Tools: decls, Temperature: t.m.Config.Temperature, MaxOutputTok: maxOut, ThinkingBudget: &tb}

	for t.iter < lim.MaxIter {
		t.iter++
		req.Contents = contents
		resp, err := t.m.LLM.Generate(t.ctx, req)
		if errors.Is(err, llm.ErrRespostaVazia) {
			return t.final(""), nil
		}
		if err != nil {
			return "", err
		}
		t.tin += resp.InputTokens
		t.tout += resp.OutputTokens
		if resp.FinishReason != "" && resp.FinishReason != "STOP" {
			return "", &llm.ErrFinishReason{Reason: resp.FinishReason}
		}
		var calls []llm.FunctionCall
		var partes []string
		for _, p := range resp.Parts {
			if p.FunctionCall != nil {
				calls = append(calls, *p.FunctionCall)
			}
			if p.Text != "" {
				partes = append(partes, p.Text)
			}
		}
		if len(calls) == 0 {
			return t.final(strings.Join(partes, "")), nil
		}
		respostas, err := t.executar(calls)
		if err != nil {
			return "", err
		}
		contents = append(contents, llm.Turn{Role: "model", Parts: resp.Parts}, llm.Turn{Role: "user", Parts: respostas})
	}
	return "", fmt.Errorf("max_iter (%d) atingido", lim.MaxIter)
}

func (t *turnoWeb) executar(calls []llm.FunctionCall) ([]llm.Part, error) {
	partes := make([]llm.Part, 0, len(calls))
	for _, c := range calls {
		var res map[string]any
		if c.Name == nomePreencher && !t.fechada {
			res = t.preencher(c.Args)
		} else {
			f, ok := t.ag.Ferramenta(c.Name)
			// A lista pública é a fronteira: o que está fora não executa, mesmo que
			// o modelo tente (S37).
			if !ok || !f.Publica || !visivel(f, t.fechada) {
				t.ferr = append(t.ferr, store.FerramentaLog{Nome: c.Name, Codigo: "FERRAMENTA_DESCONHECIDA"})
				res = map[string]any{"erro": "FERRAMENTA_DESCONHECIDA", "mensagem": "essa ferramenta não existe"}
			} else {
				r, err := t.ag.Exec.Executar(t.ctx, f, c.Args, t.ident)
				if err != nil {
					t.ferr = append(t.ferr, store.FerramentaLog{Nome: c.Name, Codigo: "ERRO"})
					return nil, fmt.Errorf("ferramenta %s: %w", c.Name, err)
				}
				l := store.FerramentaLog{Nome: c.Name, Status: r.Status}
				if r.ErroRegra != nil {
					l.Codigo = r.ErroRegra.Erro
				}
				t.ferr = append(t.ferr, l)
				res = r.ParaLLM()
			}
		}
		partes = append(partes, llm.Part{FunctionResponse: &llm.FunctionResponse{Name: c.Name, Response: res}})
	}
	return partes, nil
}

// preencher soma a escolha à ação do turno (S39). Campo fora de formato volta ao
// modelo: o Zod do Eliza recusaria a resposta inteira.
func (t *turnoWeb) preencher(args map[string]any) map[string]any {
	v := func(k string) string {
		s, _ := args[k].(string)
		return strings.TrimSpace(s)
	}
	a := entrada.AcaoWeb{Tipo: nomePreencher, ServicoID: v("servicoId"), ProfissionalID: v("profissionalId"), Data: v("data"), Hora: v("hora")}
	switch {
	case a.ServicoID != "" && !entrada.EhUUID(a.ServicoID), a.ProfissionalID != "" && !entrada.EhUUID(a.ProfissionalID):
		return map[string]any{"ok": false, "motivo": "use o id devolvido pelas ferramentas"}
	case a.Data != "" && !entrada.EhData(a.Data):
		return map[string]any{"ok": false, "motivo": "data deve ser AAAA-MM-DD"}
	case a.Hora != "" && !entrada.EhHora(a.Hora):
		return map[string]any{"ok": false, "motivo": "hora deve ser HH:MM"}
	case a.ServicoID == "" && a.ProfissionalID == "" && a.Data == "" && a.Hora == "":
		return map[string]any{"ok": false, "motivo": "informe ao menos um campo"}
	}
	t.ferr = append(t.ferr, store.FerramentaLog{Nome: nomePreencher, Status: 200})
	if t.acao == nil {
		t.acao = &a
		return map[string]any{"ok": true}
	}
	for _, p := range []struct{ de, para *string }{{&a.ServicoID, &t.acao.ServicoID}, {&a.ProfissionalID, &t.acao.ProfissionalID},
		{&a.Data, &t.acao.Data}, {&a.Hora, &t.acao.Hora}} {
		if *p.de != "" {
			*p.para = *p.de
		}
	}
	return map[string]any{"ok": true}
}

// final limpa o texto para o chat web, que não renderiza markdown (S40).
func (t *turnoWeb) final(s string) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "**", ""))
	if s == "" {
		if t.acao != nil {
			return textoPreenchido
		}
		return textoRepetirWeb
	}
	if utf8.RuneCountInString(s) > maxRespostaWeb {
		s = string([]rune(s)[:maxRespostaWeb])
	}
	return s
}

func (t *turnoWeb) registrar(mensagemID, resultado string) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(t.ctx), 5*time.Second)
	defer cancel()
	turno := store.Turno{ConversaID: t.id, Modo: store.ModoLLM, MensagemIDs: []string{mensagemID}, Modelo: t.modelo,
		Iteracoes: t.iter, Ferramentas: t.ferr, TokensEntrada: t.tin, TokensSaida: t.tout,
		CustoMicroUSD: llm.Custo(t.modelo, t.tin, t.tout), LatenciaMS: t.m.agora().Sub(t.inicio).Milliseconds(), Resultado: resultado}
	if err := t.m.Store.RegistrarTurno(ctx, turno); err != nil {
		t.m.log().Error("web: turno não registrado", "erro", err.Error())
	}
}

// Formulário em JSON curto para o prompt: é dica, então vai como dado.
func formularioEmTexto(f entrada.FormularioWeb) string {
	raw, err := json.Marshal(f)
	if err != nil {
		return "{}"
	}
	return string(raw)
}
