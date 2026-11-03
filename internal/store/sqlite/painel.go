package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"sola-agens/internal/store"
)

// Versoes implementa store.StoreTreino.
func (s *Store) Versoes(ctx context.Context, agente, tenant string) ([]store.VersaoPerfil, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT versao, ativa, origem, diff, criada_em FROM perfis WHERE agente = ? AND tenant = ? ORDER BY versao DESC`,
		agente, tenant)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []store.VersaoPerfil
	for rows.Next() {
		var v store.VersaoPerfil
		var ativa int
		var diff string
		var criada int64
		if err := rows.Scan(&v.Versao, &ativa, &v.Origem, &diff, &criada); err != nil {
			return nil, err
		}
		v.Ativa, v.DiffJSON, v.CriadaEm = ativa == 1, []byte(diff), deMs(criada)
		out = append(out, v)
	}
	return out, rows.Err()
}

// Versao implementa store.StoreTreino.
func (s *Store) Versao(ctx context.Context, agente, tenant string, n int) ([]byte, error) {
	var perfil string
	err := s.db.QueryRowContext(ctx,
		`SELECT perfil FROM perfis WHERE agente = ? AND tenant = ? AND versao = ?`, agente, tenant, n).Scan(&perfil)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, store.ErrNaoEncontrado
	}
	if err != nil {
		return nil, err
	}
	return []byte(perfil), nil
}

// EditarPerfil implementa store.StoreTreino.
func (s *Store) EditarPerfil(ctx context.Context, e store.Edicao) (store.ResultadoExtracao, error) {
	var res store.ResultadoExtracao
	err := s.tx(ctx, func(tx *sql.Tx) error {
		agora := s.agora()
		mudar := func(ids []string, estado string) error {
			for _, id := range ids {
				r, err := tx.ExecContext(ctx,
					`UPDATE observacoes SET estado = ?, atualizada_em = ? WHERE id = ? AND agente = ? AND tenant = ?`,
					estado, agora, id, e.Agente, e.Tenant)
				if err != nil {
					return err
				}
				if n, err := r.RowsAffected(); err != nil {
					return err
				} else if n == 0 {
					return store.ErrNaoEncontrado
				}
			}
			return nil
		}
		if err := mudar(e.Rejeitar, store.ObservacaoRejeitada); err != nil {
			return err
		}
		if err := mudar(e.Restaurar, store.ObservacaoCandidata); err != nil {
			return err
		}
		if e.RejeitarTodas {
			if _, err := tx.ExecContext(ctx,
				`UPDATE observacoes SET estado = ?, atualizada_em = ? WHERE agente = ? AND tenant = ?`,
				store.ObservacaoRejeitada, agora, e.Agente, e.Tenant); err != nil {
				return err
			}
		}
		if e.DescartarNotas {
			if _, err := tx.ExecContext(ctx,
				`UPDATE notas SET estado = ?, resolvida_em = ? WHERE agente = ? AND tenant = ? AND estado = ?`,
				store.NotaDescartada, ms(e.Em), e.Agente, e.Tenant, store.NotaPendente); err != nil {
				return err
			}
		}

		obs, err := observacoes(ctx, tx, e.Agente, e.Tenant)
		if err != nil {
			return err
		}
		ativo, _, err := perfilAtivo(ctx, tx, e.Agente, e.Tenant)
		if err != nil {
			return err
		}
		d, err := e.Derivar(obs, ativo)
		if err != nil {
			return err
		}
		if err := mudar(d.Rejeitar, store.ObservacaoRejeitada); err != nil {
			return err
		}
		if d.Mudou {
			v, err := gravarVersao(ctx, tx, e.Agente, e.Tenant, e.OperadorID, e.Origem, d, agora)
			if err != nil {
				return err
			}
			res.Versao = &v
		}
		res.Observacoes, err = observacoes(ctx, tx, e.Agente, e.Tenant)
		return err
	})
	return res, err
}

// Notas implementa store.StoreTreino. Pendentes vêm primeiro: é o que pede decisão.
func (s *Store) Notas(ctx context.Context, agente, tenant, estado string) ([]store.Nota, error) {
	q := `SELECT id, texto, citacao, estado, criada_em FROM notas WHERE agente = ? AND tenant = ?`
	args := []any{agente, tenant}
	if estado != "" {
		q += ` AND estado = ?`
		args = append(args, estado)
	}
	q += ` ORDER BY CASE estado WHEN 'pendente' THEN 0 ELSE 1 END, criada_em, id`
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []store.Nota
	for rows.Next() {
		var n store.Nota
		var citacao sql.NullString
		var criada int64
		if err := rows.Scan(&n.ID, &n.Texto, &citacao, &n.Estado, &criada); err != nil {
			return nil, err
		}
		n.Citacao, n.CriadaEm = citacao.String, deMs(criada)
		out = append(out, n)
	}
	return out, rows.Err()
}

// MarcarNota implementa store.StoreTreino.
func (s *Store) MarcarNota(ctx context.Context, agente, tenant, id, estado string, em time.Time) (string, error) {
	var atual string
	err := s.tx(ctx, func(tx *sql.Tx) error {
		err := tx.QueryRowContext(ctx,
			`SELECT estado FROM notas WHERE id = ? AND agente = ? AND tenant = ?`, id, agente, tenant).Scan(&atual)
		if errors.Is(err, sql.ErrNoRows) {
			return store.ErrNaoEncontrado
		}
		if err != nil || atual != store.NotaPendente {
			return err
		}
		atual = estado
		_, err = tx.ExecContext(ctx, `UPDATE notas SET estado = ?, resolvida_em = ? WHERE id = ?`, estado, ms(em), id)
		return err
	})
	return atual, err
}
