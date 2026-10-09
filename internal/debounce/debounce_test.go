package debounce

import (
	"sync"
	"testing"
	"time"

	"sola-agens/internal/mensagem"
	"sola-agens/internal/store"
)

// relogioFalso avança só quando o teste manda.
type relogioFalso struct {
	mu     sync.Mutex
	agora  time.Time
	timers []*timerFalso
}

type timerFalso struct {
	quando time.Time
	f      func()
	parado bool
	r      *relogioFalso
}

func (t *timerFalso) Stop() bool {
	t.r.mu.Lock()
	defer t.r.mu.Unlock()
	ja := t.parado
	t.parado = true
	return !ja
}

func (r *relogioFalso) Agora() time.Time {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.agora
}

func (r *relogioFalso) DepoisDe(d time.Duration, f func()) Timer {
	r.mu.Lock()
	defer r.mu.Unlock()
	t := &timerFalso{quando: r.agora.Add(d), f: f, r: r}
	r.timers = append(r.timers, t)
	return t
}

// avancar move o tempo e dispara, em ordem, os timers vencidos.
func (r *relogioFalso) avancar(d time.Duration) {
	r.mu.Lock()
	alvo := r.agora.Add(d)
	r.mu.Unlock()
	for {
		r.mu.Lock()
		var prox *timerFalso
		for _, t := range r.timers {
			if !t.parado && !t.quando.After(alvo) && (prox == nil || t.quando.Before(prox.quando)) {
				prox = t
			}
		}
		if prox == nil {
			r.agora = alvo
			r.mu.Unlock()
			return
		}
		prox.parado = true
		if prox.quando.After(r.agora) {
			r.agora = prox.quando
		}
		r.mu.Unlock()
		prox.f()
	}
}

type coletor struct {
	mu    sync.Mutex
	lotes []Lote
}

func (c *coletor) entregar(l Lote) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.lotes = append(c.lotes, l)
	return nil
}

func (c *coletor) n() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.lotes)
}

var (
	conv  = store.ConversaID{Agente: "eliza", Tenant: "org1", Contato: "5511999990000"}
	outra = store.ConversaID{Agente: "eliza", Tenant: "org1", Contato: "5511999991111"}
	t0    = time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
)

const (
	janela = 3 * time.Second
	teto   = 8 * time.Second
)

func msg(id string, seg int) mensagem.Mensagem {
	return mensagem.Mensagem{ID: id, Texto: id, RecebidaEm: t0.Add(time.Duration(seg) * time.Second)}
}

func novo(max int) (*Debounce, *relogioFalso, *coletor) {
	r := &relogioFalso{agora: t0}
	c := &coletor{}
	return New(c.entregar, max, r), r, c
}

func TestTresMensagensEmDoisSegundosViramUmItem(t *testing.T) {
	d, r, c := novo(10)
	d.Adicionar(conv, janela, teto, msg("m1", 0), "t1", "Ana")
	r.avancar(1 * time.Second)
	d.Adicionar(conv, janela, teto, msg("m2", 1), "t2", "")
	r.avancar(1 * time.Second)
	d.Adicionar(conv, janela, teto, msg("m3", 2), "t3", "Ana B")

	r.avancar(2900 * time.Millisecond) // a janela reiniciou na 3ª: ainda não fechou
	if c.n() != 0 {
		t.Fatalf("entregou antes da janela: %d", c.n())
	}
	r.avancar(200 * time.Millisecond)
	if c.n() != 1 {
		t.Fatalf("lotes = %d, quer 1", c.n())
	}
	l := c.lotes[0]
	if len(l.Mensagens) != 3 || l.Mensagens[0].ID != "m1" || l.Mensagens[2].ID != "m3" {
		t.Errorf("mensagens = %+v", l.Mensagens)
	}
	if l.Ticket != "t3" {
		t.Errorf("ticket = %q, quer o mais recente t3", l.Ticket)
	}
	if l.NomeExibicao != "Ana B" {
		t.Errorf("nome = %q, quer Ana B", l.NomeExibicao)
	}
	if l.Conversa != conv {
		t.Errorf("conversa = %+v", l.Conversa)
	}
	if d.Abertas() != 0 {
		t.Errorf("abertas = %d", d.Abertas())
	}
}

func TestTetoDeOitoSegundos(t *testing.T) {
	d, r, c := novo(10)
	// uma mensagem a cada 2 s mantém a janela de 3 s aberta para sempre, sem o teto
	for i := 0; i < 4; i++ {
		d.Adicionar(conv, janela, teto, msg("m", i*2), "t", "")
		r.avancar(2 * time.Second)
	}
	// 8 s desde a primeira: o teto fechou, mesmo com mensagem há 2 s
	if c.n() != 1 || len(c.lotes[0].Mensagens) != 4 {
		t.Fatalf("lotes = %d (quer 1 com 4 mensagens)", c.n())
	}
	// a próxima abre uma rajada nova
	d.Adicionar(conv, janela, teto, msg("m5", 8), "t", "")
	if d.Abertas() != 1 {
		t.Errorf("abertas = %d", d.Abertas())
	}
}

func TestOrdenaPorRecebidaEm(t *testing.T) {
	d, r, c := novo(10)
	d.Adicionar(conv, janela, teto, msg("tarde", 5), "t", "")
	d.Adicionar(conv, janela, teto, msg("cedo", 1), "t", "")
	r.avancar(janela)
	if got := c.lotes[0].Mensagens; got[0].ID != "cedo" || got[1].ID != "tarde" {
		t.Errorf("ordem = %v, %v", got[0].ID, got[1].ID)
	}
}

func TestFlushEhSincronoEIsolaPorConversa(t *testing.T) {
	d, r, c := novo(10)
	d.Adicionar(conv, janela, teto, msg("m1", 0), "t", "")
	d.Adicionar(outra, janela, teto, msg("o1", 0), "t", "")
	d.Flush(conv)
	if c.n() != 1 || c.lotes[0].Conversa != conv {
		t.Fatalf("flush entregou %d lotes: %+v", c.n(), c.lotes)
	}
	d.Flush(conv) // nada aberto: não faz nada
	if c.n() != 1 {
		t.Fatalf("segundo flush entregou de novo")
	}
	r.avancar(janela) // o timer da conversa descarregada não dispara; o da outra sim
	if c.n() != 2 || c.lotes[1].Conversa != outra {
		t.Fatalf("lotes = %+v", c.lotes)
	}
}

func TestAcimaDoTetoDeConversasEntregaDireto(t *testing.T) {
	d, _, c := novo(1)
	d.Adicionar(conv, janela, teto, msg("m1", 0), "t", "")
	if err := d.Adicionar(outra, janela, teto, msg("o1", 0), "t9", ""); err != nil {
		t.Fatal(err)
	}
	if c.n() != 1 || c.lotes[0].Conversa != outra || c.lotes[0].Ticket != "t9" {
		t.Fatalf("entrega direta = %+v", c.lotes)
	}
	// uma conversa já aberta continua acumulando mesmo com o teto cheio
	d.Adicionar(conv, janela, teto, msg("m2", 1), "t", "")
	if d.Abertas() != 1 {
		t.Errorf("abertas = %d", d.Abertas())
	}
}

func TestCloseDescarregaTudo(t *testing.T) {
	d, r, c := novo(10)
	d.Adicionar(conv, janela, teto, msg("m1", 0), "t", "")
	d.Adicionar(outra, janela, teto, msg("o1", 0), "t", "")
	d.Close()
	if c.n() != 2 {
		t.Fatalf("lotes = %d, quer 2", c.n())
	}
	r.avancar(time.Minute) // timers cancelados
	if c.n() != 2 {
		t.Fatalf("timer disparou depois do Close")
	}
	d.Adicionar(conv, janela, teto, msg("m2", 9), "t", "") // depois de fechado: direto
	if c.n() != 3 {
		t.Fatalf("depois do Close deveria entregar direto")
	}
}

func TestTempoReal(t *testing.T) {
	c := &coletor{}
	d := New(c.entregar, 10, nil)
	d.Adicionar(conv, 20*time.Millisecond, 100*time.Millisecond, msg("m1", 0), "t", "")
	for i := 0; i < 100 && c.n() == 0; i++ {
		time.Sleep(10 * time.Millisecond)
	}
	if c.n() != 1 {
		t.Fatal("o timer real não entregou")
	}
}
