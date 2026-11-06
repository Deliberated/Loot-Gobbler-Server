package store

import (
	"context"
	"database/sql"
	"errors"
)

// Setting returns a server-wide setting's JSON value, or nil if it was never set.
func Setting(ctx context.Context, q Querier, key string) ([]byte, error) {
	var v string
	err := q.QueryRowContext(ctx, "SELECT value FROM settings WHERE key = ?", key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return []byte(v), nil
}

// PutSetting stores a server-wide setting's JSON value.
func PutSetting(ctx context.Context, q Querier, key string, value []byte, by, now int64) error {
	_, err := q.ExecContext(ctx, `INSERT INTO settings (key, value, updated_by, updated_at) VALUES (?, ?, ?, ?)
		ON CONFLICT (key) DO UPDATE SET value = excluded.value, updated_by = excluded.updated_by, updated_at = excluded.updated_at`,
		key, string(value), by, now)
	return err
}
