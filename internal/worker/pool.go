// Package worker enfileira o trabalho de cada conversa e o executa em ordem,
// uma goroutine por conversa (specs/05 §4). Copiado do Pool do Smaug e
// adaptado: chave = agente:tenant:contato, item = turno OU deMim, Trava por
// item e timeout por turno.
package worker

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"sola-agens/internal/agent"
	"sola-agens/internal/mensagem"
	"sola-agens/internal/store"
	"sola-agens/internal/trava"
)

const (
	filaCap      = 100              // tamanho da fila por conversa (specs/05 §4)
	ociosoPadrao = 10 * time.Minute // a goroutine da conversa morre sem item por este tempo
	prazoPadrao  = 90 * time.Second
)

// ErrFilaCheia: a fila da conversa está cheia (ou o pool fechou). O handler
// traduz para httpapi.ErrFilaCheia (503).
var ErrFilaCheia = errors.New("fila da conversa cheia")

// ErrEncerramento: o Close estourou o prazo e cancelou os turnos em andamento.
var ErrEncerramento = errors.New("encerramento: prazo esgotado com turnos em andamento")

// Runner é o que o pool executa; *agent.Motor satisfaz.
type Runner interface {
	Run(ctx context.Context, t agent.Turno) error
	DeMim(ctx context.Context, ag *agent.Agente, id store.ConversaID, msg mensagem.Mensagem) error
}

// item é um tipo soma: exatamente um de turno ou deMim.
type item struct {
	conversa store.ConversaID
	turno    *agent.Turno
	deMim    *deMim
	ag       *agent.Agente
}

type deMim struct{ msg mensagem.Mensagem }

type chatWorker struct{ ch chan item }

// Pool distribui o trabalho por conversa, cada uma sequencial.
type Pool struct {
	Runner Runner
	Trava  trava.Trava
	Log    *slog.Logger
	// Prazo é o timeout do turno do agente (limites.turno_timeout_s).
	// Injetável nos testes; nil usa o limite do registro (90 s se zero).
	Prazo func(*agent.Agente) time.Duration
	// Ocioso é o tempo sem item depois do qual a goroutine da conversa sai.
	Ocioso time.Duration
	// Cap é o tamanho da fila por conversa.
	Cap int

	base   context.Context
	cancel context.CancelFunc

	mu      sync.Mutex
	workers map[string]*chatWorker
	closed  bool
	wg      sync.WaitGroup
}

// New cria um Pool pronto para uso.
func New(r Runner, t trava.Trava, log *slog.Logger) *Pool {
	if log == nil {
		log = slog.Default()
	}
	base, cancel := context.WithCancel(context.Background())
	return &Pool{Runner: r, Trava: t, Log: log, Ocioso: ociosoPadrao, Cap: filaCap,
		base: base, cancel: cancel, workers: map[string]*chatWorker{}}
}

// EnfileirarTurno empilha o turno na fila da conversa. ErrFilaCheia se a fila
// estiver cheia ou o pool fechado.
func (p *Pool) EnfileirarTurno(t agent.Turno) error {
	return p.enfileirar(item{conversa: t.Conversa, turno: &t, ag: t.Agente})
}

// EnfileirarDeMim empilha a mensagem deMim na MESMA fila da conversa: nunca roda
// em paralelo com o turno anterior (specs/03 F3).
func (p *Pool) EnfileirarDeMim(ag *agent.Agente, id store.ConversaID, msg mensagem.Mensagem) error {
	return p.enfileirar(item{conversa: id, deMim: &deMim{msg: msg}, ag: ag})
}

func (p *Pool) enfileirar(it item) error {
	chave := it.conversa.Chave()
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return ErrFilaCheia
	}
	w, ok := p.workers[chave]
	if !ok {
		w = &chatWorker{ch: make(chan item, p.Cap)}
		p.workers[chave] = w
		p.wg.Add(1)
		go p.loop(chave, w)
	}
	// O envio não bloqueante fica dentro do lock: Close fecha o canal sob o
	// mesmo lock, e mandar num canal fechado seria panic.
	select {
	case w.ch <- it:
		return nil
	default:
		return ErrFilaCheia
	}
}

// Len devolve o total de itens em espera.
func (p *Pool) Len() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	n := 0
	for _, w := range p.workers {
		n += len(w.ch)
	}
	return n
}

// Close para de aceitar itens e espera as goroutines esvaziarem as filas, até
// espera. Passado o prazo, cancela os turnos em andamento e devolve
// ErrEncerramento (o turno interrompido fica sem resposta; aceito e logado).
func (p *Pool) Close(espera time.Duration) error {
	p.mu.Lock()
	if !p.closed {
		p.closed = true
		for _, w := range p.workers {
			close(w.ch)
		}
	}
	p.mu.Unlock()

	feito := make(chan struct{})
	go func() { p.wg.Wait(); close(feito) }()
	select {
	case <-feito:
		p.cancel()
		return nil
	case <-time.After(espera):
		p.Log.Error("encerramento: prazo esgotado, cancelando turnos em andamento", "espera", espera.String())
		p.cancel()
		select { // um turno que ignora o contexto não pode travar o encerramento
		case <-feito:
		case <-time.After(5 * time.Second):
		}
		return ErrEncerramento
	}
}

func (p *Pool) loop(chave string, w *chatWorker) {
	defer p.wg.Done()
	ocioso := p.Ocioso
	if ocioso <= 0 {
		ocioso = ociosoPadrao
	}
	t := time.NewTimer(ocioso)
	defer t.Stop()
	for {
		select {
		case it, ok := <-w.ch:
			if !ok {
				return
			}
			p.processar(chave, it)
			if !t.Stop() {
				select {
				case <-t.C:
				default:
				}
			}
			t.Reset(ocioso)
		case <-t.C:
			// Sai só se nada chegou no meio: o enfileirar segura p.mu ao enviar.
			p.mu.Lock()
			if len(w.ch) == 0 {
				delete(p.workers, chave)
				p.mu.Unlock()
				return
			}
			p.mu.Unlock()
			t.Reset(ocioso)
		}
	}
}

func (p *Pool) prazo(ag *agent.Agente) time.Duration {
	if p.Prazo != nil {
		return p.Prazo(ag)
	}
	if ag != nil && ag.Registro.Limites.TurnoTimeoutS > 0 {
		return time.Duration(ag.Registro.Limites.TurnoTimeoutS) * time.Second
	}
	return prazoPadrao
}

// processar pega a Trava da conversa e roda o item com o prazo do turno.
func (p *Pool) processar(chave string, it item) {
	log := p.Log.With("agente", it.conversa.Agente, "tenant", it.conversa.Tenant,
		"conversa", mensagem.HashContato(it.conversa.Contato))
	defer func() {
		if r := recover(); r != nil { // um bug num turno não derruba a fila nem o processo
			log.Error("pânico no item da fila", "tipo", panicTipo(r))
		}
	}()

	ctx, cancel := context.WithTimeout(p.base, p.prazo(it.ag))
	defer cancel()
	liberar, err := p.Trava.Adquirir(ctx, chave)
	if err != nil {
		log.Error("trava não adquirida", "erro", err.Error())
		return
	}
	defer liberar()

	switch {
	case it.turno != nil:
		err = p.Runner.Run(ctx, *it.turno)
	case it.deMim != nil:
		err = p.Runner.DeMim(ctx, it.ag, it.conversa, it.deMim.msg)
	}
	if err != nil {
		// err não carrega texto de mensagem (specs/05 §8).
		log.Error("item falhou", "erro", err.Error())
	}
}

func panicTipo(r any) string {
	if e, ok := r.(error); ok {
		return e.Error()
	}
	return "valor"
}
