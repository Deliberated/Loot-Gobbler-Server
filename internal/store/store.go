// Package store keeps all server data in one SQLite file.
package store

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"sort"

	_ "modernc.org/sqlite" // pure Go SQLite driver, so the binary needs no C library
)

//go:embed migrations/*.sql
var migrationFS embed.FS

// Querier is satisfied by both the database and a transaction, so every query function works in either.
type Querier interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

type Store struct {
	db *sql.DB
}

// Open opens (or creates) the database at path and applies pending migrations.
func Open(path string) (*Store, error) {
	// WAL lets reads run while a write is in progress; immediate transactions take the write lock up front, so
	// two writers wait for each other (busy_timeout) instead of failing. The admin-token command opens the same
	// file from a second process, which this also covers.
	dsn := path + "?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(10000)" +
		"&_pragma=synchronous(NORMAL)&_txlock=immediate"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(4)
	s := &Store{db: db}
	if err := s.migrate(context.Background()); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate database: %w", err)
	}
	if err := backfillDrops(context.Background(), s); err != nil {
		db.Close()
		return nil, fmt.Errorf("fill the drop log: %w", err)
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

// DB is for reads outside a transaction.
func (s *Store) DB() Querier { return s.db }

// Tx runs fn in a write transaction, committing if fn returns nil and rolling back otherwise.
func (s *Store) Tx(ctx context.Context, fn func(q Querier) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}

// migrate runs migrations/NNN_*.sql in order. PRAGMA user_version records how many have been applied.
func (s *Store) migrate(ctx context.Context) error {
	names, err := fs.Glob(migrationFS, "migrations/*.sql")
	if err != nil {
		return err
	}
	sort.Strings(names)
	return s.Tx(ctx, func(q Querier) error {
		var applied int
		if err := q.QueryRowContext(ctx, "PRAGMA user_version").Scan(&applied); err != nil {
			return err
		}
		if applied > len(names) {
			return fmt.Errorf("database is from a newer server version (schema %d, this server knows %d)", applied, len(names))
		}
		for i := applied; i < len(names); i++ {
			script, err := migrationFS.ReadFile(names[i])
			if err != nil {
				return err
			}
			if _, err := q.ExecContext(ctx, string(script)); err != nil {
				return fmt.Errorf("%s: %w", names[i], err)
			}
			if _, err := q.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", i+1)); err != nil {
				return err
			}
		}
		return nil
	})
}

func nullInt(v sql.NullInt64) *int64 {
	if !v.Valid {
		return nil
	}
	return &v.Int64
}
