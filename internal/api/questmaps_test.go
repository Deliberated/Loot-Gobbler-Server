package api

import (
	"testing"
	"time"

	"github.com/Deliberated/Loot-Gobbler-Server/internal/store"
)

func TestQuestMapsDoNotRepeatStyleOrColor(t *testing.T) {
	for s, colors := range questMapColors {
		seen := map[string]bool{}
		for _, c := range colors {
			if seen[c] {
				t.Fatalf("%s has two %s colorways: a roll could run out of colors", s, c)
			}
			seen[c] = true
		}
	}
	var recent []*store.QuestMap // newest first
	for i := 0; i < 1000; i++ {
		m := newQuestMap("gobblers", 0, recent)
		for k := 0; k < noRepeatMaps && k < len(recent); k++ {
			r := recent[k]
			if r.Style == m.Style || questMapColors[r.Style][r.Variant] == questMapColors[m.Style][m.Variant] {
				t.Fatalf("map %d (%s %d) repeats map %d back (%s %d)", i, m.Style, m.Variant, k+1, r.Style, r.Variant)
			}
		}
		recent = append([]*store.QuestMap{m}, recent...)
	}
}

func TestNewQuestMapIsInRange(t *testing.T) {
	for i := 0; i < 200; i++ {
		m := newQuestMap("gobblers", 0, nil)
		days := time.Duration(m.EndsAt-m.StartsAt) * time.Millisecond / (24 * time.Hour)
		if len(m.Dungeons) < minStages || len(m.Dungeons) > maxStages || days < minMapDays || days > maxMapDays ||
			m.Variant < 0 || m.Variant >= questMapVariants || questMapStyles[m.Style] == nil {
			t.Fatalf("map out of range: %+v", m)
		}
		seen := map[string]bool{}
		for _, d := range m.Dungeons {
			if seen[d] {
				t.Fatalf("dungeon twice: %v", m.Dungeons)
			}
			seen[d] = true
		}
	}
}

func TestGuildQuestMaps(t *testing.T) {
	e := newEnv(t, nil)
	admin := e.newAdmin()
	bob := e.newUser(admin, "Bob", "203.0.113.2")
	admin.put("profile", map[string]any{"ign": "Hoster", "guild": "Gobblers", "guildRank": 40})
	bob.put("profile", map[string]any{"ign": "Bob", "guild": "Gobblers", "guildRank": 10})

	first := bob.expect(200, "GET", "/api/v1/guild/questmaps", nil).body
	cur := first["current"].(map[string]any)
	dungeons := cur["dungeons"].([]any)
	if len(dungeons) < 3 || len(cur["clears"].([]any)) != 0 || len(first["past"].([]any)) != 0 {
		t.Fatalf("new map: %v", first)
	}
	// the same map for every member
	if again := admin.expect(200, "GET", "/api/v1/guild/questmaps", nil).body["current"].(map[string]any); again["id"] != cur["id"] {
		t.Fatalf("members see different maps: %v vs %v", again["id"], cur["id"])
	}

	start := int64(cur["startsAt"].(float64))
	run := func(dungeon string, at int64, me bool) map[string]any {
		return map[string]any{"dungeon": dungeon, "start": at, "bosses": []any{map[string]any{
			"players": []any{map[string]any{"damage": 1000, "me": me}}}}}
	}
	// Bob clears stage 1, then stage 3 too early (stage 2 is not done) and stage 2 without hitting the boss;
	// the hoster clears stage 2, which counts for the guild.
	bob.put("dps_history", []any{
		run(dungeons[0].(string), start+1000, true),
		run(dungeons[2].(string), start+2000, true),
		run(dungeons[1].(string), start+2500, false),
	})
	admin.put("dps_history", []any{run(dungeons[1].(string), start+3000, true)})

	clears := bob.expect(200, "GET", "/api/v1/guild/questmaps", nil).body["current"].(map[string]any)["clears"].([]any)
	if len(clears) != 2 || clears[0].(map[string]any)["ign"] != "Bob" || clears[1].(map[string]any)["ign"] != "Hoster" {
		t.Fatalf("clears: %v", clears)
	}

	// stage 3 again, after stage 2 was cleared
	bob.expect(200, "PUT", "/api/v1/me/data/dps_history", map[string]any{"baseRevision": 1, "data": []any{
		run(dungeons[0].(string), start+1000, true), run(dungeons[2].(string), start+4000, true)}})
	clears = bob.expect(200, "GET", "/api/v1/guild/questmaps", nil).body["current"].(map[string]any)["clears"].([]any)
	if len(clears) != 3 {
		t.Fatalf("stage 3 should be cleared now: %v", clears)
	}
	bob.expect(400, "GET", "/api/v1/guild/questmaps?limit=0", nil)
}
