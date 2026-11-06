package api

import "testing"

func TestGuildLooks(t *testing.T) {
	e := newEnv(t, nil)
	admin := e.newAdmin()
	bob := e.newUser(admin, "Bob", "203.0.113.2")
	carl := e.newUser(admin, "Carl", "203.0.113.3")
	dave := e.newUser(admin, "Dave", "203.0.113.4") // another guild

	admin.put("profile", map[string]any{"ign": "Hoster", "guild": "Gobblers", "guildRank": 40})
	bob.put("profile", map[string]any{"ign": "Bob", "guild": "Gobblers", "guildRank": 10})
	carl.put("profile", map[string]any{"ign": "Carl", "guild": "Gobblers", "guildRank": 10})
	dave.put("profile", map[string]any{"ign": "Dave", "guild": "Others", "guildRank": 40})

	// Bob saved his look with his visit; Carl's client is older and only has his characters
	bob.put("dungeon_visits", []any{
		map[string]any{"dungeon": "The Nest", "start": 5_000_000, "durationMs": 600_000, "maxHp": 900,
			"hitTime": []int{1000, 200_000}, "hitDamage": []int{50, 80},
			"look": map[string]any{"player": "Bob", "classType": 819, "skin": 65011, "tex1": 7, "tex2": 9}},
	})
	carl.put("dungeon_visits", []any{map[string]any{"dungeon": "The Nest", "start": 5_000_000, "durationMs": 600_000}})
	carl.put("characters", map[string]any{
		"1": map[string]any{"charId": 1, "classType": 802, "skin": 901, "tex1": 3, "alive": true, "lastPlayed": 42},
	})
	dave.put("dungeon_visits", []any{
		map[string]any{"dungeon": "The Nest", "start": 5_000_000, "durationMs": 600_000,
			"look": map[string]any{"player": "Dave", "classType": 768, "skin": 0}},
	})

	looks := admin.expect(200, "GET", "/api/v1/guild/looks?dungeon=The%20Nest&from=5100000&to=5500000", nil).body["looks"].([]any)
	got := map[string]map[string]any{}
	carlVisit := false
	for _, l := range looks {
		m := l.(map[string]any)
		if m["player"] == "Carl" && m["source"] == "visit" {
			carlVisit = true // his visit without a look still counts for the hits
			continue
		}
		got[m["player"].(string)] = m
	}
	if !carlVisit {
		t.Fatalf("Carl's visit missing: %v", looks)
	}
	if len(got) != 2 || got["Dave"] != nil {
		t.Fatalf("looks: %v", looks)
	}
	if b := got["Bob"]; b["source"] != "visit" || b["skin"] != float64(65011) || b["tex2"] != float64(9) {
		t.Fatalf("Bob: %v", b)
	}
	// his hits timed from the asked start: the one long before the run is left out
	if b := got["Bob"]; b["maxHp"] != float64(900) || len(b["hitAt"].([]any)) != 1 ||
		b["hitAt"].([]any)[0] != float64(100_000) || b["hitDamage"].([]any)[0] != float64(80) {
		t.Fatalf("Bob's hits: %v", b)
	}
	if c := got["Carl"]; c["source"] != "character" || c["skin"] != float64(901) || c["alive"] != true {
		t.Fatalf("Carl: %v", c)
	}

	// another dungeon or time: Bob's visit does not match, so his characters (none) are all there is
	looks = admin.expect(200, "GET", "/api/v1/guild/looks?dungeon=The%20Nest&from=9000000&to=9100000", nil).body["looks"].([]any)
	for _, l := range looks {
		if l.(map[string]any)["player"] == "Bob" {
			t.Fatalf("Bob matched another run: %v", looks)
		}
	}
	admin.expect(400, "GET", "/api/v1/guild/looks?dungeon=The%20Nest", nil)
}

// Runs of one dungeon done back to back: the next one starts within the slack of the asked one, and is first in
// the list (newest first), but the hits come from the run that shares the asked time.
func TestGuildLooksBackToBack(t *testing.T) {
	e := newEnv(t, nil)
	admin := e.newAdmin()
	bob := e.newUser(admin, "Bob", "203.0.113.2")
	admin.put("profile", map[string]any{"ign": "Hoster", "guild": "Gobblers", "guildRank": 40})
	bob.put("profile", map[string]any{"ign": "Bob", "guild": "Gobblers", "guildRank": 10})
	bob.put("dungeon_visits", []any{
		map[string]any{"dungeon": "Ocean Trench", "start": 1_170_000, "durationMs": 116_000, "maxHp": 900,
			"hitTime": []int{5000}, "hitDamage": []int{30}},
		map[string]any{"dungeon": "Ocean Trench", "start": 1_000_000, "durationMs": 141_000, "maxHp": 900,
			"hitTime": []int{20_000, 90_000}, "hitDamage": []int{40, 60}},
	})
	looks := admin.expect(200, "GET", "/api/v1/guild/looks?dungeon=Ocean%20Trench&from=1001000&to=1141000", nil).body["looks"].([]any)
	if len(looks) != 1 {
		t.Fatalf("looks: %v", looks)
	}
	b := looks[0].(map[string]any)
	if hits := b["hitDamage"].([]any); len(hits) != 2 || hits[0] != float64(40) || hits[1] != float64(60) {
		t.Fatalf("Bob's hits come from the wrong run: %v", b)
	}
}
