package api

import (
	"math"
	"testing"
	"time"
)

func TestGuildFacts(t *testing.T) {
	e := newEnv(t, nil)
	admin := e.newAdmin()
	bob := e.newUser(admin, "Bob", "203.0.113.2")
	carl := e.newUser(admin, "Carl", "203.0.113.3")
	dave := e.newUser(admin, "Dave", "203.0.113.4") // another guild

	now := time.Now().UnixMilli()
	day := int64(24 * 3600 * 1000)

	admin.put("profile", map[string]any{"ign": "Hoster", "guild": "Gobblers", "guildRank": 40})
	// Bob saw exaltation progress at a login yesterday (a second one a week ago is too old), and completed two ST
	// sets: one yesterday, one long ago
	bob.put("profile", map[string]any{"ign": "Bob", "guild": "Gobblers", "guildRank": 10,
		"exaltChanges": []map[string]int64{
			{"t": now - day, "classType": 798, "stat": 0, "before": 0, "after": 30},
			{"t": now - 7*day, "classType": 798, "stat": 1, "before": 0, "after": 75}},
		"stSetsDone": []int64{now - day, now - 30*day}})
	carl.put("profile", map[string]any{"ign": "Carl", "guild": "Gobblers", "guildRank": 10})
	dave.put("profile", map[string]any{"ign": "Dave", "guild": "Others", "guildRank": 40})

	// Bob: the Trickster died yesterday after gaining 300 fame (from 100 before the 3 days), the Knight gained 50,
	// the Wizard played nothing (an older death is not counted, no fame gained either)
	bob.put("characters", map[string]any{
		"1": map[string]any{"classType": 804, "alive": false, "lastPlayed": now - day,
			"samples": []map[string]int64{{"t": now - 5*day, "fame": 100}, {"t": now - 2*day, "fame": 250}, {"t": now - day, "fame": 400}}},
		"2": map[string]any{"classType": 798, "skin": 901, "tex1": 3, "alive": true, "lastPlayed": now,
			"samples": []map[string]int64{{"t": now - day, "fame": 1000}, {"t": now, "fame": 1050}}},
		"3": map[string]any{"classType": 782, "alive": true, "lastPlayed": now - day,
			"samples": []map[string]int64{{"t": now - day, "fame": 500}}},
	})
	// two completed Nests (the later one in a Priest look), a Nest not completed, a Mad Lab a week ago
	bob.put("dungeon_visits", []any{
		map[string]any{"dungeon": "The Nest", "start": now - 2*day, "durationMs": 600_000, "completed": true,
			"look": map[string]any{"classType": 798}, "dealt": []int{100, 250}, "hitDamage": []int{50, 80},
			"exalts": []map[string]int{{"classType": 798, "stat": 2, "before": 4, "after": 16}, {"classType": 798, "stat": 3, "before": 16, "after": 17}}},
		map[string]any{"dungeon": "The Nest", "start": now - day, "durationMs": 60_000, "completed": true,
			"look": map[string]any{"classType": 784, "skin": 77}},
		map[string]any{"dungeon": "The Nest", "start": now - day + 1000, "durationMs": 60_000, "completed": false},
		map[string]any{"dungeon": "Mad Lab", "start": now - 6*day, "durationMs": 600_000, "completed": true,
			"dealt": []int{9999}, "hitDamage": []int{9999}},
	})
	// the exaltation progress now: the Knight's stat 0 went on to 50 since the login
	bob.put("exalts", map[string][]int{"798": {50, 75, 16, 17, 0, 0, 0, 0}})
	// boss DPS: 1000 and 3000 at best in the two runs in the time; a guild hall visit and an old run do not count
	bob.put("dps_history", []any{
		map[string]any{"dungeon": "The Nest", "start": now - 2*day, "bosses": []any{
			map[string]any{"fightMs": 10_000, "players": []any{map[string]any{"damage": 10_000, "me": true}}},
			map[string]any{"fightMs": 10_000, "players": []any{map[string]any{"damage": 5_000, "me": true}, map[string]any{"damage": 90_000}}}}},
		map[string]any{"dungeon": "The Nest", "start": now - day, "bosses": []any{
			map[string]any{"fightMs": 2_000, "players": []any{map[string]any{"damage": 6_000, "me": true}}}}},
		map[string]any{"dungeon": "{s.guildhall}", "start": now - day, "guildHall": true, "bosses": []any{
			map[string]any{"fightMs": 1_000, "players": []any{map[string]any{"damage": 99_000, "me": true}}}}},
		map[string]any{"dungeon": "Mad Lab", "start": now - 6*day, "bosses": []any{
			map[string]any{"fightMs": 1_000, "players": []any{map[string]any{"damage": 99_000, "me": true}}}}},
	})
	// Bob found two items yesterday (two copies from one bag) and one a week ago
	recent := map[string]any{"at": now - day, "time": "2026-01-01T10:00:00", "source": "drop", "where": "The Nest", "bag": "White"}
	old := map[string]any{"at": now - 7*day, "time": "2025-12-25T10:00:00", "source": "drop", "where": "Mad Lab", "bag": "White"}
	bob.put("collection", map[string]any{
		"100": map[string]any{"count": 2, "history": []any{recent, recent}},
		"200": map[string]any{"count": 1, "history": []any{old}},
	})
	carl.put("characters", map[string]any{"1": map[string]any{"classType": 802, "alive": true}})
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
	if fav["dungeon"] != "The Nest" || fav["count"] != float64(2) || fav["classType"] != float64(784) || fav["skin"] != float64(77) {
		t.Fatalf("Bob's favorite: %v", fav)
	}
	if b["deaths"] != float64(1) || b["fame"] != float64(350) || b["played"] != float64(2) || b["dealt"] != float64(350) ||
		b["taken"] != float64(130) || b["hits"] != float64(2) || b["inMs"] != float64(720_000) || b["loot"] != float64(2) ||
		b["stSets"] != float64(1) || b["runs"] != float64(2) || math.Abs(b["peakDps"].(float64)-2000) > 0.01 {
		t.Fatalf("Bob: %v", b)
	}
	// exalts in progress points: stat 0 from 0 to 50 = 50, stat 2 from 4 to 16 = 12, stat 3 16 -> 17 = 1
	if b["exalts"] != float64(63) {
		t.Fatalf("Bob's exalts: %v", b["exalts"])
	}
	if l := b["look"].(map[string]any); l["classType"] != float64(798) || l["skin"] != float64(901) {
		t.Fatalf("Bob's look: %v", l)
	}
	// the Mad Lab a week ago does not count: two Nests are all Bob completed in the time, so he loves them
	if c := got["Carl"]; c["favorite"] != nil || c["deaths"] != float64(0) || c["stSets"] != float64(0) || c["runs"] != float64(0) {
		t.Fatalf("Carl: %v", c)
	}
	admin.expect(400, "GET", "/api/v1/guild/facts?days=99", nil)
}

func TestFavoriteOf(t *testing.T) {
	// 18 of 20 completed runs (90%) are Nests: loved
	if f := favoriteOf(map[string]int{"The Nest": 18, "Mad Lab": 2}); f == nil || f.Dungeon != "The Nest" || f.Count != 18 {
		t.Fatalf("90%%: %v", f)
	}
	// 17 of 20 (85%) is not more than 85%: none
	if f := favoriteOf(map[string]int{"The Nest": 17, "Mad Lab": 3}); f != nil {
		t.Fatalf("85%%: %v", f)
	}
	if f := favoriteOf(map[string]int{}); f != nil {
		t.Fatalf("none: %v", f)
	}
}

func TestCompletedFromRun(t *testing.T) {
	life := func(n int64) *int64 { return &n }
	run := func(maxHP int64, endHP *int64) *runFactsDoc {
		r := &runFactsDoc{Start: 7}
		r.Bosses = append(r.Bosses, struct {
			MaxHP   int64  `json:"maxHp"`
			EndHP   *int64 `json:"endHp"`
			FightMs int64  `json:"fightMs"`
			Players []struct {
				Damage int64 `json:"damage"`
				Me     bool  `json:"me"`
			} `json:"players"`
		}{MaxHP: maxHP, EndHP: endHP})
		return r
	}
	old := visitFactsDoc{RunStart: 7} // an older client: no completed mark
	if !completed(old, run(1000, life(50))) {
		t.Fatal("boss left at 5%: completed")
	}
	if completed(old, run(1000, life(600))) {
		t.Fatal("boss left at 60%: not completed")
	}
	if completed(visitFactsDoc{}, nil) || !completed(visitFactsDoc{Victory: true}, nil) {
		t.Fatal("no run: only a victory counts")
	}
	no := false
	if completed(visitFactsDoc{RunStart: 7, Completed: &no}, run(1000, life(0))) {
		t.Fatal("the client's own judgement wins")
	}
}

func TestBestFriends(t *testing.T) {
	members := []memberFacts{{UserID: 1, IGN: "Ann"}, {UserID: 2, IGN: "Bob"}, {UserID: 3, IGN: "Cid"}}
	nest := func(start, ms int64) visitFactsDoc { return visitFactsDoc{Dungeon: "The Nest", Start: start, DurationMs: ms} }
	visits := map[int64][]visitFactsDoc{
		1: {nest(0, 100), nest(200, 100), nest(400, 100)},
		2: {nest(50, 100), nest(250, 100), {Dungeon: "Mad Lab", Start: 400, DurationMs: 100}}, // with Ann twice
		3: {nest(410, 50)},                                                                      // with Ann once
	}
	f := bestFriendsOf(members, visits)
	if f == nil || f.A != 1 || f.B != 2 || f.Count != 2 {
		t.Fatalf("best friends: %+v", f)
	}
	if bestFriendsOf(members, map[int64][]visitFactsDoc{1: {nest(0, 10)}}) != nil {
		t.Fatal("nobody ran together")
	}
}
