package sqlite

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"sola-agens/internal/mensagem"
	"sola-agens/internal/store"
	"sola-agens/internal/store/storetest"
)

var relogio = func() time.Time { return time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC) }

func abrir(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "dados", "agens.db"), relogio)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestContrato(t *testing.T) {
	storetest.Rodar(t, func(t *testing.T) store.Store { return abrir(t) })
}

func TestMigrationsEPragmas(t *testing.T) {
	caminho := filepath.Join(t.TempDir(), "agens.db")
	s, err := Open(caminho, relogio)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	var v int
	if err := s.db.QueryRowContext(ctx, `SELECT MAX(versao) FROM schema_version`).Scan(&v); err != nil || v != 2 {
		t.Fatalf("versão = %d err=%v", v, err)
	}
	var modo string
	_ = s.db.QueryRowContext(ctx, `PRAGMA journal_mode`).Scan(&modo)
	if modo != "wal" {
		t.Errorf("journal_mode = %s", modo)
	}
	var fk, busy, sync int
	_ = s.db.QueryRowContext(ctx, `PRAGMA foreign_keys`).Scan(&fk)
	_ = s.db.QueryRowContext(ctx, `PRAGMA busy_timeout`).Scan(&busy)
	_ = s.db.QueryRowContext(ctx, `PRAGMA synchronous`).Scan(&sync)
	if fk != 1 || busy != 5000 || sync != 1 {
		t.Errorf("pragmas: foreign_keys=%d busy_timeout=%d synchronous=%d", fk, busy, sync)
	}
	_ = s.Close()

	// reabrir não reaplica nem falha
	s, err = Open(caminho, relogio)
	if err != nil {
		t.Fatalf("reabrir: %v", err)
	}
	var n int
	_ = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_version`).Scan(&n)
	if n != 2 {
		t.Errorf("schema_version tem %d linhas", n)
	}
	_ = s.Close()
}

func TestApagarContatoTrocaPorHashNosTurnos(t *testing.T) {
	s := abrir(t)
	defer s.Close()
	ctx := context.Background()
	id := store.ConversaID{Agente: "eliza", Tenant: "org-1", Contato: "5511999990001"}
	outro := store.ConversaID{Agente: "eliza", Tenant: "org-1", Contato: "5511999990002"}
	for _, c := range []store.ConversaID{id, outro} {
		if err := s.RegistrarTurno(ctx, store.Turno{ConversaID: c, Modo: store.ModoFixa, Resultado: "ok"}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.ApagarContato(ctx, id); err != nil {
		t.Fatal(err)
	}
	if err := s.ApagarContato(ctx, id); err != nil {
		t.Fatal(err)
	}
	var n int
	_ = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM turnos WHERE contato = ?`, id.Contato).Scan(&n)
	if n != 0 {
		t.Errorf("turnos com o contato em claro: %d", n)
	}
	_ = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM turnos WHERE contato = ?`, mensagem.HashCompleto(id.Contato)).Scan(&n)
	if n != 1 {
		t.Errorf("turnos com hash: %d", n)
	}
	_ = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM turnos WHERE contato = ?`, outro.Contato).Scan(&n)
	if n != 1 {
		t.Errorf("turno do outro contato: %d", n)
	}
}

// Base criada só com a 0001 e com mensagens: a 0002 reconstrói a tabela sem
// perder linha nem índice e passa a aceitar o papel 'aviso' (A11).
func TestMigration0002PreservaMensagens(t *testing.T) {
	caminho := filepath.Join(t.TempDir(), "agens.db")
	cru, err := sql.Open("sqlite", caminho)
	if err != nil {
		t.Fatal(err)
	}
	init, err := migrationsFS.ReadFile("migrations/0001_init.sql")
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		string(init),
		`INSERT INTO schema_version (versao) VALUES (1)`,
		`INSERT INTO mensagens VALUES ('m1','eliza','org-1','5511999990001','cliente','oi',NULL,1)`,
		`INSERT INTO mensagens VALUES ('m2','eliza','org-1','5511999990001','humano',NULL,'[]',2)`,
	} {
		if _, err := cru.Exec(q); err != nil {
			t.Fatalf("%v: %s", err, q)
		}
	}
	if _, err := cru.Exec(`INSERT INTO mensagens VALUES ('m3','eliza','org-1','x','aviso','x',NULL,3)`); err == nil {
		t.Fatal("a 0001 não deveria aceitar o papel aviso")
	}
	_ = cru.Close()

	s, err := Open(caminho, relogio)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	var n, idx int
	_ = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM mensagens`).Scan(&n)
	_ = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND tbl_name='mensagens' AND name LIKE 'ix_mensagens_%'`).Scan(&idx)
	if n != 2 || idx != 2 {
		t.Fatalf("depois da 0002: %d mensagens, %d índices", n, idx)
	}
	var conteudo sql.NullString
	_ = s.db.QueryRowContext(ctx, `SELECT conteudo FROM mensagens WHERE id='m1'`).Scan(&conteudo)
	if conteudo.String != "oi" {
		t.Errorf("m1.conteudo = %q", conteudo.String)
	}
	id := store.ConversaID{Agente: "eliza", Tenant: "org-1", Contato: "5511999990001"}
	if err := s.AnexarMensagem(ctx, id, store.MensagemMemoria{Papel: store.PapelAviso, Conteudo: ptrStr("lembrete")}); err != nil {
		t.Fatalf("aviso depois da 0002: %v", err)
	}
}

func ptrStr(s string) *string { return &s }
