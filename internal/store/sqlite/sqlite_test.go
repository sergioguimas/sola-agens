package sqlite

import (
	"context"
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
	if err := s.db.QueryRowContext(ctx, `SELECT MAX(versao) FROM schema_version`).Scan(&v); err != nil || v != 1 {
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
	if n != 1 {
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
