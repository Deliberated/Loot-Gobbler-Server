package store

import "context"

// Season is one of the game's seasons as clients reported it.
type Season struct {
	ID        int64
	Name      string
	EndAt     int64 // epoch ms
	FirstSeen int64 // epoch ms
}

// ReportSeason records a season a client read from the game: a new one with now as its first sighting; a known one
// takes the name and end date reported (the game may move the end).
func ReportSeason(ctx context.Context, q Querier, s Season, by, now int64) error {
	_, err := q.ExecContext(ctx, `INSERT INTO seasons (id, name, end_at, first_seen, first_by, updated_at) VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT (id) DO UPDATE SET name = excluded.name, end_at = excluded.end_at, updated_at = excluded.updated_at
		WHERE seasons.name <> excluded.name OR seasons.end_at <> excluded.end_at`,
		s.ID, s.Name, s.EndAt, now, by, now)
	return err
}

// Seasons lists every season known, by end date and id.
func Seasons(ctx context.Context, q Querier) ([]Season, error) {
	rows, err := q.QueryContext(ctx, "SELECT id, name, end_at, first_seen FROM seasons ORDER BY end_at, id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Season
	for rows.Next() {
		var s Season
		if err := rows.Scan(&s.ID, &s.Name, &s.EndAt, &s.FirstSeen); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}
