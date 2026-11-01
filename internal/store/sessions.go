package store

import (
	"context"
	"database/sql"
	"errors"
)

type Session struct {
	UserID     int64
	LastUsedAt int64
	ExpiresAt  int64
}

func CreateSession(ctx context.Context, q Querier, tokenHash string, userID, now, expiresAt int64, client string) error {
	_, err := q.ExecContext(ctx,
		"INSERT INTO sessions (token_hash, user_id, created_at, last_used_at, expires_at, client) VALUES (?, ?, ?, ?, ?, ?)",
		tokenHash, userID, now, now, expiresAt, client)
	return err
}

// FindSession returns nil if there is no such session.
func FindSession(ctx context.Context, q Querier, tokenHash string) (*Session, error) {
	var s Session
	err := q.QueryRowContext(ctx, "SELECT user_id, last_used_at, expires_at FROM sessions WHERE token_hash = ?", tokenHash).
		Scan(&s.UserID, &s.LastUsedAt, &s.ExpiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return &s, err
}

// TouchSession extends a session that is in use.
func TouchSession(ctx context.Context, q Querier, tokenHash string, now, expiresAt int64) error {
	_, err := q.ExecContext(ctx, "UPDATE sessions SET last_used_at = ?, expires_at = ? WHERE token_hash = ?", now, expiresAt, tokenHash)
	return err
}

func DeleteSession(ctx context.Context, q Querier, tokenHash string) error {
	_, err := q.ExecContext(ctx, "DELETE FROM sessions WHERE token_hash = ?", tokenHash)
	return err
}

// DeleteUserSessions signs a user out everywhere except the session keepHash ("" signs out everywhere).
func DeleteUserSessions(ctx context.Context, q Querier, userID int64, keepHash string) error {
	_, err := q.ExecContext(ctx, "DELETE FROM sessions WHERE user_id = ? AND token_hash <> ?", userID, keepHash)
	return err
}

func PurgeExpiredSessions(ctx context.Context, q Querier, now int64) (int64, error) {
	res, err := q.ExecContext(ctx, "DELETE FROM sessions WHERE expires_at <= ?", now)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
