// Package sqlite implementa store.Store sobre SQLite puro Go (modernc.org/sqlite,
// sem CGO). Uma conexão só, migrations embutidas, ids uuid v7 e instantes em ms
// UTC gerados em Go (specs/01 §10, specs/05 §3).
package sqlite

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite" // registra o driver "sqlite"

	"sola-agens/internal/store"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// Store implementa store.Store.
type Store struct {
	db    *sql.DB
	clock func() time.Time
}

var _ store.Store = (*Store)(nil)

// querier é o que *sql.DB e *sql.Tx têm em comum.
type querier interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// Open abre (criando arquivo e diretório) o banco em caminho e aplica as
// migrations pendentes. clock nil = time.Now.
func Open(caminho string, clock func() time.Time) (*Store, error) {
	if clock == nil {
		clock = time.Now
	}
	if dir := filepath.Dir(caminho); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, fmt.Errorf("criar diretório do banco (%s): %w", dir, err)
		}
	}
	// PRAGMAs ficam aqui, não na migration (specs/01 §10).
	dsn := "file:" + filepath.ToSlash(caminho) + "?" +
		"_pragma=journal_mode(WAL)" +
		"&_pragma=foreign_keys(ON)" +
		"&_pragma=busy_timeout(5000)" +
		"&_pragma=synchronous(NORMAL)" +
		"&_txlock=" + url.QueryEscape("immediate")
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("abrir banco: %w", err)
	}
	// DECISÃO: conexão única (um processo só); evita corrida entre BeginTx e consultas.
	db.SetMaxOpenConns(1)
	if err := migrar(context.Background(), db); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migrar banco: %w", err)
	}
	return &Store{db: db, clock: clock}, nil
}

// Close fecha o banco.
func (s *Store) Close() error { return s.db.Close() }

func ms(t time.Time) int64 { return t.UnixMilli() }

func deMs(v int64) time.Time { return time.UnixMilli(v).UTC() }

func (s *Store) agora() int64 { return ms(s.clock()) }

// tx roda fn numa transação (BEGIN IMMEDIATE via _txlock).
func (s *Store) tx(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

// migrar aplica migrations/NNNN_*.sql ainda não registradas em schema_version.
// A 0001 cria a própria tabela schema_version; cada migration registra sua versão.
func migrar(ctx context.Context, db *sql.DB) error {
	atual := 0
	var existe int
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'schema_version'`).Scan(&existe); err != nil {
		return err
	}
	if existe > 0 {
		if err := db.QueryRowContext(ctx, `SELECT COALESCE(MAX(versao), 0) FROM schema_version`).Scan(&atual); err != nil {
			return err
		}
	}

	entradas, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		return err
	}
	nomes := make([]string, 0, len(entradas))
	for _, e := range entradas {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") {
			nomes = append(nomes, e.Name())
		}
	}
	sort.Strings(nomes)

	for _, nome := range nomes {
		prefixo, _, ok := strings.Cut(nome, "_")
		versao, err := strconv.Atoi(prefixo)
		if !ok || err != nil {
			return fmt.Errorf("nome de migration inválido: %q (esperado NNNN_descricao.sql)", nome)
		}
		if versao <= atual {
			continue
		}
		corpo, err := migrationsFS.ReadFile("migrations/" + nome)
		if err != nil {
			return err
		}
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, string(corpo)); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("aplicar %s: %w", nome, err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO schema_version (versao) VALUES (?)`, versao); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("registrar versão %d: %w", versao, err)
		}
		if err := tx.Commit(); err != nil {
			return err
		}
		atual = versao
	}
	return nil
}
