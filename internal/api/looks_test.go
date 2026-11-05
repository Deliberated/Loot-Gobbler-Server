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
		map[string]any{"dungeon": "The Nest", "start": 5_000_000, "durationMs": 600_000,
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
	for _, l := range looks {
		m := l.(map[string]any)
		got[m["player"].(string)] = m
	}
	if len(got) != 2 || got["Dave"] != nil {
		t.Fatalf("looks: %v", looks)
	}
	if b := got["Bob"]; b["source"] != "visit" || b["skin"] != float64(65011) || b["tex2"] != float64(9) {
		t.Fatalf("Bob: %v", b)
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
