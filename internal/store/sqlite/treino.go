package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/google/uuid"

	"sola-agens/internal/store"
)

var _ store.StoreTreino = (*Store)(nil)

const colunasSessao = `id, agente, tenant, operador_id, estado, roteiro_json, aberta_em, ultima_msg_em, encerrada_em`

func lerSessao(row *sql.Row) (store.SessaoTreino, error) {
	var s store.SessaoTreino
	var roteiro string
	var aberta, ultima int64
	var encerrada sql.NullInt64
	err := row.Scan(&s.ID, &s.Agente, &s.Tenant, &s.OperadorID, &s.Estado, &roteiro, &aberta, &ultima, &encerrada)
	if errors.Is(err, sql.ErrNoRows) {
		return s, store.ErrNaoEncontrado
	}
	if err != nil {
		return s, err
	}
	s.RoteiroJSON = []byte(roteiro)
	s.AbertaEm, s.UltimaMsgEm = deMs(aberta), deMs(ultima)
	if encerrada.Valid {
		t := deMs(encerrada.Int64)
		s.EncerradaEm = &t
	}
	return s, nil
}

// AbrirSessao implementa store.StoreTreino. "Uma aberta por operador" vale pela
// transação, não por índice parcial (não é portável, specs/06 §10).
func (s *Store) AbrirSessao(ctx context.Context, n store.SessaoNova) (store.SessaoTreino, bool, error) {
	if n.Agente == "" || n.Tenant == "" || n.OperadorID == "" {
		return store.SessaoTreino{}, false, store.ErrConversaInvalida
	}
	var out store.SessaoTreino
	nova := false
	err := s.tx(ctx, func(tx *sql.Tx) error {
		atual, err := lerSessao(tx.QueryRowContext(ctx,
			`SELECT `+colunasSessao+` FROM sessoes_treino
			 WHERE agente = ? AND tenant = ? AND operador_id = ? AND estado = ?`,
			n.Agente, n.Tenant, n.OperadorID, store.SessaoAberta))
		if err == nil {
			out = atual
			return nil
		}
		if !errors.Is(err, store.ErrNaoEncontrado) {
			return err
		}
		var hoje int
		if err := tx.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM sessoes_treino WHERE agente = ? AND tenant = ? AND aberta_em >= ?`,
			n.Agente, n.Tenant, ms(n.InicioDia)).Scan(&hoje); err != nil {
			return err
		}
		if hoje >= n.LimiteDia {
			return store.ErrLimiteSessoes
		}
		uid, err := uuid.NewV7()
		if err != nil {
			return err
		}
		agora := s.agora()
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO sessoes_treino (id, agente, tenant, operador_id, estado, roteiro_json, aberta_em, ultima_msg_em)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			uid.String(), n.Agente, n.Tenant, n.OperadorID, store.SessaoAberta, string(n.RoteiroJSON), agora, agora); err != nil {
			return err
		}
		out = store.SessaoTreino{ID: uid.String(), Agente: n.Agente, Tenant: n.Tenant, OperadorID: n.OperadorID,
			Estado: store.SessaoAberta, RoteiroJSON: n.RoteiroJSON, AbertaEm: deMs(agora), UltimaMsgEm: deMs(agora)}
		nova = true
		return nil
	})
	return out, nova, err
}

// SessaoTreino implementa store.StoreTreino.
func (s *Store) SessaoTreino(ctx context.Context, agente, tenant, id string) (store.SessaoTreino, error) {
	return lerSessao(s.db.QueryRowContext(ctx,
		`SELECT `+colunasSessao+` FROM sessoes_treino WHERE id = ? AND agente = ? AND tenant = ?`, id, agente, tenant))
}

// MensagensTreino implementa store.StoreTreino. O id (uuid v7) desempata falas
// gravadas no mesmo milissegundo.
func (s *Store) MensagensTreino(ctx context.Context, sessaoID string) ([]store.MensagemTreino, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT papel, tipo, cenario, conteudo, criada_em FROM mensagens_treino
		 WHERE sessao_id = ? ORDER BY criada_em, id`, sessaoID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []store.MensagemTreino
	for rows.Next() {
		var m store.MensagemTreino
		var cenario, conteudo sql.NullString
		var criada int64
		if err := rows.Scan(&m.Papel, &m.Tipo, &cenario, &conteudo, &criada); err != nil {
			return nil, err
		}
		m.Cenario = cenario.String
		if conteudo.Valid {
			c := conteudo.String
			m.Conteudo = &c
		}
		m.CriadaEm = deMs(criada)
		out = append(out, m)
	}
	return out, rows.Err()
}

// GravarTurnoTreino implementa store.StoreTreino.
func (s *Store) GravarTurnoTreino(ctx context.Context, sessaoID string, msgs []store.MensagemTreino, roteiroJSON []byte) error {
	return s.tx(ctx, func(tx *sql.Tx) error {
		agora := s.agora()
		r, err := tx.ExecContext(ctx,
			`UPDATE sessoes_treino SET roteiro_json = ?, ultima_msg_em = ? WHERE id = ? AND estado = ?`,
			string(roteiroJSON), agora, sessaoID, store.SessaoAberta)
		if err != nil {
			return err
		}
		if n, err := r.RowsAffected(); err != nil {
			return err
		} else if n == 0 {
			return store.ErrSessaoEncerrada
		}
		for _, m := range msgs {
			uid, err := uuid.NewV7()
			if err != nil {
				return err
			}
			criada := agora
			if !m.CriadaEm.IsZero() {
				criada = ms(m.CriadaEm)
			}
			var cenario sql.NullString
			if m.Cenario != "" {
				cenario = sql.NullString{String: m.Cenario, Valid: true}
			}
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO mensagens_treino (id, sessao_id, papel, tipo, cenario, conteudo, criada_em)
				 VALUES (?, ?, ?, ?, ?, ?, ?)`,
				uid.String(), sessaoID, m.Papel, m.Tipo, cenario, m.Conteudo, criada); err != nil {
				return err
			}
		}
		return nil
	})
}

// EncerrarSessao implementa store.StoreTreino.
func (s *Store) EncerrarSessao(ctx context.Context, id string, em time.Time) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE sessoes_treino SET estado = ?, encerrada_em = ? WHERE id = ? AND estado = ?`,
		store.SessaoEncerrada, ms(em), id, store.SessaoAberta)
	return err
}
