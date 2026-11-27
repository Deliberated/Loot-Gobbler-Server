package api

import (
	"strconv"
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

// The drop log comes a page at a time after the last drop a client has, only the guild's, with this database's
// instance.
func TestDropLogPages(t *testing.T) {
	e := newEnv(t, nil)
	admin := e.newAdmin()
	bob := e.newUser(admin, "Bob", "203.0.113.2")
	eve := e.newUser(admin, "Eve", "203.0.113.3")
	admin.put("profile", map[string]any{"ign": "Hoster", "guild": "Gobblers", "guildRank": 40})
	bob.put("profile", map[string]any{"ign": "Bob", "guild": "Gobblers", "guildRank": 10})
	eve.put("profile", map[string]any{"ign": "Eve", "guild": "Others", "guildRank": 10})
	at := func(h int) string { return time.Now().UTC().Add(-time.Duration(h) * time.Hour).Format("2006-01-02T15:04:05") }
	find := func(h int) map[string]any {
		return map[string]any{"time": at(h), "source": "drop", "where": "Lost Halls", "bag": "White", "enchants": 1}
	}
	bob.put("collection", map[string]any{"100": map[string]any{"count": 2, "history": []any{find(3), find(2)}}})
	eve.put("collection", map[string]any{"300": map[string]any{"count": 1, "history": []any{find(1)}}})
	admin.put("collection", map[string]any{"200": map[string]any{"count": 1, "history": []any{find(1)}}})

	first := bob.expect(200, "GET", "/api/v1/guild/drops/log?limit=2", nil).body
	drops := first["drops"].([]any)
	if len(drops) != 2 || first["more"] != true || first["instance"] == "" || first["guild"] != "Gobblers" {
		t.Fatalf("first page %v", first)
	}
	last := int(drops[1].(map[string]any)["id"].(float64))
	if int(drops[0].(map[string]any)["id"].(float64)) >= last {
		t.Fatalf("not in log order: %v", drops)
	}
	rest := bob.expect(200, "GET", "/api/v1/guild/drops/log?limit=2&after="+strconv.Itoa(last), nil).body
	more := rest["drops"].([]any)
	if len(more) != 1 || rest["more"] != false || more[0].(map[string]any)["itemId"].(float64) != 200 {
		t.Fatalf("second page %v", rest)
	}
	if rest["instance"] != first["instance"] {
		t.Fatalf("instance changed: %v, %v", first["instance"], rest["instance"])
	}
	end := int(more[0].(map[string]any)["id"].(float64))
	none := bob.expect(200, "GET", "/api/v1/guild/drops/log?after="+strconv.Itoa(end), nil).body
	if len(none["drops"].([]any)) != 0 || none["more"] != false {
		t.Fatalf("after the last %v", none)
	}
	bob.expect(400, "GET", "/api/v1/guild/drops/log?limit=501", nil)
}

// A member's balloon colour, from their profile, comes with the caravan; none picked: none sent.
func TestCaravanBalloonColour(t *testing.T) {
	e := newEnv(t, nil)
	admin := e.newAdmin()
	bob := e.newUser(admin, "Bob", "203.0.113.2")
	admin.put("profile", map[string]any{"ign": "Hoster", "guild": "Gobblers", "guildRank": 40})
	bob.put("profile", map[string]any{"ign": "Bob", "guild": "Gobblers", "guildRank": 10, "balloon": 4})
	members := bob.expect(200, "GET", "/api/v1/guild/caravan", nil).body["members"].([]any)
	got := map[string]any{}
	for _, m := range members {
		mm := m.(map[string]any)
		got[mm["ign"].(string)] = mm["balloon"]
	}
	if got["Bob"] != 4.0 || got["Hoster"] != nil {
		t.Fatalf("balloons %v", got)
	}
}
