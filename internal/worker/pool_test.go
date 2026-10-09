package worker

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"sola-agens/internal/agent"
	"sola-agens/internal/mensagem"
	"sola-agens/internal/store"
	"sola-agens/internal/trava/memoria"
)

var silencio = slog.New(slog.NewTextHandler(io.Discard, nil))

// runner registra a ordem e deixa o teste segurar a execução.
type runner struct {
	mu       sync.Mutex
	ordem    []string
	rodando  map[string]int // execuções simultâneas por contato
	maxSimul map[string]int
	segurar  chan struct{} // se != nil, Run espera isto (ou o ctx)
	iniciou  chan string   // se != nil, avisa o início de cada item
	ctxErr   map[string]error
}

func novoRunner() *runner {
	return &runner{rodando: map[string]int{}, maxSimul: map[string]int{}, ctxErr: map[string]error{}}
}

func (r *runner) entrar(contato, rotulo string) {
	r.mu.Lock()
	r.rodando[contato]++
	if r.rodando[contato] > r.maxSimul[contato] {
		r.maxSimul[contato] = r.rodando[contato]
	}
	r.ordem = append(r.ordem, rotulo)
	r.mu.Unlock()
	if r.iniciou != nil {
		r.iniciou <- rotulo
	}
}

func (r *runner) sair(contato string) {
	r.mu.Lock()
	r.rodando[contato]--
	r.mu.Unlock()
}

func (r *runner) Run(ctx context.Context, t agent.Turno) error {
	rotulo := t.Mensagens[0].ID
	r.entrar(t.Conversa.Contato, rotulo)
	defer r.sair(t.Conversa.Contato)
	if r.segurar != nil {
		select {
		case <-r.segurar:
		case <-ctx.Done():
			r.mu.Lock()
			r.ctxErr[rotulo] = ctx.Err()
			r.mu.Unlock()
			return ctx.Err()
		}
	}
	return nil
}

func (r *runner) DeMim(_ context.Context, _ *agent.Agente, id store.ConversaID, msg mensagem.Mensagem) error {
	r.entrar(id.Contato, "demim:"+msg.ID)
	r.sair(id.Contato)
	return nil
}

func conversa(contato string) store.ConversaID {
	return store.ConversaID{Agente: "eliza", Tenant: "org", Contato: contato}
}

func turno(contato, id string) agent.Turno {
	return agent.Turno{Conversa: conversa(contato), Mensagens: []mensagem.Mensagem{{ID: id, Texto: id}}}
}

func novoPool(r Runner) *Pool {
	p := New(r, &memoria.Trava{}, silencio)
	p.Prazo = func(*agent.Agente) time.Duration { return 2 * time.Second }
	return p
}

func espera(t *testing.T, cond func() bool) {
	t.Helper()
	for i := 0; i < 400; i++ {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condição não ocorreu a tempo")
}

func TestOrdemPorConversaComDeMimNaMesmaFila(t *testing.T) {
	r := novoRunner()
	r.segurar = make(chan struct{})
	r.iniciou = make(chan string, 10)
	p := novoPool(r)

	p.EnfileirarTurno(turno("a", "t1"))
	<-r.iniciou // t1 rodando, preso
	p.EnfileirarDeMim(nil, conversa("a"), mensagem.Mensagem{ID: "h1"})
	p.EnfileirarTurno(turno("a", "t2"))
	close(r.segurar)
	if err := p.Close(2 * time.Second); err != nil {
		t.Fatal(err)
	}

	want := []string{"t1", "demim:h1", "t2"}
	for i, w := range want {
		if r.ordem[i] != w {
			t.Fatalf("ordem = %v, quer %v", r.ordem, want)
		}
	}
	if r.maxSimul["a"] != 1 {
		t.Errorf("simultâneos na mesma conversa = %d", r.maxSimul["a"])
	}
}

func TestConversasDiferentesEmParalelo(t *testing.T) {
	r := novoRunner()
	r.segurar = make(chan struct{})
	r.iniciou = make(chan string, 10)
	p := novoPool(r)
	p.EnfileirarTurno(turno("a", "ta"))
	p.EnfileirarTurno(turno("b", "tb"))
	// os dois começam sem que nenhum termine: só é possível em paralelo
	<-r.iniciou
	<-r.iniciou
	close(r.segurar)
	p.Close(2 * time.Second)
}

func TestFilaCheia(t *testing.T) {
	r := novoRunner()
	r.segurar = make(chan struct{})
	r.iniciou = make(chan string, 10)
	p := novoPool(r)
	p.Cap = 2

	p.EnfileirarTurno(turno("a", "t1"))
	<-r.iniciou // t1 saiu da fila e está rodando
	for _, id := range []string{"t2", "t3"} {
		if err := p.EnfileirarTurno(turno("a", id)); err != nil {
			t.Fatalf("%s: %v", id, err)
		}
	}
	if err := p.EnfileirarTurno(turno("a", "t4")); err != ErrFilaCheia {
		t.Fatalf("err = %v, quer ErrFilaCheia", err)
	}
	if err := p.EnfileirarDeMim(nil, conversa("a"), mensagem.Mensagem{ID: "h"}); err != ErrFilaCheia {
		t.Fatalf("deMim com fila cheia: err = %v", err)
	}
	if err := p.EnfileirarTurno(turno("b", "outra")); err != nil { // fila é por conversa
		t.Fatalf("outra conversa: %v", err)
	}
	close(r.segurar)
	p.Close(2 * time.Second)
	if err := p.EnfileirarTurno(turno("a", "tarde")); err != ErrFilaCheia {
		t.Errorf("depois do Close: err = %v", err)
	}
}

func TestTimeoutDoTurno(t *testing.T) {
	r := novoRunner()
	r.segurar = make(chan struct{}) // nunca liberado
	p := novoPool(r)
	p.Prazo = func(*agent.Agente) time.Duration { return 30 * time.Millisecond }
	p.EnfileirarTurno(turno("a", "t1"))
	p.EnfileirarTurno(turno("a", "t2"))
	if err := p.Close(2 * time.Second); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"t1", "t2"} {
		if r.ctxErr[id] != context.DeadlineExceeded {
			t.Errorf("%s: ctx.Err = %v, quer DeadlineExceeded", id, r.ctxErr[id])
		}
	}
}

func TestCloseEsperaOProcessamento(t *testing.T) {
	r := novoRunner()
	r.segurar = make(chan struct{})
	r.iniciou = make(chan string, 10)
	p := novoPool(r)
	p.EnfileirarTurno(turno("a", "t1"))
	<-r.iniciou
	go func() { time.Sleep(50 * time.Millisecond); close(r.segurar) }()
	if err := p.Close(2 * time.Second); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if r.ctxErr["t1"] != nil {
		t.Errorf("o turno foi cancelado: %v", r.ctxErr["t1"])
	}
}

func TestClosePrazoEsgotadoCancelaOsTurnos(t *testing.T) {
	r := novoRunner()
	r.segurar = make(chan struct{})
	r.iniciou = make(chan string, 10)
	p := novoPool(r)
	p.Prazo = func(*agent.Agente) time.Duration { return time.Minute }
	p.EnfileirarTurno(turno("a", "t1"))
	<-r.iniciou
	if err := p.Close(30 * time.Millisecond); err != ErrEncerramento {
		t.Fatalf("Close = %v, quer ErrEncerramento", err)
	}
	if r.ctxErr["t1"] != context.Canceled {
		t.Errorf("ctx.Err = %v, quer Canceled", r.ctxErr["t1"])
	}
}

func TestGoroutineOciosaMorreESeRecria(t *testing.T) {
	r := novoRunner()
	p := novoPool(r)
	p.Ocioso = 20 * time.Millisecond
	p.EnfileirarTurno(turno("a", "t1"))
	espera(t, func() bool { p.mu.Lock(); defer p.mu.Unlock(); return len(p.workers) == 0 })
	if err := p.EnfileirarTurno(turno("a", "t2")); err != nil {
		t.Fatal(err)
	}
	p.Close(2 * time.Second)
	if len(r.ordem) != 2 {
		t.Errorf("ordem = %v", r.ordem)
	}
}
