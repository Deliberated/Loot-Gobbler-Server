package store

import (
	"context"
	"database/sql"
	"errors"
)

type Role string

const (
	RoleUser      Role = "user"
	RoleModerator Role = "moderator"
	RoleAdmin     Role = "admin"
)

// Rank orders roles: user < moderator < admin.
func (r Role) Rank() int {
	switch r {
	case RoleModerator:
		return 1
	case RoleAdmin:
		return 2
	}
	return 0
}

func (r Role) Valid() bool { return r == RoleUser || r == RoleModerator || r == RoleAdmin }

const (
	StatusPending = "pending"
	StatusActive  = "active"
)

type User struct {
	ID          int64  `json:"id"`
	IGN         string `json:"ign"`
	Status      string `json:"status"`
	Role        Role   `json:"role"`
	Root        bool   `json:"root"`
	CreatedAt   int64  `json:"createdAt"`
	LastLoginAt *int64 `json:"lastLoginAt,omitempty"`
}

func (u *User) Active() bool { return u.Status == StatusActive }

const userColumns = "id, ign, status, role, is_root, created_at, last_login_at"

type scanner interface{ Scan(dest ...any) error }

func scanUser(row scanner, extra ...any) (*User, error) {
	var u User
	var lastLogin sql.NullInt64
	dest := append([]any{&u.ID, &u.IGN, &u.Status, &u.Role, &u.Root, &u.CreatedAt, &lastLogin}, extra...)
	if err := row.Scan(dest...); err != nil {
		return nil, err
	}
	u.LastLoginAt = nullInt(lastLogin)
	return &u, nil
}

// GetUser returns nil if there is no such user.
func GetUser(ctx context.Context, q Querier, id int64) (*User, error) {
	u, err := scanUser(q.QueryRowContext(ctx, "SELECT "+userColumns+" FROM users WHERE id = ?", id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return u, err
}

// FindUserByIGN looks the IGN up case-insensitively and also returns the password hash. nil if not found.
func FindUserByIGN(ctx context.Context, q Querier, ign string) (*User, string, error) {
	var hash string
	u, err := scanUser(q.QueryRowContext(ctx, "SELECT "+userColumns+", password_hash FROM users WHERE ign = ?", ign), &hash)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, "", nil
	}
	return u, hash, err
}

func PasswordHash(ctx context.Context, q Querier, id int64) (string, error) {
	var hash string
	err := q.QueryRowContext(ctx, "SELECT password_hash FROM users WHERE id = ?", id).Scan(&hash)
	return hash, err
}

func CreatePendingUser(ctx context.Context, q Querier, ign, passwordHash string, now int64) (int64, error) {
	res, err := q.ExecContext(ctx,
		"INSERT INTO users (ign, password_hash, status, created_at) VALUES (?, ?, ?, ?)",
		ign, passwordHash, StatusPending, now)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// ListUsers returns users with the given status: pending ones oldest first, active ones by name.
func ListUsers(ctx context.Context, q Querier, status string) ([]User, error) {
	order := "ign COLLATE NOCASE"
	if status == StatusPending {
		order = "created_at"
	}
	rows, err := q.QueryContext(ctx, "SELECT "+userColumns+" FROM users WHERE status = ? ORDER BY "+order, status)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	users := []User{}
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		users = append(users, *u)
	}
	return users, rows.Err()
}

// ApproveUser activates a pending account. approvedBy is nil when a console token did it.
func ApproveUser(ctx context.Context, q Querier, id int64, approvedBy *int64, now int64) error {
	_, err := q.ExecContext(ctx, "UPDATE users SET status = ?, approved_at = ?, approved_by = ? WHERE id = ?",
		StatusActive, now, approvedBy, id)
	return err
}

func DeleteUser(ctx context.Context, q Querier, id int64) error {
	_, err := q.ExecContext(ctx, "DELETE FROM users WHERE id = ?", id)
	return err
}

func SetRole(ctx context.Context, q Querier, id int64, role Role, root bool) error {
	_, err := q.ExecContext(ctx, "UPDATE users SET role = ?, is_root = ? WHERE id = ?", role, root, id)
	return err
}

func SetPassword(ctx context.Context, q Querier, id int64, passwordHash string) error {
	_, err := q.ExecContext(ctx, "UPDATE users SET password_hash = ? WHERE id = ?", passwordHash, id)
	return err
}

func TouchLogin(ctx context.Context, q Querier, id, now int64) error {
	_, err := q.ExecContext(ctx, "UPDATE users SET last_login_at = ? WHERE id = ?", now, id)
	return err
}

func AdminExists(ctx context.Context, q Querier) (bool, error) {
	return exists(ctx, q, "SELECT 1 FROM users WHERE role = ? AND status = ? LIMIT 1", RoleAdmin, StatusActive)
}

func RootExists(ctx context.Context, q Querier) (bool, error) {
	return exists(ctx, q, "SELECT 1 FROM users WHERE is_root = 1 LIMIT 1")
}

func exists(ctx context.Context, q Querier, query string, args ...any) (bool, error) {
	var one int
	err := q.QueryRowContext(ctx, query, args...).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}
