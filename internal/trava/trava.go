// Package trava garante um turno por conversa por vez (specs/05 §4). Na F1
// basta a trava em memória; com réplicas, a troca é por advisory lock do Postgres.
package trava

import "context"

// Trava dá exclusão mútua por chave.
type Trava interface {
	// Adquirir bloqueia até obter a chave ou ctx terminar (erro = ctx.Err()).
	// liberar é idempotente.
	Adquirir(ctx context.Context, chave string) (liberar func(), err error)
}
