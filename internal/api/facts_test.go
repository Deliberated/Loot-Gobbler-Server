package api

import (
	"testing"
	"time"
)

func TestGuildFacts(t *testing.T) {
	e := newEnv(t, nil)
	admin := e.newAdmin()
	bob := e.newUser(admin, "Bob", "203.0.113.2")
	carl := e.newUser(admin, "Carl", "203.0.113.3")
	dave := e.newUser(admin, "Dave", "203.0.113.4") // another guild

	admin.put("profile", map[string]any{"ign": "Hoster", "guild": "Gobblers", "guildRank": 40})
	bob.put("profile", map[string]any{"ign": "Bob", "guild": "Gobblers", "guildRank": 10, "stSets": 4})
	carl.put("profile", map[string]any{"ign": "Carl", "guild": "Gobblers", "guildRank": 10})
	dave.put("profile", map[string]any{"ign": "Dave", "guild": "Others", "guildRank": 40})

	now := time.Now().UnixMilli()
	day := int64(24 * 3600 * 1000)
	// Bob: 30 Nests over two characters (the dead one counts too), the Knight did most of them. The Trickster died
	// yesterday after gaining 300 fame (from 100 before the 3 days); an older death is not counted.
	bob.put("characters", map[string]any{
		"1": map[string]any{"classType": 804, "alive": false, "lastPlayed": now - day,
			"dungeons": map[string]int{"The Nest": 10, "Mad Lab": 12},
			"samples":  []map[string]int64{{"t": now - 5*day, "fame": 100}, {"t": now - 2*day, "fame": 250}, {"t": now - day, "fame": 400}}},
		"2": map[string]any{"classType": 798, "skin": 901, "tex1": 3, "alive": true, "lastPlayed": now,
			"dungeons": map[string]int{"The Nest": 20},
			"samples":  []map[string]int64{{"t": now - day, "fame": 1000}, {"t": now, "fame": 1050}}},
		"3": map[string]any{"classType": 782, "alive": false, "lastPlayed": now - 10*day},
	})
	bob.put("dungeon_visits", []any{
		map[string]any{"dungeon": "The Nest", "start": now - day, "durationMs": 600_000, "dealt": []int{100, 250}, "hitDamage": []int{50, 80},
			"exalts": []map[string]int{{"classType": 798, "stat": 2, "before": 4, "after": 16}, {"classType": 798, "stat": 3, "before": 16, "after": 17}}},
		map[string]any{"dungeon": "Mad Lab", "start": now - 6*day, "dealt": []int{9999}, "hitDamage": []int{9999}},
	})
	// Bob found two items yesterday (two copies from one bag) and one a week ago
	recent := map[string]any{"at": now - day, "time": "2026-01-01T10:00:00", "source": "drop", "where": "The Nest", "bag": "White"}
	old := map[string]any{"at": now - 7*day, "time": "2025-12-25T10:00:00", "source": "drop", "where": "Mad Lab", "bag": "White"}
	bob.put("collection", map[string]any{
		"100": map[string]any{"count": 2, "history": []any{recent, recent}},
		"200": map[string]any{"count": 1, "history": []any{old}},
	})
	admin.put("characters", map[string]any{
		"1": map[string]any{"classType": 782, "alive": true, "dungeons": map[string]int{"Lost Halls": 5}},
	})
	carl.put("characters", map[string]any{"1": map[string]any{"classType": 802, "alive": true}}) // an older client
	dave.put("characters", map[string]any{"1": map[string]any{"classType": 768, "alive": false, "lastPlayed": now}})

	body := admin.expect(200, "GET", "/api/v1/guild/facts", nil).body
	if body["days"] != float64(3) {
		t.Fatalf("days: %v", body)
	}
	got := map[string]map[string]any{}
	for _, m := range body["members"].([]any) {
		got[m.(map[string]any)["ign"].(string)] = m.(map[string]any)
	}
	if len(got) != 3 || got["Dave"] != nil {
		t.Fatalf("members: %v", got)
	}
	b := got["Bob"]
	fav := b["favorite"].(map[string]any)
	if fav["dungeon"] != "The Nest" || fav["count"] != float64(30) || fav["classType"] != float64(798) || fav["skin"] != float64(901) {
		t.Fatalf("Bob's favorite: %v", fav)
	}
	if b["deaths"] != float64(1) || b["fame"] != float64(350) || b["dealt"] != float64(350) || b["taken"] != float64(130) ||
		b["hits"] != float64(2) || b["inMs"] != float64(600_000) || b["exalts"] != float64(2) ||
		b["loot"] != float64(2) || b["played"] != float64(2) || b["stSets"] != float64(4) {
		t.Fatalf("Bob: %v", b)
	}
	if l := b["look"].(map[string]any); l["classType"] != float64(798) || l["skin"] != float64(901) {
		t.Fatalf("Bob's look: %v", l)
	}
	if h := got["Hoster"]["favorite"].(map[string]any); h["dungeon"] != "Lost Halls" || h["count"] != float64(5) {
		t.Fatalf("Hoster: %v", h)
	}
	if c := got["Carl"]; c["favorite"] != nil || c["deaths"] != float64(0) || c["stSets"] != float64(-1) {
		t.Fatalf("Carl: %v", c)
	}
	admin.expect(400, "GET", "/api/v1/guild/facts?days=99", nil)
}
