package api

import (
	"testing"
	"time"
)

// Clients report the game's seasons; the season running now starts when the one before it ended, and the season's
// caravan only counts what was found since.
func TestSeasons(t *testing.T) {
	e := newEnv(t, nil)
	now := time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC)
	e.srv.now = func() time.Time { return now }
	admin := e.newAdmin()
	bob := e.newUser(admin, "Bob", "203.0.113.2")
	admin.put("profile", map[string]any{"ign": "Hoster", "guild": "Gobblers", "guildRank": 40})
	bob.put("profile", map[string]any{"ign": "Bob", "guild": "Gobblers", "guildRank": 10})

	end := time.Date(2026, 12, 1, 9, 59, 59, 0, time.UTC)
	season := func(id int, name string, end time.Time) map[string]any {
		return map[string]any{"id": id, "name": name, "endDate": end.Unix()}
	}
	// a main season and a side track ending together, one already over and one too far off (both ignored)
	cur := bob.expect(200, "POST", "/api/v1/seasons", map[string]any{"seasons": []any{
		season(57, "Side Track", end), season(55, "Skuld", end),
		season(50, "Old", now.Add(-time.Hour)), season(99, "Bogus", now.Add(3*365*24*time.Hour)),
	}}).body["current"].(map[string]any)
	if names := cur["names"].([]any); len(names) != 2 || names[0] != "Skuld" || cur["start"].(float64) != 0 ||
		cur["end"].(float64) != float64(end.UnixMilli()) {
		t.Fatalf("current season %v", cur)
	}
	if n := len(bob.expect(200, "GET", "/api/v1/seasons", nil).body["seasons"].([]any)); n != 2 {
		t.Fatalf("%d seasons known, want 2", n)
	}

	// drops before and after the season's end: one by its exact time only
	before := map[string]any{"time": "2026-11-30T08:00:00", "source": "drop", "where": "Nest", "bag": "White"}
	exact := map[string]any{"time": "2026-12-01T05:00:00", "at": end.Add(time.Hour).UnixMilli(), "source": "drop", "where": "Nest", "bag": "White"}
	after := map[string]any{"time": "2026-12-02T08:00:00", "source": "drop", "where": "Nest", "bag": "White"}
	bob.put("collection", map[string]any{"100": map[string]any{"count": 3, "history": []any{before, exact, after}}})

	total := func(rng string) (float64, map[string]any) {
		t.Helper()
		body := bob.expect(200, "GET", "/api/v1/guild/caravan?range="+rng, nil).body
		for _, m := range body["members"].([]any) {
			if mm := m.(map[string]any); mm["ign"] == "Bob" {
				return mm["total"].(float64), body["season"].(map[string]any)
			}
		}
		t.Fatal("Bob not in the caravan")
		return 0, nil
	}
	// before the end: no season before this one known, so the season counts everything
	if n, _ := total("season"); n != 3 {
		t.Fatalf("season caravan before the change: %v items, want 3", n)
	}
	// after it, before anyone reported the next: the new season began at the old one's end
	now = end.Add(48 * time.Hour)
	n, s := total("season")
	if n != 2 || s["start"].(float64) != float64(end.UnixMilli()) || s["end"].(float64) != 0 {
		t.Fatalf("season caravan after the change: %v items, season %v", n, s)
	}
	if n, _ := total("all"); n != 3 {
		t.Fatalf("all-time caravan: %v items, want 3", n)
	}
	// the next season reported: still started at the old one's end
	next := end.Add(90 * 24 * time.Hour)
	bob.expect(200, "POST", "/api/v1/seasons", map[string]any{"seasons": []any{season(58, "Next", next)}})
	if _, s := total("season"); s["names"].([]any)[0] != "Next" || s["start"].(float64) != float64(end.UnixMilli()) {
		t.Fatalf("next season %v", s)
	}
}
