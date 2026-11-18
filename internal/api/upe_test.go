package api

import "testing"

func TestGuildUpe(t *testing.T) {
	e := newEnv(t, nil)
	admin := e.newAdmin()
	bob := e.newUser(admin, "Bob", "203.0.113.2")
	dave := e.newUser(admin, "Dave", "203.0.113.4") // another guild

	admin.put("profile", map[string]any{"ign": "Hoster", "guild": "Gobblers", "guildRank": 40})
	bob.put("profile", map[string]any{"ign": "Bob", "guild": "Gobblers", "guildRank": 10})
	dave.put("profile", map[string]any{"ign": "Dave", "guild": "Others", "guildRank": 40})

	trade := func(at int64) map[string]any {
		return map[string]any{"at": at, "me": "Bob", "partner": "Hoster", "mode": "gUPE", "standing": true,
			"gave": []any{map[string]any{"item": 1041, "clean": true}}, "got": []any{}, "status": "P"}
	}
	bob.put("upe", map[string]any{"version": 1, "runs": map[string]any{
		"7": map[string]any{"charId": 7, "mode": "gUPE", "trades": []any{trade(1_000), trade(9_000)}},
		"8": map[string]any{"charId": 8, "mode": "UPE"},
	}})
	dave.put("upe", map[string]any{"version": 1, "runs": map[string]any{
		"3": map[string]any{"charId": 3, "trades": []any{trade(9_500)}},
	}})

	members := admin.expect(200, "GET", "/api/v1/guild/upe?since=5000", nil).body["members"].([]any)
	if len(members) != 1 {
		t.Fatalf("only the guild mate Bob, not Dave nor the asker: %v", members)
	}
	b := members[0].(map[string]any)
	trades := b["trades"].([]any)
	if b["ign"] != "Bob" || len(trades) != 1 || trades[0].(map[string]any)["at"] != float64(9_000) {
		t.Fatalf("Bob's trades since 5000: %v", b)
	}
	if g := trades[0].(map[string]any)["gave"].([]any)[0].(map[string]any); g["item"] != float64(1041) || g["clean"] != true {
		t.Fatalf("the report as the client wrote it: %v", trades[0])
	}
	// a guild mate without a upe document is listed with no trades: in the guild, but no report
	bobs := bob.expect(200, "GET", "/api/v1/guild/upe?since=0", nil).body["members"].([]any)
	if len(bobs) != 1 || bobs[0].(map[string]any)["ign"] != "Hoster" || len(bobs[0].(map[string]any)["trades"].([]any)) != 0 {
		t.Fatalf("Hoster for Bob: %v", bobs)
	}
	admin.expect(400, "GET", "/api/v1/guild/upe", nil)
}
