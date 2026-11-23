package api

import "testing"

func TestGuildUpeRuns(t *testing.T) {
	e := newEnv(t, nil)
	admin := e.newAdmin()
	bob := e.newUser(admin, "Bob", "203.0.113.2")
	dave := e.newUser(admin, "Dave", "203.0.113.4") // another guild
	admin.put("profile", map[string]any{"ign": "Hoster", "guild": "Gobblers", "guildRank": 40})
	bob.put("profile", map[string]any{"ign": "Bob", "guild": "Gobblers", "guildRank": 10})
	dave.put("profile", map[string]any{"ign": "Dave", "guild": "Others", "guildRank": 40})

	bob.put("characters", map[string]any{
		"7": map[string]any{"classType": 782, "className": "Wizard", "alive": false},
	})
	bob.put("upe", map[string]any{"version": 1, "runs": map[string]any{
		"7": map[string]any{"charId": 7, "mode": "UPE", "marked": true, "markedAt": 1000, "brokenAt": 0, "gathered": []any{
			map[string]any{"item": 100, "enchants": []int{1, 2}, "at": 2000, "how": "D", "where": "Lost Halls"},
			map[string]any{"item": 100, "at": 3000, "how": "D", "where": "Snake Pit"},
		}},
		"8": map[string]any{"charId": 8, "mode": "gUPE", "marked": true, "brokenAt": 5000, "broken": "Equipped a vault item",
			"notes": []string{"Traded with a guild mate"}, "gathered": []any{}},
		"9": map[string]any{"charId": 9, "mode": "UPE", "marked": false}, // not a run: left out
	}})
	dave.put("upe", map[string]any{"runs": map[string]any{"1": map[string]any{"charId": 1, "mode": "UPE", "marked": true}}})

	body := bob.expect(200, "GET", "/api/v1/guild/upe/runs", nil).body
	members := body["members"].([]any)
	if body["guild"] != "Gobblers" || len(members) != 2 {
		t.Fatalf("upe: %v", body)
	}
	b := members[0].(map[string]any)
	runs := b["runs"].([]any)
	if b["ign"] != "Bob" || len(runs) != 2 {
		t.Fatalf("Bob's runs: %v", b)
	}
	lost, wiz := runs[0].(map[string]any), runs[1].(map[string]any) // newest character first
	if lost["charId"] != 8.0 || lost["lost"] != true || lost["character"] != nil || lost["why"] != "Equipped a vault item" ||
		lost["lostAt"] != 5000.0 || len(lost["notes"].([]any)) != 1 {
		t.Fatalf("lost run: %v", lost)
	}
	if _, ok := wiz["why"]; ok {
		t.Fatalf("a standing run has no reason: %v", wiz)
	}
	g := wiz["gathered"].([]any)
	if wiz["mode"] != "UPE" || wiz["alive"] != false || wiz["character"].(map[string]any)["className"] != "Wizard" || len(g) != 2 ||
		g[0].(map[string]any)["enchants"] != 2.0 || g[1].(map[string]any)["enchants"] != -1.0 {
		t.Fatalf("wizard run: %v", wiz)
	}
	if hoster := members[1].(map[string]any); len(hoster["runs"].([]any)) != 0 {
		t.Fatalf("member without runs: %v", hoster)
	}
}
