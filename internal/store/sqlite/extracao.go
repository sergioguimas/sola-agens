package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"sola-agens/internal/store"
)

// SessoesInativas implementa store.StoreTreino.
func (s *Store) SessoesInativas(ctx context.Context, agente string, antesDe time.Time) ([]store.SessaoTreino, error) {
	return s.listarSessoes(ctx,
		`SELECT `+colunasSessao+` FROM sessoes_treino WHERE agente = ? AND estado = ? AND ultima_msg_em < ? ORDER BY ultima_msg_em`,
		agente, store.SessaoAberta, ms(antesDe))
}

// SessoesPendentes implementa store.StoreTreino.
func (s *Store) SessoesPendentes(ctx context.Context, agente string, maxTentativas int) ([]store.SessaoTreino, error) {
	return s.listarSessoes(ctx,
		`SELECT `+colunasSessao+` FROM sessoes_treino
		 WHERE agente = ? AND estado IN (?, ?) AND tentativas_extracao < ? ORDER BY encerrada_em`,
		agente, store.SessaoEncerrada, store.SessaoErroExtracao, maxTentativas)
}

func (s *Store) listarSessoes(ctx context.Context, q string, args ...any) ([]store.SessaoTreino, error) {
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []store.SessaoTreino
	for rows.Next() {
		x, err := lerSessao(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

// PerfilAtivo implementa store.StoreTreino.
func (s *Store) PerfilAtivo(ctx context.Context, agente, tenant string) ([]byte, int, error) {
	return perfilAtivo(ctx, s.db, agente, tenant)
}

func perfilAtivo(ctx context.Context, q querier, agente, tenant string) ([]byte, int, error) {
	var perfil string
	var versao int
	err := q.QueryRowContext(ctx,
		`SELECT perfil, versao FROM perfis WHERE agente = ? AND tenant = ? AND ativa = 1`, agente, tenant).Scan(&perfil, &versao)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, 0, nil
	}
	if err != nil {
		return nil, 0, err
	}
	return []byte(perfil), versao, nil
}

// MarcarErroExtracao implementa store.StoreTreino.
func (s *Store) MarcarErroExtracao(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE sessoes_treino SET estado = ?, tentativas_extracao = tentativas_extracao + 1
		 WHERE id = ? AND estado IN (?, ?)`,
		store.SessaoErroExtracao, id, store.SessaoEncerrada, store.SessaoErroExtracao)
	return err
}

// AplicarExtracao implementa store.StoreTreino.
func (s *Store) AplicarExtracao(ctx context.Context, e store.Extracao) (store.ResultadoExtracao, error) {
	var res store.ResultadoExtracao
	err := s.tx(ctx, func(tx *sql.Tx) error {
		var estado string
		err := tx.QueryRowContext(ctx,
			`SELECT estado FROM sessoes_treino WHERE id = ? AND agente = ? AND tenant = ?`,
			e.SessaoID, e.Agente, e.Tenant).Scan(&estado)
		if errors.Is(err, sql.ErrNoRows) {
			return store.ErrNaoEncontrado
		}
		if err != nil {
			return err
		}
		switch estado {
		case store.SessaoExtraida:
			return store.ErrJaExtraida
		case store.SessaoAberta:
			return fmt.Errorf("extração de sessão aberta: %s", e.SessaoID)
		}

		agora := s.agora()
		for _, o := range e.Observacoes {
			if err := fundir(ctx, tx, e, o, agora); err != nil {
				return err
			}
		}
		for _, n := range e.Notas {
			var citacao sql.NullString
			if n.Citacao != "" {
				citacao = sql.NullString{String: n.Citacao, Valid: true}
			}
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO notas (id, agente, tenant, sessao_id, texto, citacao, estado, criada_em)
				 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
				n.ID, e.Agente, e.Tenant, e.SessaoID, n.Texto, citacao, store.NotaPendente, agora); err != nil {
				return err
			}
		}

		obs, err := observacoes(ctx, tx, e.Agente, e.Tenant)
		if err != nil {
			return err
		}
		res.Observacoes = obs
		ativo, _, err := perfilAtivo(ctx, tx, e.Agente, e.Tenant)
		if err != nil {
			return err
		}
		d, err := e.Derivar(obs, ativo)
		if err != nil {
			return err
		}
		if d.Mudou {
			v, err := gravarVersao(ctx, tx, e.Agente, e.Tenant, e.OperadorID, "sessao:"+e.SessaoID, d, agora)
			if err != nil {
				return err
			}
			res.Versao = &v
		}
		var versao sql.NullInt64
		if res.Versao != nil {
			versao = sql.NullInt64{Int64: int64(*res.Versao), Valid: true}
		}
		_, err = tx.ExecContext(ctx,
			`UPDATE sessoes_treino SET estado = ?, versao_gerada = ?, tentativas_extracao = tentativas_extracao + 1 WHERE id = ?`,
			store.SessaoExtraida, versao, e.SessaoID)
		return err
	})
	return res, err
}

// fundir soma a observação à que já existe ou cria uma nova (specs/06 §7.3).
// Rejeitada continua rejeitada: nova evidência não a traz de volta.
func fundir(ctx context.Context, tx *sql.Tx, e store.Extracao, o store.ObservacaoNova, agora int64) error {
	var id, origem, ultima string
	err := tx.QueryRowContext(ctx,
		`SELECT id, origem, ultima_sessao FROM observacoes WHERE agente = ? AND tenant = ? AND campo = ? AND valor = ?`,
		e.Agente, e.Tenant, o.Campo, o.Valor).Scan(&id, &origem, &ultima)
	if errors.Is(err, sql.ErrNoRows) {
		uid, err := uuid.NewV7()
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx,
			`INSERT INTO observacoes (id, agente, tenant, campo, valor, valor_exibicao, cenario, origem, estado,
			   ocorrencias, sessoes, ultima_sessao, citacao, criada_em, atualizada_em)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1, ?, ?, ?, ?)`,
			uid.String(), e.Agente, e.Tenant, o.Campo, o.Valor, o.ValorExibicao, nulo(o.Cenario), o.Origem,
			store.ObservacaoCandidata, o.Ocorrencias, e.SessaoID, nulo(o.Citacao), agora, agora)
		return err
	}
	if err != nil {
		return err
	}
	sessoes := 0
	if ultima != e.SessaoID {
		sessoes = 1
	}
	// Quem declarou com todas as letras vale mais do que quem foi só observado.
	if o.Origem == store.OrigemDeclarada {
		origem = store.OrigemDeclarada
	}
	_, err = tx.ExecContext(ctx,
		`UPDATE observacoes SET ocorrencias = ocorrencias + ?, sessoes = sessoes + ?, ultima_sessao = ?, origem = ?,
		   citacao = COALESCE(?, citacao), atualizada_em = ? WHERE id = ?`,
		o.Ocorrencias, sessoes, e.SessaoID, origem, nulo(o.Citacao), agora, id)
	return err
}

func nulo(s string) sql.NullString { return sql.NullString{String: s, Valid: s != ""} }

// Observacoes implementa store.StoreTreino.
func (s *Store) Observacoes(ctx context.Context, agente, tenant string) ([]store.Observacao, error) {
	return observacoes(ctx, s.db, agente, tenant)
}

func observacoes(ctx context.Context, q querier, agente, tenant string) ([]store.Observacao, error) {
	rows, err := q.QueryContext(ctx,
		`SELECT id, campo, valor, valor_exibicao, cenario, origem, estado, ocorrencias, sessoes, ultima_sessao, citacao
		 FROM observacoes WHERE agente = ? AND tenant = ? ORDER BY id`, agente, tenant)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []store.Observacao
	for rows.Next() {
		var o store.Observacao
		var cenario, citacao sql.NullString
		if err := rows.Scan(&o.ID, &o.Campo, &o.Valor, &o.ValorExibicao, &cenario, &o.Origem, &o.Estado,
			&o.Ocorrencias, &o.Sessoes, &o.UltimaSessao, &citacao); err != nil {
			return nil, err
		}
		o.Cenario, o.Citacao = cenario.String, citacao.String
		out = append(out, o)
	}
	return out, rows.Err()
}

// gravarVersao cria a versão nova já ativa. "Uma ativa por tenant" vale pela
// transação, não por índice parcial (specs/06 §10).
func gravarVersao(ctx context.Context, tx *sql.Tx, agente, tenant, operador, origem string, d store.Derivacao, agora int64) (int, error) {
	var ultima int
	if err := tx.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(versao), 0) FROM perfis WHERE agente = ? AND tenant = ?`, agente, tenant).Scan(&ultima); err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE perfis SET ativa = 0 WHERE agente = ? AND tenant = ? AND ativa = 1`, agente, tenant); err != nil {
		return 0, err
	}
	v := ultima + 1
	_, err := tx.ExecContext(ctx,
		`INSERT INTO perfis (agente, tenant, versao, ativa, perfil, diff, origem, criada_por, criada_em)
		 VALUES (?, ?, ?, 1, ?, ?, ?, ?, ?)`,
		agente, tenant, v, string(d.PerfilJSON), string(d.DiffJSON), origem, operador, agora)
	return v, err
}
