// Package memoria implementa trava.Trava com um mutex por chave, em processo.
package memoria

import (
	"context"
	"sync"

	"sola-agens/internal/trava"
)

var _ trava.Trava = (*Trava)(nil)

type entrada struct {
	ch   chan struct{} // capacidade 1: cheio = travado
	refs int           // quem segura ou espera
}

// Trava é segura para uso concorrente; o zero value serve.
type Trava struct {
	mu     sync.Mutex
	chaves map[string]*entrada
}

// Adquirir espera a vez da chave. Chaves diferentes não se bloqueiam; a entrada
// da chave some do mapa quando ninguém mais segura nem espera.
func (t *Trava) Adquirir(ctx context.Context, chave string) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	t.mu.Lock()
	if t.chaves == nil {
		t.chaves = map[string]*entrada{}
	}
	e := t.chaves[chave]
	if e == nil {
		e = &entrada{ch: make(chan struct{}, 1)}
		t.chaves[chave] = e
	}
	e.refs++
	t.mu.Unlock()

	select {
	case e.ch <- struct{}{}:
	case <-ctx.Done():
		t.soltar(chave, e)
		return nil, ctx.Err()
	}

	var uma sync.Once
	return func() {
		uma.Do(func() {
			<-e.ch
			t.soltar(chave, e)
		})
	}, nil
}

func (t *Trava) soltar(chave string, e *entrada) {
	t.mu.Lock()
	defer t.mu.Unlock()
	e.refs--
	if e.refs == 0 {
		delete(t.chaves, chave)
	}
}
