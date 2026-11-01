package store

import (
	"context"
	"database/sql"
	"errors"
)

// RoleToken is an unused token that grants Kind (admin or moderator) to whoever redeems it.
type RoleToken struct {
	ID        int64
	Kind      Role
	CreatedBy *int64 // nil = printed in the server console
}

// OpenToken describes a token that has not been used yet. The token itself is never stored, only its hash.
type OpenToken struct {
	ID        int64  `json:"id"`
	Note      string `json:"note,omitempty"`
	CreatedBy string `json:"createdBy,omitempty"`
	CreatedAt int64  `json:"createdAt"`
	ExpiresAt *int64 `json:"expiresAt,omitempty"`
}

func InsertRoleToken(ctx context.Context, q Querier, tokenHash string, kind Role, note string, createdBy *int64, now int64, expiresAt *int64) (int64, error) {
	res, err := q.ExecContext(ctx,
		"INSERT INTO role_tokens (token_hash, kind, note, created_by, created_at, expires_at) VALUES (?, ?, ?, ?, ?, ?)",
		tokenHash, kind, note, createdBy, now, expiresAt)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// DeleteUnusedConsoleTokens invalidates admin claim tokens printed earlier, so only the newest one works.
func DeleteUnusedConsoleTokens(ctx context.Context, q Querier) error {
	_, err := q.ExecContext(ctx, "DELETE FROM role_tokens WHERE created_by IS NULL AND used_at IS NULL")
	return err
}

// FindUsableRoleToken returns the token with this hash if it is unused and not expired, else nil.
func FindUsableRoleToken(ctx context.Context, q Querier, tokenHash string, now int64) (*RoleToken, error) {
	var t RoleToken
	var createdBy sql.NullInt64
	err := q.QueryRowContext(ctx,
		"SELECT id, kind, created_by FROM role_tokens WHERE token_hash = ? AND used_at IS NULL AND (expires_at IS NULL OR expires_at > ?)",
		tokenHash, now).Scan(&t.ID, &t.Kind, &createdBy)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	t.CreatedBy = nullInt(createdBy)
	return &t, err
}

func MarkRoleTokenUsed(ctx context.Context, q Querier, id, userID, now int64) error {
	_, err := q.ExecContext(ctx, "UPDATE role_tokens SET used_by = ?, used_at = ? WHERE id = ?", userID, now, id)
	return err
}

// ListOpenRoleTokens returns unused, unexpired tokens of kind, newest first.
func ListOpenRoleTokens(ctx context.Context, q Querier, kind Role, now int64) ([]OpenToken, error) {
	rows, err := q.QueryContext(ctx, `
		SELECT t.id, COALESCE(t.note, ''), COALESCE(u.ign, ''), t.created_at, t.expires_at
		FROM role_tokens t LEFT JOIN users u ON u.id = t.created_by
		WHERE t.kind = ? AND t.used_at IS NULL AND (t.expires_at IS NULL OR t.expires_at > ?)
		ORDER BY t.created_at DESC`, kind, now)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	tokens := []OpenToken{}
	for rows.Next() {
		var t OpenToken
		var expires sql.NullInt64
		if err := rows.Scan(&t.ID, &t.Note, &t.CreatedBy, &t.CreatedAt, &expires); err != nil {
			return nil, err
		}
		t.ExpiresAt = nullInt(expires)
		tokens = append(tokens, t)
	}
	return tokens, rows.Err()
}

// DeleteUnusedRoleToken revokes a token of kind. It reports false if there was no such unused token.
func DeleteUnusedRoleToken(ctx context.Context, q Querier, id int64, kind Role) (bool, error) {
	res, err := q.ExecContext(ctx, "DELETE FROM role_tokens WHERE id = ? AND kind = ? AND used_at IS NULL", id, kind)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}
