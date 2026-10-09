package atendimento

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"sola-agens/internal/agent"
	"sola-agens/internal/agentes"
	"sola-agens/internal/entrada"
	"sola-agens/internal/httpapi"
	"sola-agens/internal/mensagem"
	"sola-agens/internal/store"
	"sola-agens/internal/trava/memoria"
)

type runnerOrdem struct {
	mu    sync.Mutex
	ordem []string
	trava chan struct{} // se != nil, o 1º Run espera
}

func (r *runnerOrdem) Run(_ context.Context, t agent.Turno) error {
	if r.trava != nil {
		<-r.trava
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	ids := ""
	for _, m := range t.Mensagens {
		ids += m.ID + ","
	}
	r.ordem = append(r.ordem, "turno:"+ids+"ticket="+t.Ticket)
	return nil
}

func (r *runnerOrdem) DeMim(_ context.Context, _ *agent.Agente, _ store.ConversaID, m mensagem.Mensagem) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ordem = append(r.ordem, "demim:"+m.ID)
	return nil
}

func servicoDeTeste(r *runnerOrdem, rajadaMs int) (*Servico, agentes.Agente) {
	reg := agentes.Agente{ID: "eliza", Limites: agentes.Limites{RajadaMs: rajadaMs, RajadaMaxMs: rajadaMs * 4, TurnoTimeoutS: 5}}
	conj := &Conjunto{Agentes: map[string]*agent.Agente{"eliza": agent.NovoAgente(reg, nil, nil, nil, "m")}, Registro: []agentes.Agente{reg}}
	s := Novo(Opcoes{Conjunto: conj, Runner: r, Trava: &memoria.Trava{}, MaxConversas: 100,
		Log: slog.New(slog.NewJSONHandler(io.Discard, nil))})
	return s, reg
}

func entradaDe(id, ticket string, deMim bool) entrada.Entrada {
	return entrada.Entrada{Tenant: "org", Contato: "5511999990000", Ticket: ticket, DeMim: deMim,
		Mensagem: mensagem.Mensagem{ID: id, Texto: id, RecebidaEm: time.Now()}}
}

// A rajada aberta da conversa entra na fila ANTES do deMim (specs/03 F8).
func TestDeMimDescarregaARajadaAbertaAntes(t *testing.T) {
	r := &runnerOrdem{}
	s, reg := servicoDeTeste(r, 3_600_000) // janela de 1 h: só o Flush fecha
	ctx := context.Background()

	if err := s.Mensagem(ctx, reg, entradaDe("m1", "t1", false)); err != nil {
		t.Fatal(err)
	}
	if err := s.Mensagem(ctx, reg, entradaDe("m2", "t2", false)); err != nil {
		t.Fatal(err)
	}
	if err := s.DeMim(ctx, reg, entradaDe("h1", "", true)); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(2 * time.Second); err != nil {
		t.Fatal(err)
	}
	want := []string{"turno:m1,m2,ticket=t2", "demim:h1"}
	if len(r.ordem) != 2 || r.ordem[0] != want[0] || r.ordem[1] != want[1] {
		t.Fatalf("ordem = %v, quer %v", r.ordem, want)
	}
}

func TestCloseDescarregaORestoDoDebounce(t *testing.T) {
	r := &runnerOrdem{}
	s, reg := servicoDeTeste(r, 3_600_000)
	s.Mensagem(context.Background(), reg, entradaDe("m1", "t1", false))
	if err := s.Close(2 * time.Second); err != nil {
		t.Fatal(err)
	}
	if len(r.ordem) != 1 {
		t.Fatalf("o que estava acumulado deveria virar turno no Close: %v", r.ordem)
	}
}

func TestFilaCheiaViraErrFilaCheia(t *testing.T) {
	r := &runnerOrdem{trava: make(chan struct{})}
	s, reg := servicoDeTeste(r, 3_600_000)
	s.Pool.Cap = 1
	ctx := context.Background()

	// 1º deMim ocupa a goroutine (preso no Run do turno anterior), 2º enche a fila, 3º estoura
	s.Mensagem(ctx, reg, entradaDe("m1", "t1", false))
	s.Deb.Flush(store.ConversaID{Agente: "eliza", Tenant: "org", Contato: "5511999990000"})
	time.Sleep(50 * time.Millisecond) // o worker pega o turno e trava
	if err := s.DeMim(ctx, reg, entradaDe("h1", "", true)); err != nil {
		t.Fatal(err)
	}
	err := s.DeMim(ctx, reg, entradaDe("h2", "", true))
	if !errors.Is(err, httpapi.ErrFilaCheia) {
		t.Fatalf("err = %v, quer httpapi.ErrFilaCheia", err)
	}
	close(r.trava)
	s.Close(2 * time.Second)
}

func TestAgenteSemRuntime(t *testing.T) {
	r := &runnerOrdem{}
	s, _ := servicoDeTeste(r, 100)
	defer s.Close(time.Second)
	if err := s.Mensagem(context.Background(), agentes.Agente{ID: "outro"}, entradaDe("m", "t", false)); err == nil {
		t.Error("agente desconhecido deveria dar erro")
	}
}
