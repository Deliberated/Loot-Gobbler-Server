package api

import (
	"testing"
	"time"
)

// A player who clears their collection keeps their drops in the guild views; identical drops count once each.
func TestDropsSurviveClearedCollection(t *testing.T) {
	e := newEnv(t, nil)
	admin := e.newAdmin()
	bob := e.newUser(admin, "Bob", "203.0.113.2")
	admin.put("profile", map[string]any{"ign": "Hoster", "guild": "Gobblers", "guildRank": 40})
	bob.put("profile", map[string]any{"ign": "Bob", "guild": "Gobblers", "guildRank": 10})

	t1 := time.Now().UTC().Add(-time.Hour).Format("2006-01-02T15:04:05")
	drop := map[string]any{"time": t1, "source": "drop", "where": "Lost Halls", "bag": "White", "enchantIds": []any{5, 7}}
	// two copies out of the same bag, plus a vault sighting that is not a drop
	bob.put("collection", map[string]any{"100": map[string]any{"count": 3, "history": []any{drop, drop,
		map[string]any{"time": t1, "source": "vault", "where": "Vault", "bag": "Vault chest"}}}})

	check := func(when string) {
		t.Helper()
		members := bob.expect(200, "GET", "/api/v1/guild/caravan", nil).body["members"].([]any)
		var total float64
		for _, m := range members {
			if mm := m.(map[string]any); mm["ign"] == "Bob" {
				total = mm["total"].(float64)
				items := mm["items"].([]any)
				if len(items) != 2 || items[0].(map[string]any)["enchants"].(float64) != 2 {
					t.Fatalf("%s: caravan items %v", when, items)
				}
			}
		}
		if total != 2 {
			t.Fatalf("%s: caravan total %v, want 2", when, total)
		}
		if drops := bob.expect(200, "GET", "/api/v1/guild/drops", nil).body["drops"].([]any); len(drops) != 2 {
			t.Fatalf("%s: recent drops %d, want 2", when, len(drops))
		}
	}
	check("after upload")

	// uploading the same again does not double them; clearing the collection does not remove them
	bob.expect(200, "PUT", "/api/v1/me/data/collection", map[string]any{"baseRevision": 1,
		"data": map[string]any{"100": map[string]any{"count": 2, "history": []any{drop, drop}}}})
	check("after the same upload")
	bob.expect(200, "PUT", "/api/v1/me/data/collection", map[string]any{"baseRevision": 2, "data": map[string]any{}})
	check("after clearing the collection")
}
