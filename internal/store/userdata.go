package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
)

// DataItem is one stored JSON document. Data is left out when only revisions were asked for.
type DataItem struct {
	Revision  int64           `json:"revision"`
	UpdatedAt int64           `json:"updatedAt"`
	Data      json.RawMessage `json:"data,omitempty"`
}

// ListData returns the user's documents by kind. kinds limits the result (nil = all); withData false returns
// only revisions and times.
func ListData(ctx context.Context, q Querier, userID int64, kinds []string, withData bool) (map[string]DataItem, error) {
	dataCol := "NULL"
	if withData {
		dataCol = "data"
	}
	query := "SELECT kind, revision, updated_at, " + dataCol + " FROM user_data WHERE user_id = ?"
	args := []any{userID}
	if kinds != nil {
		if len(kinds) == 0 {
			return map[string]DataItem{}, nil
		}
		query += " AND kind IN (?" + strings.Repeat(", ?", len(kinds)-1) + ")"
		for _, k := range kinds {
			args = append(args, k)
		}
	}
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := map[string]DataItem{}
	for rows.Next() {
		var kind string
		var item DataItem
		var data sql.NullString
		if err := rows.Scan(&kind, &item.Revision, &item.UpdatedAt, &data); err != nil {
			return nil, err
		}
		if data.Valid {
			item.Data = json.RawMessage(data.String)
		}
		items[kind] = item
	}
	return items, rows.Err()
}

// GetData returns one document, or nil if the user has none of that kind.
func GetData(ctx context.Context, q Querier, userID int64, kind string) (*DataItem, error) {
	var item DataItem
	var data string
	err := q.QueryRowContext(ctx, "SELECT revision, updated_at, data FROM user_data WHERE user_id = ? AND kind = ?", userID, kind).
		Scan(&item.Revision, &item.UpdatedAt, &data)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	item.Data = json.RawMessage(data)
	return &item, err
}

func CountData(ctx context.Context, q Querier, userID int64) (int, error) {
	var n int
	err := q.QueryRowContext(ctx, "SELECT COUNT(*) FROM user_data WHERE user_id = ?", userID).Scan(&n)
	return n, err
}

// PutData stores a document at revision and logs the change. It returns the change's sequence number.
func PutData(ctx context.Context, q Querier, userID int64, kind string, revision int64, data []byte, now int64) (int64, error) {
	_, err := q.ExecContext(ctx, `
		INSERT INTO user_data (user_id, kind, revision, data, updated_at) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (user_id, kind) DO UPDATE SET revision = excluded.revision, data = excluded.data, updated_at = excluded.updated_at`,
		userID, kind, revision, string(data), now)
	if err != nil {
		return 0, err
	}
	res, err := q.ExecContext(ctx, "INSERT INTO changes (user_id, kind, revision, at) VALUES (?, ?, ?, ?)", userID, kind, revision, now)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// MemberDoc is one active user's document of one kind.
type MemberDoc struct {
	UserID    int64
	IGN       string
	UpdatedAt int64
	Data      json.RawMessage
}

// DocsOfKind returns the document of the given kind of every active user who has one.
func DocsOfKind(ctx context.Context, q Querier, kind string) ([]MemberDoc, error) {
	rows, err := q.QueryContext(ctx, `
		SELECT d.user_id, u.ign, d.updated_at, d.data
		FROM user_data d JOIN users u ON u.id = d.user_id
		WHERE d.kind = ? AND u.status = ?`, kind, StatusActive)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var docs []MemberDoc
	for rows.Next() {
		var d MemberDoc
		var data string
		if err := rows.Scan(&d.UserID, &d.IGN, &d.UpdatedAt, &data); err != nil {
			return nil, err
		}
		d.Data = json.RawMessage(data)
		docs = append(docs, d)
	}
	return docs, rows.Err()
}

// LastActivity is when each user last uploaded anything (epoch ms), by user id.
func LastActivity(ctx context.Context, q Querier) (map[int64]int64, error) {
	rows, err := q.QueryContext(ctx, "SELECT user_id, MAX(updated_at) FROM user_data GROUP BY user_id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	last := map[int64]int64{}
	for rows.Next() {
		var id, at int64
		if err := rows.Scan(&id, &at); err != nil {
			return nil, err
		}
		last[id] = at
	}
	return last, rows.Err()
}

// LatestSeq is the sequence number of the newest change (0 if none).
func LatestSeq(ctx context.Context, q Querier) (int64, error) {
	var seq int64
	err := q.QueryRowContext(ctx, "SELECT COALESCE(MAX(seq), 0) FROM changes").Scan(&seq)
	return seq, err
}
