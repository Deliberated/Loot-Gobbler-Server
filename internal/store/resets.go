package store

import (
	"context"
	"database/sql"
	"errors"
)

// InsertPasswordReset stores a new reset code for userID, replacing any unused one the player still had.
func InsertPasswordReset(ctx context.Context, q Querier, userID int64, codeHash string, createdBy *int64, now, expiresAt int64) error {
	if _, err := q.ExecContext(ctx, "DELETE FROM password_resets WHERE user_id = ? AND used_at IS NULL", userID); err != nil {
		return err
	}
	_, err := q.ExecContext(ctx,
		"INSERT INTO password_resets (user_id, code_hash, created_by, created_at, expires_at) VALUES (?, ?, ?, ?, ?)",
		userID, codeHash, createdBy, now, expiresAt)
	return err
}

// FindUsablePasswordReset returns the id of the unused, unexpired code with this hash for userID, or 0 if none.
func FindUsablePasswordReset(ctx context.Context, q Querier, userID int64, codeHash string, now int64) (int64, error) {
	var id int64
	err := q.QueryRowContext(ctx,
		"SELECT id FROM password_resets WHERE user_id = ? AND code_hash = ? AND used_at IS NULL AND expires_at > ?",
		userID, codeHash, now).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return id, err
}

func MarkPasswordResetUsed(ctx context.Context, q Querier, id, now int64) error {
	_, err := q.ExecContext(ctx, "UPDATE password_resets SET used_at = ? WHERE id = ?", now, id)
	return err
}

// DeleteOldPasswordResets removes codes that were used or expired before cutoff (maintenance).
func DeleteOldPasswordResets(ctx context.Context, q Querier, cutoff int64) error {
	_, err := q.ExecContext(ctx, "DELETE FROM password_resets WHERE expires_at < ? OR used_at < ?", cutoff, cutoff)
	return err
}
