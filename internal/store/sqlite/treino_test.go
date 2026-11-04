package sqlite

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"sola-agens/internal/store"
	"sola-agens/internal/store/storetest"
)

func TestContratoTreino(t *testing.T) {
	storetest.RodarTreino(t, func(t *testing.T) storetest.StoreTreinoFechavel { return abrir(t) })
}

// Base na 0002 com turnos: a 0003 reconstrói a tabela sem perder linha nem
// índice e passa a aceitar modo='treino' (specs/06 §10).
func TestMigration0003PreservaTurnos(t *testing.T) {
	caminho := filepath.Join(t.TempDir(), "agens.db")
	cru, err := sql.Open("sqlite", caminho)
	if err != nil {
		t.Fatal(err)
	}
	var q []string
	for _, nome := range []string{"0001_init.sql", "0002_papel_aviso.sql"} {
		corpo, err := migrationsFS.ReadFile("migrations/" + nome)
		if err != nil {
			t.Fatal(err)
		}
		q = append(q, string(corpo))
	}
	q = append(q, `INSERT INTO schema_version (versao) VALUES (1), (2)`,
		`INSERT INTO turnos (id, agente, tenant, contato, modo, mensagem_ids, ferramentas, tokens_entrada, resultado, escalou, criado_em)
		 VALUES ('t1','eliza','org-1','5511','llm','["m1"]','[]',42,'ok',1,7)`)
	for _, c := range q {
		if _, err := cru.Exec(c); err != nil {
			t.Fatalf("%v: %s", err, c)
		}
	}
	_, err = cru.Exec(`INSERT INTO turnos (id, agente, tenant, contato, modo, mensagem_ids, ferramentas, resultado, criado_em)
		VALUES ('t2','eliza','org-1','5511','treino','[]','[]','ok',8)`)
	if err == nil {
		t.Fatal("a 0002 não deveria aceitar o modo treino")
	}
	_ = cru.Close()

	s, err := Open(caminho, relogio)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	var tokens, escalou, idx int
	var modo string
	if err := s.db.QueryRowContext(ctx, `SELECT modo, tokens_entrada, escalou FROM turnos WHERE id='t1'`).
		Scan(&modo, &tokens, &escalou); err != nil {
		t.Fatal(err)
	}
	if modo != "llm" || tokens != 42 || escalou != 1 {
		t.Errorf("t1 depois da 0003: modo=%s tokens=%d escalou=%d", modo, tokens, escalou)
	}
	_ = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name='ix_turnos_agente_tenant'`).Scan(&idx)
	if idx != 1 {
		t.Error("índice de turnos não foi recriado")
	}
	id := store.ConversaID{Agente: "eliza", Tenant: "org-1", Contato: "u1"}
	for _, m := range []string{store.ModoTreino, store.ModoPrevia} {
		if err := s.RegistrarTurno(ctx, store.Turno{ConversaID: id, Modo: m, Resultado: "ok"}); err != nil {
			t.Errorf("modo %s depois da 0003: %v", m, err)
		}
	}
}

// A retenção zera o texto do treino como o das conversas (specs/06 §10).
func TestExpurgoAlcancaMensagensDeTreino(t *testing.T) {
	s := abrir(t)
	defer s.Close()
	ctx := context.Background()
	sess, _, err := s.AbrirSessao(ctx, store.SessaoNova{Agente: "eliza", Tenant: "org-1", OperadorID: "u1",
		RoteiroJSON: []byte(`{}`), LimiteDia: 10})
	if err != nil {
		t.Fatal(err)
	}
	velha := relogio().AddDate(0, 0, -100)
	err = s.GravarTurnoTreino(ctx, sess.ID, []store.MensagemTreino{
		{Papel: store.PapelOperador, Tipo: store.TipoLivre, Conteudo: ptrStr("velha"), CriadaEm: velha},
		{Papel: store.PapelAgente, Tipo: store.TipoLivre, Conteudo: ptrStr("nova"), CriadaEm: relogio()},
	}, []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	n, err := s.ExpurgarTexto(ctx, relogio().Add(-90*24*time.Hour))
	if err != nil || n != 1 {
		t.Fatalf("expurgadas=%d err=%v", n, err)
	}
	msgs, err := s.MensagensTreino(ctx, sess.ID)
	if err != nil || len(msgs) != 2 {
		t.Fatalf("msgs=%d err=%v", len(msgs), err)
	}
	if msgs[0].Conteudo != nil || msgs[1].Conteudo == nil {
		t.Errorf("conteúdos depois do expurgo: %v, %v", msgs[0].Conteudo, msgs[1].Conteudo)
	}
}

// Rejeitada continua rejeitada com evidência nova (specs/06 §7.3). A rota de
// rejeitar é da fase do painel; aqui o estado é posto direto na base.
func TestFusaoNaoRessuscitaRejeitada(t *testing.T) {
	s := abrir(t)
	defer s.Close()
	ctx := context.Background()
	aplicar := func() store.ResultadoExtracao {
		t.Helper()
		x, _, err := s.AbrirSessao(ctx, store.SessaoNova{Agente: "eliza", Tenant: "org-1", OperadorID: "u1",
			RoteiroJSON: []byte(`{}`), LimiteDia: 10})
		if err != nil {
			t.Fatal(err)
		}
		if err := s.EncerrarSessao(ctx, x.ID, relogio()); err != nil {
			t.Fatal(err)
		}
		res, err := s.AplicarExtracao(ctx, store.Extracao{Agente: "eliza", Tenant: "org-1", SessaoID: x.ID, OperadorID: "u1",
			Observacoes: []store.ObservacaoNova{{Campo: "tom", Valor: "seca", ValorExibicao: "seca",
				Origem: store.OrigemExtraida, Ocorrencias: 1}},
			Derivar: func([]store.Observacao, []byte) (store.Derivacao, error) { return store.Derivacao{}, nil }})
		if err != nil {
			t.Fatal(err)
		}
		return res
	}
	aplicar()
	if _, err := s.db.ExecContext(ctx, `UPDATE observacoes SET estado = 'rejeitada'`); err != nil {
		t.Fatal(err)
	}
	res := aplicar()
	if o := res.Observacoes[0]; o.Estado != store.ObservacaoRejeitada || o.Sessoes != 2 {
		t.Fatalf("observação: %+v", o)
	}
}
