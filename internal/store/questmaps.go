package store

import (
	"context"
	"database/sql"
	"encoding/json"
)

// QuestMapClear is one cleared stage of a quest map: who cleared it and when (Unix ms).
type QuestMapClear struct {
	UserID int64  `json:"userId"`
	IGN    string `json:"ign"`
	At     int64  `json:"at"`
}

// QuestMap is a guild's chain of dungeons. Clears has one entry per cleared stage, in order.
type QuestMap struct {
	ID          int64           `json:"id"`
	Guild       string          `json:"-"`
	Name        string          `json:"name"`
	Style       string          `json:"style"`
	Variant     int             `json:"variant"`
	Dungeons    []string        `json:"dungeons"`
	Clears      []QuestMapClear `json:"clears"`
	StartsAt    int64           `json:"startsAt"`
	EndsAt      int64           `json:"endsAt"`
	CompletedAt *int64          `json:"completedAt,omitempty"`
}

const questMapColumns = "id, guild, name, style, variant, dungeons, clears, starts_at, ends_at, completed_at"

func scanQuestMap(row interface{ Scan(...any) error }) (*QuestMap, error) {
	var m QuestMap
	var dungeons, clears string
	var completed sql.NullInt64
	if err := row.Scan(&m.ID, &m.Guild, &m.Name, &m.Style, &m.Variant, &dungeons, &clears, &m.StartsAt, &m.EndsAt, &completed); err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(dungeons), &m.Dungeons); err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(clears), &m.Clears); err != nil {
		return nil, err
	}
	m.CompletedAt = nullInt(completed)
	return &m, nil
}

// QuestMaps returns a guild's quest maps, newest first, at most limit.
func QuestMaps(ctx context.Context, q Querier, guild string, limit int) ([]*QuestMap, error) {
	rows, err := q.QueryContext(ctx, "SELECT "+questMapColumns+" FROM quest_maps WHERE guild = ? ORDER BY starts_at DESC, id DESC LIMIT ?", guild, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var maps []*QuestMap
	for rows.Next() {
		m, err := scanQuestMap(rows)
		if err != nil {
			return nil, err
		}
		maps = append(maps, m)
	}
	return maps, rows.Err()
}

// InsertQuestMap stores a new map and sets its ID.
func InsertQuestMap(ctx context.Context, q Querier, m *QuestMap) error {
	dungeons, _ := json.Marshal(m.Dungeons)
	clears, _ := json.Marshal(m.Clears)
	res, err := q.ExecContext(ctx, "INSERT INTO quest_maps (guild, name, style, variant, dungeons, clears, starts_at, ends_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)",
		m.Guild, m.Name, m.Style, m.Variant, string(dungeons), string(clears), m.StartsAt, m.EndsAt)
	if err != nil {
		return err
	}
	m.ID, err = res.LastInsertId()
	return err
}

// SaveQuestMapProgress stores a map's clears and completion time.
func SaveQuestMapProgress(ctx context.Context, q Querier, m *QuestMap) error {
	clears, _ := json.Marshal(m.Clears)
	var completed any
	if m.CompletedAt != nil {
		completed = *m.CompletedAt
	}
	_, err := q.ExecContext(ctx, "UPDATE quest_maps SET clears = ?, completed_at = ? WHERE id = ?", string(clears), completed, m.ID)
	return err
}
