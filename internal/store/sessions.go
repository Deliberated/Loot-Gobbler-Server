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
	Client     string // the app and its version, e.g. "Rust v0.0.1" ("" if it never said)
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
	var client sql.NullString
	err := q.QueryRowContext(ctx, "SELECT user_id, last_used_at, expires_at, client FROM sessions WHERE token_hash = ?", tokenHash).
		Scan(&s.UserID, &s.LastUsedAt, &s.ExpiresAt, &client)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	s.Client = client.String
	return &s, err
}

// SetSessionClient notes which app (and version) uses a session, e.g. after the app was updated.
func SetSessionClient(ctx context.Context, q Querier, tokenHash, client string) error {
	_, err := q.ExecContext(ctx, "UPDATE sessions SET client = ? WHERE token_hash = ?", client, tokenHash)
	return err
}

// UserClient is the app a user used last: their most recently used session that has not expired.
type UserClient struct {
	Client     string
	LastUsedAt int64
}

// LatestClients gives, per user with a session that has not expired, the app of the one used last.
func LatestClients(ctx context.Context, q Querier, now int64) (map[int64]UserClient, error) {
	rows, err := q.QueryContext(ctx,
		"SELECT user_id, COALESCE(client, ''), last_used_at FROM sessions WHERE expires_at > ? ORDER BY last_used_at", now)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]UserClient{}
	for rows.Next() {
		var id int64
		var c UserClient
		if err := rows.Scan(&id, &c.Client, &c.LastUsedAt); err != nil {
			return nil, err
		}
		out[id] = c // the later rows win
	}
	return out, rows.Err()
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
