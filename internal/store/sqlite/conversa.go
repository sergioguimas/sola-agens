package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"github.com/google/uuid"

	"sola-agens/internal/mensagem"
	"sola-agens/internal/store"
)

// garantir cria a linha vazia da conversa se ela não existe.
func (s *Store) garantir(ctx context.Context, q querier, id store.ConversaID) error {
	agora := s.agora()
	_, err := q.ExecContext(ctx,
		`INSERT INTO conversas (agente, tenant, contato, criada_em, atualizada_em) VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT (agente, tenant, contato) DO NOTHING`,
		id.Agente, id.Tenant, id.Contato, agora, agora)
	return err
}

// Conversa devolve o estado da conversa, criando a linha vazia se preciso.
func (s *Store) Conversa(ctx context.Context, id store.ConversaID) (store.Conversa, error) {
	if err := id.Valida(); err != nil {
		return store.Conversa{}, err
	}
	if err := s.garantir(ctx, s.db, id); err != nil {
		return store.Conversa{}, err
	}
	var (
		pausada, configEm sql.NullInt64
		config            sql.NullString
		criada, atualiz   int64
	)
	err := s.db.QueryRowContext(ctx,
		`SELECT pausada_ate, config_json, config_em, criada_em, atualizada_em
		 FROM conversas WHERE agente = ? AND tenant = ? AND contato = ?`,
		id.Agente, id.Tenant, id.Contato).Scan(&pausada, &config, &configEm, &criada, &atualiz)
	if err != nil {
		return store.Conversa{}, err
	}
	c := store.Conversa{ID: id, CriadaEm: deMs(criada), AtualizadaEm: deMs(atualiz)}
	if pausada.Valid {
		t := deMs(pausada.Int64)
		c.PausadaAte = &t
	}
	if config.Valid {
		c.ConfigJSON = []byte(config.String)
	}
	if configEm.Valid {
		t := deMs(configEm.Int64)
		c.ConfigEm = &t
	}
	return c, nil
}

// Pausar fixa pausada_ate (cada deMim reinicia o prazo).
func (s *Store) Pausar(ctx context.Context, id store.ConversaID, ate time.Time) error {
	if err := id.Valida(); err != nil {
		return err
	}
	return s.tx(ctx, func(tx *sql.Tx) error {
		if err := s.garantir(ctx, tx, id); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx,
			`UPDATE conversas SET pausada_ate = ?, atualizada_em = ? WHERE agente = ? AND tenant = ? AND contato = ?`,
			ms(ate), s.agora(), id.Agente, id.Tenant, id.Contato)
		return err
	})
}

// GuardarConfig guarda a última configuração combinada recebida do sistema.
func (s *Store) GuardarConfig(ctx context.Context, id store.ConversaID, configJSON []byte) error {
	if err := id.Valida(); err != nil {
		return err
	}
	return s.tx(ctx, func(tx *sql.Tx) error {
		if err := s.garantir(ctx, tx, id); err != nil {
			return err
		}
		agora := s.agora()
		_, err := tx.ExecContext(ctx,
			`UPDATE conversas SET config_json = ?, config_em = ?, atualizada_em = ? WHERE agente = ? AND tenant = ? AND contato = ?`,
			string(configJSON), agora, agora, id.Agente, id.Tenant, id.Contato)
		return err
	})
}

// anexoJSON é a forma gravada do anexo: nunca inclui Ref.
type anexoJSON struct {
	Tipo         string `json:"tipo"`
	Mime         string `json:"mime,omitempty"`
	Tamanho      int64  `json:"tamanho,omitempty"`
	DuracaoS     int    `json:"duracaoS,omitempty"`
	Encaminhado  bool   `json:"encaminhado,omitempty"`
	TipoOriginal string `json:"tipoOriginal,omitempty"`
}

// AnexarMensagem grava uma mensagem na memória da conversa.
func (s *Store) AnexarMensagem(ctx context.Context, id store.ConversaID, m store.MensagemMemoria) error {
	if err := id.Valida(); err != nil {
		return err
	}
	switch m.Papel {
	case store.PapelCliente, store.PapelAgente, store.PapelHumano, store.PapelAviso:
	default:
		return store.ErrPapelInvalido
	}
	var anexos sql.NullString
	if len(m.Anexos) > 0 {
		lista := make([]anexoJSON, len(m.Anexos))
		for i, a := range m.Anexos {
			lista[i] = anexoJSON{a.Tipo, a.Mime, a.Tamanho, a.DuracaoS, a.Encaminhado, a.TipoOriginal}
		}
		b, err := json.Marshal(lista)
		if err != nil {
			return err
		}
		anexos = sql.NullString{String: string(b), Valid: true}
	}
	var conteudo sql.NullString
	if m.Conteudo != nil {
		conteudo = sql.NullString{String: *m.Conteudo, Valid: true}
	}
	criada := s.agora()
	if !m.CriadaEm.IsZero() {
		criada = ms(m.CriadaEm)
	}
	uid, err := uuid.NewV7()
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO mensagens (id, agente, tenant, contato, papel, conteudo, anexos, criada_em)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		uid.String(), id.Agente, id.Tenant, id.Contato, m.Papel, conteudo, anexos, criada)
	return err
}

// Janela devolve as últimas max mensagens com criada_em >= desde, em ordem crescente.
func (s *Store) Janela(ctx context.Context, id store.ConversaID, max int, desde time.Time) ([]store.MensagemMemoria, error) {
	if err := id.Valida(); err != nil {
		return nil, err
	}
	if max <= 0 {
		return nil, nil
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT papel, conteudo, anexos, criada_em FROM mensagens
		 WHERE agente = ? AND tenant = ? AND contato = ? AND criada_em >= ?
		 ORDER BY criada_em DESC, id DESC LIMIT ?`,
		id.Agente, id.Tenant, id.Contato, ms(desde), max)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []store.MensagemMemoria
	for rows.Next() {
		var (
			m      store.MensagemMemoria
			cont   sql.NullString
			anexos sql.NullString
			criada int64
		)
		if err := rows.Scan(&m.Papel, &cont, &anexos, &criada); err != nil {
			return nil, err
		}
		if cont.Valid {
			c := cont.String
			m.Conteudo = &c
		}
		if anexos.Valid {
			var lista []anexoJSON
			if err := json.Unmarshal([]byte(anexos.String), &lista); err != nil {
				return nil, err
			}
			for _, a := range lista {
				m.Anexos = append(m.Anexos, mensagem.Anexo{Tipo: a.Tipo, Mime: a.Mime, Tamanho: a.Tamanho,
					DuracaoS: a.DuracaoS, Encaminhado: a.Encaminhado, TipoOriginal: a.TipoOriginal})
			}
		}
		m.CriadaEm = deMs(criada)
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// veio do mais novo para o mais velho; inverte
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, nil
}

// ExpurgarTexto zera o conteúdo das mensagens anteriores ao instante (a linha e
// os anexos, que não têm Ref nem texto, ficam). Devolve quantas linhas mudaram.
func (s *Store) ExpurgarTexto(ctx context.Context, antesDe time.Time) (int64, error) {
	r, err := s.db.ExecContext(ctx,
		`UPDATE mensagens SET conteudo = NULL WHERE criada_em < ? AND conteudo IS NOT NULL`, ms(antesDe))
	if err != nil {
		return 0, err
	}
	return r.RowsAffected()
}

// ApagarContato apaga mensagens e conversa e troca o contato por sha256 completo
// nos turnos (specs/01 §9). Idempotente.
func (s *Store) ApagarContato(ctx context.Context, id store.ConversaID) error {
	if err := id.Valida(); err != nil {
		return err
	}
	hash := mensagem.HashCompleto(id.Contato)
	return s.tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM mensagens WHERE agente = ? AND tenant = ? AND contato = ?`,
			id.Agente, id.Tenant, id.Contato); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM conversas WHERE agente = ? AND tenant = ? AND contato = ?`,
			id.Agente, id.Tenant, id.Contato); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx,
			`UPDATE turnos SET contato = ? WHERE agente = ? AND tenant = ? AND contato = ?`,
			hash, id.Agente, id.Tenant, id.Contato)
		return err
	})
}

// RegistrarTurno grava a linha de auditoria do turno.
func (s *Store) RegistrarTurno(ctx context.Context, t store.Turno) error {
	if err := t.ConversaID.Valida(); err != nil {
		return err
	}
	switch t.Modo {
	case store.ModoLLM, store.ModoFastpath, store.ModoFixa, store.ModoPausado, store.ModoErro:
	default:
		return store.ErrModoInvalido
	}
	ids := t.MensagemIDs
	if ids == nil {
		ids = []string{}
	}
	ferr := t.Ferramentas
	if ferr == nil {
		ferr = []store.FerramentaLog{}
	}
	idsJSON, err := json.Marshal(ids)
	if err != nil {
		return err
	}
	ferrJSON, err := json.Marshal(ferr)
	if err != nil {
		return err
	}
	var modelo sql.NullString
	if t.Modelo != "" {
		modelo = sql.NullString{String: t.Modelo, Valid: true}
	}
	escalou := 0
	if t.Escalou {
		escalou = 1
	}
	criado := s.agora()
	if !t.CriadoEm.IsZero() {
		criado = ms(t.CriadoEm)
	}
	uid, err := uuid.NewV7()
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO turnos (id, agente, tenant, contato, modo, mensagem_ids, modelo, iteracoes, ferramentas,
		   tokens_entrada, tokens_saida, custo_micro_usd, latencia_ms, resultado, escalou, criado_em)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		uid.String(), t.Agente, t.Tenant, t.Contato, t.Modo, string(idsJSON), modelo, t.Iteracoes, string(ferrJSON),
		t.TokensEntrada, t.TokensSaida, t.CustoMicroUSD, t.LatenciaMS, t.Resultado, escalou, criado)
	return err
}
