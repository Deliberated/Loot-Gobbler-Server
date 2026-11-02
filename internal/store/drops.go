package store

import (
	"context"
	"encoding/json"
	"strconv"
)

// Drop is one logged drop: the item and the find as the client sent it (JSON).
type Drop struct {
	ItemID int
	Data   json.RawMessage
}

// The parts of a collection document the drop log reads.
type collectionFinds map[string]struct {
	History []json.RawMessage `json:"history"`
}

type findKey struct {
	Source string `json:"source"`
	Time   string `json:"time"`
	Where  string `json:"where"`
	Bag    string `json:"bag"`
}

// RecordDrops adds the drops of a collection document that are not logged yet. Nothing is ever removed: a drop
// missing from a later upload (collection cleared, history cut short) stays logged. Identical drops (same item,
// time, place and bag) are told apart by how many of them the document has.
func RecordDrops(ctx context.Context, q Querier, userID int64, collection []byte) (int, error) {
	var coll collectionFinds
	if err := json.Unmarshal(collection, &coll); err != nil {
		return 0, nil // not a collection we can read: nothing to log
	}
	added := 0
	for id, e := range coll {
		itemID, err := strconv.Atoi(id)
		if err != nil {
			continue
		}
		copies := map[findKey]int{}
		for _, raw := range e.History {
			var k findKey
			if json.Unmarshal(raw, &k) != nil || k.Source != "drop" || k.Time == "" {
				continue
			}
			k.Source = ""
			n := copies[k]
			copies[k] = n + 1
			res, err := q.ExecContext(ctx, `INSERT OR IGNORE INTO drop_log (user_id, item_id, time, place, bag, copy, data)
				VALUES (?, ?, ?, ?, ?, ?, ?)`, userID, itemID, k.Time, k.Where, k.Bag, n, string(raw))
			if err != nil {
				return added, err
			}
			if c, _ := res.RowsAffected(); c > 0 {
				added++
			}
		}
	}
	return added, nil
}

// Drops returns all logged drops of a user, newest first.
func Drops(ctx context.Context, q Querier, userID int64) ([]Drop, error) {
	rows, err := q.QueryContext(ctx, "SELECT item_id, data FROM drop_log WHERE user_id = ? ORDER BY time DESC", userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Drop
	for rows.Next() {
		var d Drop
		var data string
		if err := rows.Scan(&d.ItemID, &data); err != nil {
			return nil, err
		}
		d.Data = json.RawMessage(data)
		out = append(out, d)
	}
	return out, rows.Err()
}

// backfillDrops logs the drops of every stored collection, once: when the drop log is still empty (a server that
// just got it). Collections uploaded later are logged as they come in.
func backfillDrops(ctx context.Context, s *Store) error {
	var n int
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM drop_log").Scan(&n); err != nil || n > 0 {
		return err
	}
	return s.Tx(ctx, func(q Querier) error {
		rows, err := q.QueryContext(ctx, "SELECT user_id, data FROM user_data WHERE kind = 'collection'")
		if err != nil {
			return err
		}
		type doc struct {
			user int64
			data string
		}
		var docs []doc
		for rows.Next() {
			var d doc
			if err := rows.Scan(&d.user, &d.data); err != nil {
				rows.Close()
				return err
			}
			docs = append(docs, d)
		}
		rows.Close()
		for _, d := range docs {
			if _, err := RecordDrops(ctx, q, d.user, []byte(d.data)); err != nil {
				return err
			}
		}
		return nil
	})
}
