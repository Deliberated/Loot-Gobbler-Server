package api

import (
	"testing"
	"time"
)

func TestWeekStart(t *testing.T) {
	wed := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	sun := time.Date(2026, 11, 8, 23, 59, 0, 0, time.UTC)
	mon := time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC)
	if !weekStart(wed).Equal(mon) || !weekStart(sun).Equal(mon) || !weekStart(mon).Equal(mon) {
		t.Fatalf("week of %v is %v", wed, weekStart(wed))
	}
}

func TestQuests(t *testing.T) {
	e := newEnv(t, nil)
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC) // a Wednesday; the week started Monday 2 Nov
	e.srv.now = func() time.Time { return now }
	admin := e.newAdmin()
	bob := e.newUser(admin, "Bob", "203.0.113.2")
	carl := e.newUser(admin, "Carl", "203.0.113.3")
	for _, c := range []*client{admin, bob, carl} {
		c.put("profile", map[string]any{"guild": "Gobblers"})
	}

	// nothing accepted yet: no quests
	q := bob.expect(200, "GET", "/api/v1/quests", nil).body
	if len(q["personal"].([]any)) != 0 || len(q["guildQuests"].([]any)) != 0 || q["guild"] != "Gobblers" {
		t.Fatalf("empty pool: %v", q)
	}

	// suggestions: members suggest, only admins see and decide
	fame := bob.expect(201, "POST", "/api/v1/quests/suggestions", map[string]any{
		"type": "fame", "params": map[string]any{"count": 100}, "scope": "personal"}).body
	if fame["title"] != "Earn 100 fame" || fame["status"] != "pending" || fame["proposedBy"] != "Bob" {
		t.Fatalf("suggestion: %v", fame)
	}
	guildDrops := bob.expect(201, "POST", "/api/v1/quests/suggestions", map[string]any{
		"type": "drops", "params": map[string]any{"count": 4}, "scope": "guild"}).body
	bob.expect(400, "POST", "/api/v1/quests/suggestions", map[string]any{"type": "fly", "params": map[string]any{"count": 1}, "scope": "personal"})
	bob.expect(403, "GET", "/api/v1/admin/quests", nil)

	pool := admin.expect(200, "GET", "/api/v1/admin/quests?status=pending", nil).body["quests"].([]any)
	byTitle := map[string]string{}
	for _, p := range pool {
		m := p.(map[string]any)
		byTitle[m["title"].(string)] = itoa(int64(m["id"].(float64)))
	}
	if len(pool) != 14 { // 12 starters + Bob's 2
		t.Fatalf("pool: %d", len(pool))
	}
	// exactly three personal quests, so everyone gets all three
	admin.expect(204, "POST", "/api/v1/admin/quests/"+byTitle["Drop 3 UT/ST items"]+"/approve", map[string]any{"scope": "personal"})
	admin.expect(204, "POST", "/api/v1/admin/quests/"+byTitle["Clear 3 Voids"]+"/approve", map[string]any{"scope": "personal"})
	admin.expect(204, "POST", "/api/v1/admin/quests/"+itoa(int64(fame["id"].(float64)))+"/approve", map[string]any{"scope": "personal", "points": 20})
	admin.expect(204, "POST", "/api/v1/admin/quests/"+itoa(int64(guildDrops["id"].(float64)))+"/approve", map[string]any{"scope": "guild"})
	admin.expect(204, "POST", "/api/v1/admin/quests/"+byTitle["Earn 2,000 fame"]+"/deny", nil)
	bob.expect(403, "POST", "/api/v1/admin/quests/"+byTitle["Clear 5 dungeons"]+"/approve", map[string]any{"scope": "personal"})

	// what Bob and Carl did: this week counts, last week does not
	ms := func(d time.Duration) int64 { return now.Add(d).UnixMilli() }
	drop := func(at int64, where string) map[string]any {
		return map[string]any{"time": "2000-01-01T00:00:00", "at": at, "source": "drop", "where": where, "bag": "White"}
	}
	bob.put("collection", map[string]any{
		"100": map[string]any{"count": 3, "history": []any{drop(ms(-time.Hour), "Void"), drop(ms(-2*time.Hour), "Void"), drop(ms(-9*24*time.Hour), "Void")}},
		"200": map[string]any{"count": 1, "history": []any{drop(ms(-3*time.Hour), "Shatters")}},
	})
	carl.put("collection", map[string]any{"300": map[string]any{"count": 1, "history": []any{drop(ms(-time.Hour), "Lost Halls")}}})
	bob.put("dps_history", []any{
		map[string]any{"dungeon": "The Void", "start": ms(-time.Hour), "bosses": []any{}},
		map[string]any{"dungeon": "The Void", "start": ms(-2 * time.Hour), "bosses": []any{}},
		map[string]any{"dungeon": "Guild Hall", "start": ms(-time.Hour), "guildHall": true, "bosses": []any{}},
	})
	bob.put("characters", map[string]any{"1": map[string]any{"samples": []any{
		map[string]any{"t": ms(-5 * 24 * time.Hour), "fame": 1000}, // before the week: the base
		map[string]any{"t": ms(-time.Hour), "fame": 1150},
	}}})

	q = bob.expect(200, "GET", "/api/v1/quests", nil).body
	got := map[string]map[string]any{}
	for _, v := range q["personal"].([]any) {
		m := v.(map[string]any)
		got[m["title"].(string)] = m
	}
	if len(got) != 3 {
		t.Fatalf("Bob's personal quests: %v", q["personal"])
	}
	if d := got["Drop 3 UT/ST items"]; d["progress"] != 3.0 || d["completedAt"] == nil {
		t.Fatalf("drops: %v", d)
	}
	if v := got["Clear 3 Voids"]; v["progress"] != 2.0 || v["completedAt"] != nil { // the guild hall run does not count
		t.Fatalf("voids: %v", v)
	}
	if f := got["Earn 100 fame"]; f["progress"] != 100.0 || f["completedAt"] == nil || f["points"] != 20.0 {
		t.Fatalf("fame: %v", f)
	}
	g := q["guildQuests"].([]any)[0].(map[string]any)
	contributors := g["contributors"].([]any)
	if g["progress"] != 4.0 || g["completedAt"] == nil || len(contributors) != 2 ||
		contributors[0].(map[string]any)["ign"] != "Bob" || contributors[0].(map[string]any)["amount"] != 3.0 {
		t.Fatalf("guild quest: %v", g)
	}
	if q["points"] != 70.0 || q["period"] != "2026-11" { // 10 + 20 personal + 40 guild
		t.Fatalf("Bob's points: %v", q["points"])
	}

	// checking again awards nothing twice; Carl got the guild quest's points
	if p := bob.expect(200, "GET", "/api/v1/quests", nil).body["points"]; p != 70.0 {
		t.Fatalf("points changed on a second look: %v", p)
	}
	if p := carl.expect(200, "GET", "/api/v1/quests", nil).body["points"]; p != 40.0 {
		t.Fatalf("Carl's points: %v", p)
	}
	board := admin.expect(200, "GET", "/api/v1/guild/leaderboard", nil).body["members"].([]any)
	for _, row := range board {
		r := row.(map[string]any)
		if want := map[string]float64{"Bob": 70, "Carl": 40, "Hoster": 0}[r["ign"].(string)]; r["points"] != want {
			t.Fatalf("leaderboard points of %v: %v, want %v", r["ign"], r["points"], want)
		}
	}

	// next week: new quests, last week's drops no longer count; a removed quest is not rolled again
	admin.expect(204, "POST", "/api/v1/admin/quests/"+byTitle["Clear 3 Voids"]+"/deny", nil)
	now = now.Add(7 * 24 * time.Hour)
	q = bob.expect(200, "GET", "/api/v1/quests", nil).body
	if len(q["personal"].([]any)) != 2 {
		t.Fatalf("next week: %v", q["personal"])
	}
	for _, v := range q["personal"].([]any) {
		if m := v.(map[string]any); m["progress"] != 0.0 || m["title"] == "Clear 3 Voids" {
			t.Fatalf("next week quest: %v", m)
		}
	}
	if q["points"] != 70.0 {
		t.Fatalf("points are per month: %v", q["points"])
	}
}

func TestQuestsWithoutGuild(t *testing.T) {
	e := newEnv(t, nil)
	admin := e.newAdmin()
	pool := admin.expect(200, "GET", "/api/v1/admin/quests", nil).body["quests"].([]any)
	for _, p := range pool {
		m := p.(map[string]any)
		if m["scope"] == "personal" {
			admin.expect(204, "POST", "/api/v1/admin/quests/"+itoa(int64(m["id"].(float64)))+"/approve", map[string]any{"scope": "personal"})
		}
	}
	q := admin.expect(200, "GET", "/api/v1/quests", nil).body // no profile: personal quests still work
	if len(q["personal"].([]any)) != 3 || q["guild"] != nil {
		t.Fatalf("without guild: %v", q)
	}
}
