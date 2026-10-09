// Package debounce junta a rajada de mensagens de uma conversa num só item
// antes da fila (specs/03 F8, specs/05 §4).
package debounce

import (
	"sort"
	"sync"
	"time"

	"sola-agens/internal/mensagem"
	"sola-agens/internal/store"
)

// Lote é o que sai quando a janela fecha: as mensagens em ordem de RecebidaEm,
// o ticket mais recente e o nome de exibição mais recente.
type Lote struct {
	Conversa     store.ConversaID
	Mensagens    []mensagem.Mensagem
	Ticket       string
	NomeExibicao string
}

// Relogio é o tempo injetável dos testes.
type Relogio interface {
	Agora() time.Time
	// DepoisDe agenda f; o Timer devolvido cancela.
	DepoisDe(d time.Duration, f func()) Timer
}

// Timer é o pedaço de *time.Timer que o debounce usa.
type Timer interface{ Stop() bool }

type relogioReal struct{}

func (relogioReal) Agora() time.Time { return time.Now() }
func (relogioReal) DepoisDe(d time.Duration, f func()) Timer {
	return time.AfterFunc(d, f)
}

type aberta struct {
	lote     Lote
	primeira time.Time
	timer    Timer
	geracao  int // invalida o disparo de um timer já substituído
}

// Debounce acumula mensagens por conversa.
type Debounce struct {
	// Entregar recebe cada lote fechado. É chamado COM o lock do debounce, para
	// que Flush seja síncrono e não perca a ordem: não pode bloquear nem chamar o
	// Debounce (o enfileirar do worker é não bloqueante). O erro de uma entrega
	// agendada (fila cheia) é de quem entrega logar.
	Entregar func(Lote) error
	// Max é o teto de conversas abertas (AGENS_MAX_CONVERSAS); acima disso a
	// mensagem é entregue direto, sem esperar.
	Max     int
	Relogio Relogio

	mu      sync.Mutex
	abertas map[string]*aberta
	fechado bool
}

// New cria o debounce. relogio nil = tempo real.
func New(entregar func(Lote) error, max int, relogio Relogio) *Debounce {
	if relogio == nil {
		relogio = relogioReal{}
	}
	return &Debounce{Entregar: entregar, Max: max, Relogio: relogio, abertas: map[string]*aberta{}}
}

// Adicionar põe a mensagem na rajada da conversa. A janela reinicia a cada
// mensagem (janela), limitada pelo teto desde a primeira (teto). O erro só vem
// da entrega direta (debounce cheio ou fechado).
func (d *Debounce) Adicionar(c store.ConversaID, janela, teto time.Duration, m mensagem.Mensagem, ticket, nomeExibicao string) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	chave := c.Chave()
	a := d.abertas[chave]
	if a == nil {
		if d.fechado || len(d.abertas) >= d.Max {
			return d.Entregar(Lote{Conversa: c, Mensagens: []mensagem.Mensagem{m}, Ticket: ticket, NomeExibicao: nomeExibicao})
		}
		a = &aberta{lote: Lote{Conversa: c}, primeira: d.Relogio.Agora()}
		d.abertas[chave] = a
	}
	a.lote.Mensagens = append(a.lote.Mensagens, m)
	a.lote.Ticket = ticket // o mais recente
	if nomeExibicao != "" {
		a.lote.NomeExibicao = nomeExibicao
	}

	agora := d.Relogio.Agora()
	prazo := agora.Add(janela)
	if limite := a.primeira.Add(teto); prazo.After(limite) {
		prazo = limite
	}
	espera := prazo.Sub(agora)
	if espera < 0 {
		espera = 0
	}
	if a.timer != nil {
		a.timer.Stop()
	}
	a.geracao++
	geracao := a.geracao
	a.timer = d.Relogio.DepoisDe(espera, func() { d.fechar(chave, geracao) })
	return nil
}

func (d *Debounce) fechar(chave string, geracao int) {
	d.mu.Lock()
	defer d.mu.Unlock()
	a := d.abertas[chave]
	if a == nil || a.geracao != geracao {
		return
	}
	d.entregar(chave, a)
}

// Flush entrega agora, de forma síncrona, a rajada aberta da conversa (se houver).
// O deMim usa antes de entrar na fila, para manter a ordem.
func (d *Debounce) Flush(c store.ConversaID) {
	d.mu.Lock()
	defer d.mu.Unlock()
	chave := c.Chave()
	if a := d.abertas[chave]; a != nil {
		d.entregar(chave, a)
	}
}

// Close descarrega tudo que estiver acumulado; depois dele, Adicionar entrega
// direto.
func (d *Debounce) Close() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.fechado = true
	for chave, a := range d.abertas {
		d.entregar(chave, a)
	}
}

// entregar exige d.mu.
func (d *Debounce) entregar(chave string, a *aberta) {
	delete(d.abertas, chave)
	if a.timer != nil {
		a.timer.Stop()
	}
	sort.SliceStable(a.lote.Mensagens, func(i, j int) bool {
		return a.lote.Mensagens[i].RecebidaEm.Before(a.lote.Mensagens[j].RecebidaEm)
	})
	_ = d.Entregar(a.lote)
}

// Abertas devolve quantas conversas estão acumulando.
func (d *Debounce) Abertas() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.abertas)
}
