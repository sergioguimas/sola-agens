// Package treino é o chat de treino do atendente (specs/06): o operador do
// tenant responde a ensaios em que o agens faz o papel de cliente e, ao
// encerrar, o motor extrai o perfil de estilo. O perfil fica versionado na base
// e ainda não chega ao atendimento (S19, PLANO-DE-BUILD §7): nada daqui muda como
// o bot fala com os clientes.
package treino

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"sola-agens/internal/agent"
	"sola-agens/internal/agentes"
	"sola-agens/internal/llm"
	"sola-agens/internal/store"
	"sola-agens/internal/trava"
)

const (
	maxIteracoes    = 4 // specs/06 §6.3
	ensaiosSugestao = 6 // specs/06 §11 T-F2
	textoRepetir    = "Pode repetir?"

	resultadoOK         = "ok"
	resultadoErroLLM    = "erro_llm"
	resultadoLimiteIter = "limite_iteracoes"
)

// aspas acha o trecho entre aspas retas ou curvas.
var aspas = regexp.MustCompile(`"([^"]+)"|“([^”]+)”`)

// Erros que a API traduz em código (specs/06 §5).
var (
	ErrTextoVazio   = errors.New("texto vazio")
	ErrTextoLongo   = errors.New("texto longo demais")
	ErrSessaoCheia  = errors.New("sessão cheia")
	ErrIndisponivel = errors.New("treino indisponível")
)

// Store é o que o chat usa da persistência.
type Store interface {
	store.StoreTreino
	RegistrarTurno(ctx context.Context, t store.Turno) error
}

// Operador é quem treina, asseverado pelo sistema (specs/06 §4).
type Operador struct {
	Tenant string `json:"tenant"`
	ID     string `json:"id"`
	Papel  string `json:"papel"`
	Nome   string `json:"nome"`
}

// Config são os parâmetros do ambiente.
type Config struct {
	ModeloPadrao   string // AGENS_MODEL
	Temperature    float64
	ThinkingBudget int
	Loc            *time.Location // AGENS_TZ: define o "dia" do limite de sessões
}

// Servico conduz as sessões. Só depende de interfaces.
type Servico struct {
	Store  Store
	LLM    llm.Provider
	Trava  trava.Trava
	Config Config
	Clock  func() time.Time
	Log    *slog.Logger
}

func (s *Servico) agora() time.Time {
	if s.Clock != nil {
		return s.Clock()
	}
	return time.Now()
}

func (s *Servico) log() *slog.Logger {
	if s.Log != nil {
		return s.Log
	}
	return slog.Default()
}

func (s *Servico) inicioDia() time.Time {
	loc := s.Config.Loc
	if loc == nil {
		loc = time.UTC
	}
	a, m, d := s.agora().In(loc).Date()
	return time.Date(a, m, d, 0, 0, 0, 0, loc)
}

// roteiro é o andamento da sessão (sessoes_treino.roteiro_json).
type roteiro struct {
	Feitos       map[string]int `json:"feitos"`
	Preferencias []Preferencia  `json:"preferencias"`
	// UltimoEnsaio reconhece a repetição da fala quando o operador não respondeu.
	UltimoEnsaio *Ensaio `json:"ultimoEnsaio,omitempty"`
}

func lerRoteiro(raw []byte) (roteiro, error) {
	r := roteiro{Feitos: map[string]int{}, Preferencias: []Preferencia{}}
	if err := json.Unmarshal(raw, &r); err != nil {
		return r, fmt.Errorf("roteiro da sessão: %w", err)
	}
	if r.Feitos == nil {
		r.Feitos = map[string]int{}
	}
	return r, nil
}

func (r roteiro) ensaios() int {
	n := 0
	for _, v := range r.Feitos {
		n += v
	}
	return n
}

// ensaioDoTurno decide a que ensaio a fala do agens pertence. Quando o operador
// não responde, o modelo repete o ensaio, às vezes com outras palavras ou sem
// chamar a ferramenta: sem reconhecer isso a próxima resposta ficaria sem par e
// a repetição contaria como ensaio novo.
func (r roteiro) ensaioDoTurno(pedido *Ensaio, texto string) (atual *Ensaio, repetiu bool) {
	ultimo := r.UltimoEnsaio
	if pedido != nil {
		return pedido, ultimo != nil && pedido.Cenario == ultimo.Cenario
	}
	if ultimo != nil && citaFala(texto, ultimo.Fala) {
		return ultimo, true
	}
	return nil, false
}

// somarPreferencias guarda as novas. O histórico só tem texto, então o modelo
// não lembra do que já registrou e costuma registrar de novo.
func (r *roteiro) somarPreferencias(novas []Preferencia) {
	for _, n := range novas {
		repetida := slices.ContainsFunc(r.Preferencias, func(p Preferencia) bool {
			return p.Campo == n.Campo && normalizar(p.Valor) == normalizar(n.Valor)
		})
		if !repetida {
			r.Preferencias = append(r.Preferencias, n)
		}
	}
}

// falaVista devolve a fala como o operador a viu. É ela que o modelo copia do
// histórico ao repetir o ensaio, e pode diferir do que ele passou à ferramenta.
func falaVista(texto, fala string) string {
	if m := aspas.FindAllStringSubmatch(texto, -1); len(m) > 0 {
		ultima := m[len(m)-1]
		return ultima[1] + ultima[2]
	}
	return fala
}

// citaFala exige as aspas: fala curta como "oi" casaria dentro de "foi".
func citaFala(texto, fala string) bool {
	return strings.Contains(texto, `"`+fala+`"`) || strings.Contains(texto, "“"+fala+"”")
}

// Abertura é a resposta de POST /sessoes.
type Abertura struct {
	SessaoID string `json:"sessaoId"`
	Resposta string `json:"resposta"`
	Nova     bool   `json:"nova"`
}

// Resposta é a resposta de POST /sessoes/{id}/mensagens.
type Resposta struct {
	Resposta        string `json:"resposta"`
	EnsaiosFeitos   int    `json:"ensaiosFeitos"`
	SugerirEncerrar bool   `json:"sugerirEncerrar"`
}

// Encerramento é a resposta de POST /sessoes/{id}/encerrar.
type Encerramento struct {
	VersaoNova          *int         `json:"versaoNova"`
	Mudancas            []Mudanca    `json:"mudancas"`
	AguardandoEvidencia []Aguardando `json:"aguardandoEvidencia"`
	NotasNovas          []NotaResumo `json:"notasNovas"`
	ExtracaoPendente    bool         `json:"extracaoPendente,omitempty"`
}

// Abrir é o fluxo T-F1: devolve a sessão aberta do operador ou cria uma, com a
// primeira fala do agens. A abertura que falhou no LLM é refeita na próxima
// chamada, porque a sessão fica sem fala.
func (s *Servico) Abrir(ctx context.Context, ag agentes.Agente, op Operador) (Abertura, error) {
	lim := ag.Treino.Limites
	ctx, cancel := context.WithTimeout(ctx, time.Duration(lim.TurnoTimeoutS)*time.Second)
	defer cancel()

	sess, nova, err := s.Store.AbrirSessao(ctx, store.SessaoNova{
		Agente: ag.ID, Tenant: op.Tenant, OperadorID: op.ID, RoteiroJSON: []byte(`{"feitos":{},"preferencias":[]}`),
		InicioDia: s.inicioDia(), LimiteDia: lim.SessoesDiaTenant,
	})
	if err != nil {
		return Abertura{}, err
	}
	liberar, err := s.Trava.Adquirir(ctx, "sessao:"+sess.ID)
	if err != nil {
		return Abertura{}, fmt.Errorf("%w: %v", ErrIndisponivel, err)
	}
	defer liberar()

	msgs, err := s.Store.MensagensTreino(ctx, sess.ID)
	if err != nil {
		return Abertura{}, err
	}
	if fala := ultimaFala(msgs); fala != "" {
		return Abertura{SessaoID: sess.ID, Resposta: fala, Nova: nova}, nil
	}
	t, err := s.turno(ctx, ag, op, sess, msgs, "")
	if err != nil {
		return Abertura{}, err
	}
	return Abertura{SessaoID: sess.ID, Resposta: t.resposta, Nova: nova}, nil
}

// Mensagem é o fluxo T-F2: uma mensagem do operador, uma resposta do agens.
func (s *Servico) Mensagem(ctx context.Context, ag agentes.Agente, op Operador, sessaoID, conteudo string) (Resposta, error) {
	lim := ag.Treino.Limites
	conteudo = strings.TrimSpace(conteudo)
	if conteudo == "" {
		return Resposta{}, ErrTextoVazio
	}
	if utf8.RuneCountInString(conteudo) > lim.TextoMax {
		return Resposta{}, ErrTextoLongo
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(lim.TurnoTimeoutS)*time.Second)
	defer cancel()

	sess, err := s.Store.SessaoTreino(ctx, ag.ID, op.Tenant, sessaoID)
	if err != nil {
		return Resposta{}, err
	}
	liberar, err := s.Trava.Adquirir(ctx, "sessao:"+sess.ID)
	if err != nil {
		return Resposta{}, fmt.Errorf("%w: %v", ErrIndisponivel, err)
	}
	defer liberar()

	// Relê com a trava: outra chamada pode ter encerrado a sessão na espera.
	if sess, err = s.Store.SessaoTreino(ctx, ag.ID, op.Tenant, sessaoID); err != nil {
		return Resposta{}, err
	}
	if sess.Estado != store.SessaoAberta {
		return Resposta{}, store.ErrSessaoEncerrada
	}
	msgs, err := s.Store.MensagensTreino(ctx, sess.ID)
	if err != nil {
		return Resposta{}, err
	}
	if len(msgs) >= lim.MensagensSessao {
		return Resposta{}, ErrSessaoCheia
	}
	t, err := s.turno(ctx, ag, op, sess, msgs, conteudo)
	if err != nil {
		return Resposta{}, err
	}
	return Resposta{Resposta: t.resposta, EnsaiosFeitos: t.ensaios, SugerirEncerrar: t.ensaios >= ensaiosSugestao}, nil
}

// Encerrar é o fluxo T-F3: fecha a sessão, extrai e devolve o que mudou. Chamar
// de novo não reaplica nada, porque o sistema pode repetir depois de um timeout:
// devolve a versão gerada, sem o detalhe.
func (s *Servico) Encerrar(ctx context.Context, ag agentes.Agente, op Operador, sessaoID string) (Encerramento, error) {
	ctx, cancel := context.WithTimeout(ctx, time.Duration(ag.Treino.Limites.TurnoTimeoutS)*time.Second)
	defer cancel()

	sess, err := s.Store.SessaoTreino(ctx, ag.ID, op.Tenant, sessaoID)
	if err != nil {
		return Encerramento{}, err
	}
	liberar, err := s.Trava.Adquirir(ctx, "sessao:"+sess.ID)
	if err != nil {
		return Encerramento{}, fmt.Errorf("%w: %v", ErrIndisponivel, err)
	}
	defer liberar()
	if err := s.Store.EncerrarSessao(ctx, sess.ID, s.agora()); err != nil {
		return Encerramento{}, err
	}
	// Relê com a trava: o estado mudou aqui ou numa chamada que estava na frente.
	if sess, err = s.Store.SessaoTreino(ctx, ag.ID, op.Tenant, sessaoID); err != nil {
		return Encerramento{}, err
	}
	return s.extrair(ctx, ag, sess)
}

// ultimaFala devolve a última fala do agens na sessão.
func ultimaFala(msgs []store.MensagemTreino) string {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Papel == store.PapelAgente && msgs[i].Conteudo != nil {
			return *msgs[i].Conteudo
		}
	}
	return ""
}

// resultadoTurno é o que o turno devolve a Abrir e Mensagem.
type resultadoTurno struct {
	resposta string
	ensaios  int
}

// exec é o estado de um turno.
type exec struct {
	s      *Servico
	ctx    context.Context
	modelo string
	iter   int
	tin    int
	tout   int
	ferr   []store.FerramentaLog
	ensaio *Ensaio
	prefs  []Preferencia
	inicio time.Time
	sess   store.SessaoTreino
	op     Operador
	doOp   []string
}

// turno chama o modelo e grava o resultado. O operador só é gravado junto com
// a resposta (S20): se o LLM falhar, reenviar a mensagem não a duplica nem
// perde o vínculo com o ensaio. conteudo vazio = fala de abertura.
func (s *Servico) turno(ctx context.Context, ag agentes.Agente, op Operador, sess store.SessaoTreino,
	msgs []store.MensagemTreino, conteudo string) (resultadoTurno, error) {
	rot, err := lerRoteiro(sess.RoteiroJSON)
	if err != nil {
		return resultadoTurno{}, err
	}
	modelo := ag.Modelo
	if modelo == "" {
		modelo = s.Config.ModeloPadrao
	}
	e := &exec{s: s, ctx: ctx, modelo: modelo, inicio: s.agora(), sess: sess, op: op,
		doOp: falasDoOperador(msgs, conteudo), ferr: []store.FerramentaLog{}}

	texto, resultado, err := e.loop(promptTreino(op.Nome, rot.Feitos), historico(msgs, conteudo))
	if err != nil {
		e.registrar(resultadoErroLLM)
		e.s.log().Warn("treino: turno falhou", "agente", sess.Agente, "tenant", sess.Tenant,
			"sessao", sess.ID, "erro", err.Error())
		return resultadoTurno{}, fmt.Errorf("%w: %v", ErrIndisponivel, err)
	}

	var novas []store.MensagemTreino
	if conteudo != "" {
		tipo, cenario := tipoDaResposta(msgs)
		novas = append(novas, store.MensagemTreino{Papel: store.PapelOperador, Tipo: tipo, Cenario: cenario,
			Conteudo: &conteudo, CriadaEm: e.inicio})
	}
	fala := store.MensagemTreino{Papel: store.PapelAgente, Tipo: store.TipoLivre, Conteudo: &texto}
	if atual, repetiu := rot.ensaioDoTurno(e.ensaio, texto); atual != nil {
		fala.Tipo, fala.Cenario = store.TipoEnsaio, atual.Cenario
		if !repetiu {
			rot.Feitos[atual.Cenario]++
		}
		rot.UltimoEnsaio = &Ensaio{Cenario: atual.Cenario, Fala: falaVista(texto, atual.Fala)}
	}
	novas = append(novas, fala)
	rot.somarPreferencias(e.prefs)
	raw, err := json.Marshal(rot)
	if err != nil {
		return resultadoTurno{}, err
	}
	if err := s.Store.GravarTurnoTreino(ctx, sess.ID, novas, raw); err != nil {
		return resultadoTurno{}, err
	}
	e.registrar(resultado)
	return resultadoTurno{resposta: texto, ensaios: rot.ensaios()}, nil
}

// tipoDaResposta liga a mensagem do operador ao ensaio que ele está respondendo.
func tipoDaResposta(msgs []store.MensagemTreino) (tipo, cenario string) {
	if n := len(msgs); n > 0 && msgs[n-1].Papel == store.PapelAgente && msgs[n-1].Tipo == store.TipoEnsaio {
		return store.TipoRespostaEnsaio, msgs[n-1].Cenario
	}
	return store.TipoLivre, ""
}

// historico monta os turnos para o modelo: a sessão inteira (specs/06 §6.4).
func historico(msgs []store.MensagemTreino, conteudo string) []llm.Turn {
	out := []llm.Turn{{Role: "user", Parts: []llm.Part{{Text: inicioSessao}}}}
	for _, m := range msgs {
		if m.Conteudo == nil {
			continue
		}
		role := "user"
		if m.Papel == store.PapelAgente {
			role = "model"
		}
		out = append(out, llm.Turn{Role: role, Parts: []llm.Part{{Text: *m.Conteudo}}})
	}
	if conteudo != "" {
		out = append(out, llm.Turn{Role: "user", Parts: []llm.Part{{Text: conteudo}}})
	}
	return out
}

// loop roda o modelo com as ferramentas do treino até ele escrever a resposta.
func (e *exec) loop(system string, contents []llm.Turn) (texto, resultado string, err error) {
	tb := e.s.Config.ThinkingBudget
	maxOut := 1024
	if tb > 0 {
		maxOut += tb // os tokens de "pensamento" saem do mesmo teto
	}
	req := llm.GenerateRequest{Model: e.modelo, System: system, Tools: decls(),
		Temperature: e.s.Config.Temperature, MaxOutputTok: maxOut, ThinkingBudget: &tb}

	resultado = resultadoLimiteIter
	for e.iter < maxIteracoes {
		e.iter++
		req.Contents = contents
		resp, err := e.gerar(req)
		if err != nil {
			return "", "", err
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
			texto = strings.TrimSpace(strings.Join(partes, ""))
			resultado = resultadoOK
			break
		}
		// O turno do modelo entra inteiro, ThoughtSignature inclusa.
		contents = append(contents, llm.Turn{Role: "model", Parts: resp.Parts},
			llm.Turn{Role: "user", Parts: e.executar(calls)})
	}
	return e.textoFinal(texto), resultado, nil
}

func (e *exec) gerar(req llm.GenerateRequest) (*llm.GenerateResponse, error) {
	resp, err := e.s.LLM.Generate(e.ctx, req)
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

func (e *exec) executar(calls []llm.FunctionCall) []llm.Part {
	partes := make([]llm.Part, 0, len(calls))
	for _, c := range calls {
		r := e.ferramenta(c)
		log := store.FerramentaLog{Nome: c.Name, Status: 200, Codigo: "ok"}
		if ok, _ := r["ok"].(bool); !ok {
			log.Status, log.Codigo = 422, "recusada"
		}
		e.ferr = append(e.ferr, log)
		partes = append(partes, llm.Part{FunctionResponse: &llm.FunctionResponse{Name: c.Name, Response: r}})
	}
	return partes
}

func (e *exec) ferramenta(c llm.FunctionCall) map[string]any {
	switch c.Name {
	case nomeIniciarEnsaio:
		v, erro := validarEnsaio(c.Args)
		if erro != nil {
			return erro
		}
		e.ensaio = &v
	case nomeRegistrarPreferencia:
		p, erro := validarPreferencia(c.Args, e.doOp)
		if erro != nil {
			return erro
		}
		e.prefs = append(e.prefs, p)
	default:
		return recusa("ferramenta desconhecida")
	}
	return map[string]any{"ok": true}
}

// textoFinal garante que o operador veja a fala do ensaio: sem ela, a resposta
// dele não teria o que responder e o ensaio ficaria sem par para a extração.
func (e *exec) textoFinal(texto string) string {
	if e.ensaio != nil && !strings.ContainsAny(texto, `"“`) {
		fala := `"` + e.ensaio.Fala + `"`
		if texto == "" {
			texto = fala
		} else {
			texto += "\n\n" + fala
		}
	}
	if texto == "" {
		return textoRepetir
	}
	return agent.FormatarWhatsApp(texto)
}

// registrar grava o turno na auditoria de custo. Falha aqui só loga: o operador
// já tem a resposta. O contato do turno é o operador.
func (e *exec) registrar(resultado string) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(e.ctx), 5*time.Second)
	defer cancel()
	t := store.Turno{
		ConversaID:  store.ConversaID{Agente: e.sess.Agente, Tenant: e.sess.Tenant, Contato: e.op.ID},
		Modo:        store.ModoTreino,
		MensagemIDs: []string{uuid.NewString()},
		Modelo:      e.modelo, Iteracoes: e.iter, Ferramentas: e.ferr,
		TokensEntrada: e.tin, TokensSaida: e.tout, CustoMicroUSD: llm.Custo(e.modelo, e.tin, e.tout),
		LatenciaMS: e.s.agora().Sub(e.inicio).Milliseconds(), Resultado: resultado,
	}
	if err := e.s.Store.RegistrarTurno(ctx, t); err != nil {
		e.s.log().Error("treino: turno não registrado", "sessao", e.sess.ID, "erro", err.Error())
		return
	}
	e.s.log().Info("treino: turno", "agente", e.sess.Agente, "tenant", e.sess.Tenant, "sessao", e.sess.ID,
		"resultado", resultado, "iteracoes", e.iter, "ferramentas", len(e.ferr), "tokens_entrada", e.tin,
		"tokens_saida", e.tout, "custo_micro_usd", t.CustoMicroUSD, "latencia_ms", t.LatenciaMS)
}
