package store

import (
	"context"
	"path/filepath"
	"testing"
)

// A server that gets the drop log fills it from the collections already stored, once, on start.
func TestDropLogBackfill(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "test.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	// a collection stored before the drop log existed: written directly, not through RecordDrops
	if _, err := s.db.ExecContext(ctx, "INSERT INTO users (ign, password_hash, status, created_at) VALUES ('Bob', 'x', 'active', 0)"); err != nil {
		t.Fatal(err)
	}
	coll := `{"100":{"count":2,"history":[{"time":"2026-11-01T10:00:00","source":"drop","where":"Lost Halls","bag":"White"},` +
		`{"time":"2026-11-01T10:00:00","source":"drop","where":"Lost Halls","bag":"White"},{"time":"2026-11-01T11:00:00","source":"vault"}]}}`
	if _, err := s.db.ExecContext(ctx, "INSERT INTO user_data (user_id, kind, revision, data, updated_at) VALUES (1, 'collection', 1, ?, 0)", coll); err != nil {
		t.Fatal(err)
	}
	s.Close()

	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	drops, err := Drops(ctx, s.db, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(drops) != 2 || drops[0].ItemID != 100 {
		t.Fatalf("backfilled drops: %v", drops)
	}
}
