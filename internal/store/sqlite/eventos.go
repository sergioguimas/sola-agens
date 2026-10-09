package sqlite

import (
	"context"
	"database/sql"
	"time"
)

// MarcarEvento registra a mensagem do canal; novo=false se já existia.
func (s *Store) MarcarEvento(ctx context.Context, agente, mensagemID string) (bool, error) {
	r, err := s.db.ExecContext(ctx,
		`INSERT INTO eventos (agente, mensagem_id, recebido_em) VALUES (?, ?, ?)
		 ON CONFLICT (agente, mensagem_id) DO NOTHING`, agente, mensagemID, s.agora())
	if err != nil {
		return false, err
	}
	n, err := r.RowsAffected()
	return n > 0, err
}

// DesmarcarEvento permite que o reenvio da mesma mensagem não vire duplicado.
func (s *Store) DesmarcarEvento(ctx context.Context, agente, mensagemID string) error {
	_, err := s.db.ExecContext(ctx,
		`DELETE FROM eventos WHERE agente = ? AND mensagem_id = ?`, agente, mensagemID)
	return err
}

// RegistrarEco guarda o id de uma mensagem enviada pelo próprio agens.
func (s *Store) RegistrarEco(ctx context.Context, agente, mensagemID string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO ecos (agente, mensagem_id, criado_em) VALUES (?, ?, ?)
		 ON CONFLICT (agente, mensagem_id) DO NOTHING`, agente, mensagemID, s.agora())
	return err
}

// EhEco diz se o id foi registrado como resposta do agens.
func (s *Store) EhEco(ctx context.Context, agente, mensagemID string) (bool, error) {
	var n int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM ecos WHERE agente = ? AND mensagem_id = ?`, agente, mensagemID).Scan(&n)
	return n > 0, err
}

// ApagarEventosEcos apaga eventos e ecos anteriores ao instante; devolve o total.
func (s *Store) ApagarEventosEcos(ctx context.Context, antesDe time.Time) (int64, error) {
	var total int64
	err := s.tx(ctx, func(tx *sql.Tx) error {
		r, err := tx.ExecContext(ctx, `DELETE FROM eventos WHERE recebido_em < ?`, ms(antesDe))
		if err != nil {
			return err
		}
		a, _ := r.RowsAffected()
		r, err = tx.ExecContext(ctx, `DELETE FROM ecos WHERE criado_em < ?`, ms(antesDe))
		if err != nil {
			return err
		}
		b, _ := r.RowsAffected()
		total = a + b
		return nil
	})
	return total, err
}

// JobFeito diz se (nome, dataRef) já foi marcado.
func (s *Store) JobFeito(ctx context.Context, nome, dataRef string) (bool, error) {
	var n int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM jobs WHERE nome = ? AND data_ref = ?`, nome, dataRef).Scan(&n)
	return n > 0, err
}

// MarcarJob marca (nome, dataRef) como feito; idempotente.
func (s *Store) MarcarJob(ctx context.Context, nome, dataRef string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO jobs (nome, data_ref, feito_em) VALUES (?, ?, ?)
		 ON CONFLICT (nome, data_ref) DO NOTHING`, nome, dataRef, s.agora())
	return err
}
