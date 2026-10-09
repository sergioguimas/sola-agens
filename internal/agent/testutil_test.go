package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"sola-agens/internal/agentes"
	"sola-agens/internal/conector"
	"sola-agens/internal/llm"
	"sola-agens/internal/mensagem"
	"sola-agens/internal/openapi"
	"sola-agens/internal/sistema"
	"sola-agens/internal/store"
	"sola-agens/internal/store/sqlite"
)

// bufSeguro é um io.Writer seguro para o logger de vários goroutines.
type bufSeguro struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (w *bufSeguro) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.b.Write(p)
}

func (w *bufSeguro) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.b.String()
}

// gravador guarda os turnos registrados (a interface Store não os lê de volta).
type gravador struct {
	store.Store
	mu     sync.Mutex
	turnos []store.Turno
}

func (g *gravador) RegistrarTurno(ctx context.Context, t store.Turno) error {
	g.mu.Lock()
	g.turnos = append(g.turnos, t)
	g.mu.Unlock()
	return g.Store.RegistrarTurno(ctx, t)
}

func (g *gravador) unico(t *testing.T) store.Turno {
	t.Helper()
	g.mu.Lock()
	defer g.mu.Unlock()
	if len(g.turnos) != 1 {
		t.Fatalf("turnos registrados = %d, quer 1: %+v", len(g.turnos), g.turnos)
	}
	return g.turnos[0]
}

type escalada struct{ Motivo, Resumo string }

// sistemaFalso implementa sistema.Sistema.
type sistemaFalso struct {
	mu          sync.Mutex
	Ctx         sistema.Contexto
	CtxErr      error
	CtxChamadas int
	Enviadas    []string
	EnvioErr    error
	SemID       bool
	Escaladas   []escalada
	EscalarErrs []error // consumidos em ordem; além do fim, sucesso
	Tickets     []string
}

func (s *sistemaFalso) Contexto(_ context.Context, id conector.Identidade) (sistema.Contexto, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.CtxChamadas++
	s.Tickets = append(s.Tickets, id.Ticket)
	return s.Ctx, s.CtxErr
}

func (s *sistemaFalso) Responder(_ context.Context, id conector.Identidade, texto string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Tickets = append(s.Tickets, id.Ticket)
	if s.EnvioErr != nil {
		return "", s.EnvioErr
	}
	s.Enviadas = append(s.Enviadas, texto)
	if s.SemID {
		return "", nil
	}
	return "out-" + string(rune('0'+len(s.Enviadas))), nil
}

func (s *sistemaFalso) Escalar(_ context.Context, id conector.Identidade, motivo, resumo string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Tickets = append(s.Tickets, id.Ticket)
	s.Escaladas = append(s.Escaladas, escalada{motivo, resumo})
	if i := len(s.Escaladas) - 1; i < len(s.EscalarErrs) && s.EscalarErrs[i] != nil {
		return false, s.EscalarErrs[i]
	}
	return true, nil
}

func (s *sistemaFalso) BaixarMidia(context.Context, conector.Identidade, string) (sistema.Midia, error) {
	return sistema.Midia{}, nil
}

type chamada struct {
	Nome   string
	Args   map[string]any
	Ticket string
}

// execFalso implementa conector.Executor.
type execFalso struct {
	mu       sync.Mutex
	Chamadas []chamada
	Fn       func(f openapi.Ferramenta, args map[string]any) (conector.Resultado, error)
}

func (e *execFalso) Executar(_ context.Context, f openapi.Ferramenta, args map[string]any, id conector.Identidade) (conector.Resultado, error) {
	e.mu.Lock()
	e.Chamadas = append(e.Chamadas, chamada{f.Decl.Name, args, id.Ticket})
	fn := e.Fn
	e.mu.Unlock()
	if fn != nil {
		return fn(f, args)
	}
	status := 200
	if f.Metodo == "POST" {
		status = 201
	}
	return conector.Resultado{Dados: json.RawMessage(`{"ok":true}`), Status: status}, nil
}

func (e *execFalso) nomes() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	var out []string
	for _, c := range e.Chamadas {
		out = append(out, c.Nome)
	}
	return out
}

func ferramentasDeTeste() []openapi.Ferramenta {
	mk := func(nome, metodo string, args map[string]openapi.Destino) openapi.Ferramenta {
		return openapi.Ferramenta{
			Decl:   llm.ToolDecl{Name: nome, Description: nome},
			Metodo: metodo, Caminho: "/x/" + nome, Args: args,
		}
	}
	return []openapi.Ferramenta{
		mk("listarServicos", "GET", nil),
		mk("criarAgendamento", "POST", map[string]openapi.Destino{"inicio": openapi.DestinoBody, "observacao": openapi.DestinoBody}),
		mk("confirmarAgendamento", "POST", map[string]openapi.Destino{"id": openapi.DestinoPath}),
		mk("cancelarAgendamento", "POST", map[string]openapi.Destino{"id": openapi.DestinoPath}),
	}
}

func registroDeTeste() agentes.Agente {
	return agentes.Agente{
		ID: "eliza", ModoIdentidade: agentes.ModoEncaminhado,
		Operacoes: agentes.Operacoes{
			Contexto: "obterContexto", Responder: "enviarMensagem", Escalar: "escalarParaEquipe", Midia: "baixarMidia",
			Confirmar: "confirmarAgendamento", Cancelar: "cancelarAgendamento",
		},
		Midia: map[string]string{"audio": "processar", "imagem": "resposta_fixa", "outro": "resposta_fixa"},
		Limites: agentes.Limites{MaxIter: 8, RajadaMs: 3000, RajadaMaxMs: 8000, MemoriaMensagens: 20,
			MemoriaHoras: 24, AudioMaxS: 120, TurnoTimeoutS: 90},
	}
}

func contextoDeTeste() sistema.Contexto {
	almoco := &sistema.Almoco{Inicio: "12:00", Fim: "13:00"}
	abre, fecha := "09:00", "18:00"
	ins := "Não atendemos feriados."
	return sistema.Contexto{
		Agora:       sistema.Momento{UTC: "2026-10-09T17:30:00Z", Local: "2026-10-09T14:30"},
		FusoHorario: "America/Sao_Paulo",
		Organizacao: sistema.Organizacao{Nome: "Studio Bela", Nicho: "beleza", Termos: sistema.Termos{
			Genero: "m", Cliente: "cliente", ClientePlural: "clientes", Profissional: "profissional",
			ProfissionalPlural: "profissionais", Servico: "serviço", ServicoPlural: "serviços",
			Agendamento: "agendamento", AgendamentoPlural: "agendamentos"}},
		Expediente: sistema.Expediente{DiasDaSemana: []int{1, 2, 3, 4, 5, 6}, Abertura: &abre, Fechamento: &fecha, Almoco: almoco},
		Politica: sistema.Politica{AntecedenciaMinimaMinutos: 120, JanelaMaximaDias: 30, MaxAgendamentosAtivos: 3,
			InstrucoesAtendimento: &ins},
		Identificacao: sistema.Identificacao{Situacao: sistema.SituacaoIdentificado,
			Cliente: &sistema.ClienteIdentificado{ID: "c1", PrimeiroNome: "Ana"}},
		Agendamentos: []sistema.AgendamentoResumo{{
			ID: "ag1", Status: "pending",
			Inicio:  sistema.Momento{Local: "2026-10-15T14:00"},
			Servico: &sistema.Ref{ID: "s1", Nome: "Corte"}, Profissional: &sistema.Ref{ID: "p1", Nome: "Bia"},
		}},
	}
}

var conversaDeTeste = store.ConversaID{Agente: "eliza", Tenant: "org1", Contato: "5511999990000"}

// amb é o ambiente de um teste do agente.
type amb struct {
	t     *testing.T
	motor *Motor
	st    *gravador
	llm   *llm.Fake
	sis   *sistemaFalso
	ex    *execFalso
	ag    *Agente
	logs  *bufSeguro
	reg   agentes.Agente
}

func novoAmb(t *testing.T) *amb {
	t.Helper()
	real, err := sqlite.Open(filepath.Join(t.TempDir(), "agens.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { real.Close() })

	a := &amb{t: t, st: &gravador{Store: real}, llm: &llm.Fake{}, sis: &sistemaFalso{Ctx: contextoDeTeste()},
		ex: &execFalso{}, logs: &bufSeguro{}, reg: registroDeTeste()}
	log := slog.New(slog.NewJSONHandler(a.logs, &slog.HandlerOptions{Level: slog.LevelInfo}))
	a.motor = &Motor{Store: a.st, LLM: a.llm, Log: log, Config: Config{PausaHumanaH: 12}}
	a.montar()
	return a
}

// montar (re)cria o runtime com o registro atual (testes mudam a.reg antes).
func (a *amb) montar() {
	a.ag = NovoAgente(a.reg, ferramentasDeTeste(), a.ex, a.sis, "gemini-2.5-flash")
}

func (a *amb) turno(textos ...string) Turno {
	ms := make([]mensagem.Mensagem, len(textos))
	for i, tx := range textos {
		ms[i] = mensagem.Mensagem{ID: "in-" + string(rune('1'+i)), Texto: tx,
			RecebidaEm: time.Date(2026, 10, 9, 14, 30, i, 0, time.UTC)}
	}
	return Turno{Agente: a.ag, Conversa: conversaDeTeste, Mensagens: ms, Ticket: "tk-1", NomeExibicao: "Ana W"}
}

func (a *amb) run(textos ...string) store.Turno {
	a.t.Helper()
	if err := a.motor.Run(context.Background(), a.turno(textos...)); err != nil {
		a.t.Fatalf("Run: %v", err)
	}
	return a.st.unico(a.t)
}

func (a *amb) janela() []string {
	a.t.Helper()
	ms, err := a.st.Janela(context.Background(), conversaDeTeste, 50, time.Unix(0, 0))
	if err != nil {
		a.t.Fatal(err)
	}
	var out []string
	for _, m := range ms {
		c := "<nil>"
		if m.Conteudo != nil {
			c = *m.Conteudo
		}
		out = append(out, m.Papel+":"+c)
	}
	return out
}

func (a *amb) semLLM() {
	a.t.Helper()
	if len(a.llm.Reqs) != 0 {
		a.t.Errorf("o LLM foi chamado %d vezes, quer 0", len(a.llm.Reqs))
	}
}

func chamadaLLM(nome string, args map[string]any) llm.Part {
	return llm.Part{FunctionCall: &llm.FunctionCall{Name: nome, Args: args}}
}

func respComTokens(r llm.GenerateResponse, in, out int) llm.GenerateResponse {
	r.InputTokens, r.OutputTokens = in, out
	return r
}

func contem(t *testing.T, s, quer string) {
	t.Helper()
	if !strings.Contains(s, quer) {
		t.Errorf("%q não contém %q", s, quer)
	}
}

type openapiFerramenta = openapi.Ferramenta

func slogDebug(w io.Writer) *slog.Logger {
	return slog.New(slog.NewJSONHandler(w, &slog.HandlerOptions{Level: slog.LevelDebug}))
}
