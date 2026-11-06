package api

import (
	"testing"
	"time"
)

func TestScoring(t *testing.T) {
	e := newEnv(t, nil)
	admin := e.newAdmin()
	bob := e.newUser(admin, "Bob", "203.0.113.2")
	admin.put("profile", map[string]any{"ign": "Hoster", "guild": "Gobblers", "guildRank": 40})
	bob.put("profile", map[string]any{"ign": "Bob", "guild": "Gobblers", "guildRank": 10})

	if w := bob.expect(200, "GET", "/api/v1/scoring", nil).body; w["feedPower"] != float64(1) || w["rarity"] != float64(1) {
		t.Fatalf("default weights: %v", w)
	}
	// moderators and users cannot change them; out of range or incomplete is refused
	bob.expect(403, "PUT", "/api/v1/admin/scoring", map[string]any{"feedPower": 0.5, "difficulty": 0.5, "rarity": 0.5})
	admin.expect(400, "PUT", "/api/v1/admin/scoring", map[string]any{"feedPower": 1.5, "difficulty": 0.5, "rarity": 0.5})
	admin.expect(400, "PUT", "/api/v1/admin/scoring", map[string]any{"feedPower": 0.5})
	admin.expect(200, "PUT", "/api/v1/admin/scoring", map[string]any{"feedPower": 0.25, "difficulty": 0, "rarity": 0.75})
	if w := bob.expect(200, "GET", "/api/v1/scoring", nil).body; w["feedPower"] != 0.25 || w["difficulty"] != float64(0) || w["rarity"] != 0.75 {
		t.Fatalf("stored weights: %v", w)
	}

	// the tallies: two identical drops count twice, the other enchantment count apart
	t1 := time.Now().UTC().Add(-time.Hour).Format("2006-01-02T15:04:05")
	t2 := time.Now().UTC().Add(-2 * time.Hour).Format("2006-01-02T15:04:05")
	drop := map[string]any{"time": t1, "source": "drop", "where": "Lost Halls", "bag": "White", "enchantIds": []any{5}}
	other := map[string]any{"time": t2, "source": "drop", "where": "Lost Halls", "bag": "White", "enchants": 3}
	bob.put("collection", map[string]any{"100": map[string]any{"count": 3, "history": []any{drop, drop, other}}})
	check := func(path, list string) {
		t.Helper()
		for _, m := range bob.expect(200, "GET", path, nil).body[list].([]any) {
			mm := m.(map[string]any)
			if mm["ign"] != "Bob" {
				continue
			}
			tally := mm["tally"].([]any)
			first := tally[0].(map[string]any)
			if len(tally) != 2 || first["count"] != float64(2) || first["enchants"] != float64(1) ||
				first["where"] != "Lost Halls" || tally[1].(map[string]any)["enchants"] != float64(3) {
				t.Fatalf("%s tally: %v", path, tally)
			}
			return
		}
		t.Fatalf("%s: Bob missing", path)
	}
	check("/api/v1/guild/caravan", "members")
	check("/api/v1/guild/leaderboard", "members")
}
