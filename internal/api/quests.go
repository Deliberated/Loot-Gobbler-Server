package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"net/http"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Deliberated/Loot-Gobbler-Server/internal/store"
)

// Quests. Every week (Monday 00:00 UTC) each player rolls up to personalQuestsPerWeek personal quests from the
// accepted personal quests, and each guild gets guildQuestsPerWeek guild quests, the same for all its members.
// Progress is computed from the data players sync, counting only what happened during that week. A finished quest
// gives its points once: personal quests to the player, guild quests to every member who contributed. Points
// count per calendar month (UTC).
//
// Quests are checked whenever someone opens the quests or the leaderboard; the previous week is checked once more,
// so data synced shortly after a week ends still counts for it.

const (
	personalQuestsPerWeek = 3
	guildQuestsPerWeek    = 1
	maxPendingSuggestions = 10
	week                  = 7 * 24 * time.Hour
)

// questTypes are the kinds of quests the server can check, with a short description for generated titles.
var questTypes = map[string]string{
	"drops":          "Drop {n} UT/ST items",
	"new_items":      "Find {n} items that are new to your collection",
	"dungeon_clears": "Clear {n} dungeons",
	"boss_damage":    "Deal {n} damage to bosses",
	"fame":           "Earn {n} fame",
}

// weekStart is Monday 00:00 UTC of t's week.
func weekStart(t time.Time) time.Time {
	t = t.UTC()
	day := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
	return day.AddDate(0, 0, -((int(day.Weekday()) + 6) % 7))
}

func monthStart(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)
}

// --- what players did, from their synced documents ---

type runDoc struct {
	Dungeon   string `json:"dungeon"`
	Start     int64  `json:"start"`
	GuildHall bool   `json:"guildHall"`
	Bosses    []struct {
		Players []struct {
			Damage int64 `json:"damage"`
			Me     bool  `json:"me"`
		} `json:"players"`
	} `json:"bosses"`
}

type sampleDoc struct {
	T    int64 `json:"t"`
	Fame int64 `json:"fame"`
}

type playerActivity struct {
	collection collectionDoc
	drops      []itemDrop // from the drop log: kept when the collection is cleared
	runs       []runDoc
	samples    [][]sampleDoc // per character, oldest first
}

func (s *Server) activity(ctx context.Context, userID int64) (*playerActivity, error) {
	items, err := store.ListData(ctx, s.store.DB(), userID, []string{"collection", "dps_history", "characters"}, true)
	if err != nil {
		return nil, err
	}
	a := &playerActivity{}
	json.Unmarshal(items["collection"].Data, &a.collection)
	if a.drops, err = s.drops(ctx, userID); err != nil {
		return nil, err
	}
	json.Unmarshal(items["dps_history"].Data, &a.runs)
	var chars map[string]struct {
		Samples []sampleDoc `json:"samples"`
	}
	if json.Unmarshal(items["characters"].Data, &chars) == nil {
		for _, c := range chars {
			a.samples = append(a.samples, c.Samples)
		}
	}
	return a, nil
}

// findTime is when a find happened: its exact time if the client sent one, else its local time read as UTC.
func findTime(f findDoc) time.Time {
	if f.At > 0 {
		return time.UnixMilli(f.At)
	}
	t, err := time.Parse(findTimeLayout, f.Time)
	if err != nil {
		return time.Time{}
	}
	return t
}

func matches(name, part string) bool {
	return part == "" || strings.Contains(strings.ToLower(name), strings.ToLower(part))
}

// progress counts what the player did towards a quest between from and to.
func (a *playerActivity) progress(typ string, p store.QuestParams, from, to time.Time) int64 {
	in := func(t time.Time) bool { return !t.Before(from) && t.Before(to) }
	inMs := func(ms int64) bool { return ms >= from.UnixMilli() && ms < to.UnixMilli() }
	var n int64
	switch typ {
	case "drops":
		for _, d := range a.drops {
			f := d.findDoc
			enchants := f.Enchants
			if f.EnchantIDs != nil {
				enchants = len(f.EnchantIDs)
			}
			if in(findTime(f)) && matches(f.Where, p.Dungeon) && enchants >= p.MinEnchants &&
				(p.Bag == "" || strings.EqualFold(bagColor(f.Bag), p.Bag) || strings.EqualFold(f.Bag, p.Bag)) {
				n++
			}
		}
	case "new_items":
		for _, e := range a.collection {
			first := time.Time{}
			for _, f := range e.History {
				if t := findTime(f); !t.IsZero() && (first.IsZero() || t.Before(first)) {
					first = t
				}
			}
			if first.IsZero() {
				first, _ = time.Parse(findTimeLayout, e.First)
			}
			if in(first) {
				n++
			}
		}
	case "dungeon_clears":
		for _, r := range a.runs {
			if !r.GuildHall && inMs(r.Start) && matches(r.Dungeon, p.Dungeon) {
				n++
			}
		}
	case "boss_damage":
		for _, r := range a.runs {
			if r.GuildHall || !inMs(r.Start) || !matches(r.Dungeon, p.Dungeon) {
				continue
			}
			for _, b := range r.Bosses {
				for _, pl := range b.Players {
					if pl.Me {
						n += pl.Damage
					}
				}
			}
		}
	case "fame":
		// per character: highest fame in the week minus the fame before it (or at its first sample in the week)
		for _, samples := range a.samples {
			base, best, seen := int64(-1), int64(-1), false
			for _, sm := range samples {
				switch {
				case sm.T < from.UnixMilli():
					base = sm.Fame
				case sm.T < to.UnixMilli():
					if !seen && base < 0 {
						base = sm.Fame
					}
					seen = true
					best = max(best, sm.Fame)
				}
			}
			if seen && best > base {
				n += best - base
			}
		}
	}
	return n
}

// --- rolling and checking quests ---

// fillQuests gives the owner (a player, or a guild) of week up to want quests from the accepted ones of scope.
func fillQuests(ctx context.Context, q store.Querier, scope string, userID int64, guild string, weekMs int64, want int) error {
	var have []store.QuestAssignment
	var err error
	if scope == store.QuestPersonal {
		have, err = store.PersonalAssignments(ctx, q, userID, weekMs)
	} else {
		have, err = store.GuildAssignments(ctx, q, guild, weekMs)
	}
	if err != nil {
		return err
	}
	taken := map[int64]bool{}
	count := 0
	for _, a := range have {
		if a.WeekStart != weekMs {
			continue
		}
		count++
		if a.TemplateID != nil {
			taken[*a.TemplateID] = true
		}
	}
	if count >= want {
		return nil
	}
	pool, err := store.ListQuestTemplates(ctx, q, store.QuestApproved, scope)
	if err != nil {
		return err
	}
	rand.Shuffle(len(pool), func(i, j int) { pool[i], pool[j] = pool[j], pool[i] })
	for _, t := range pool {
		if count >= want {
			break
		}
		if taken[t.ID] {
			continue
		}
		a := store.QuestAssignment{WeekStart: weekMs, TemplateID: &t.ID, Title: t.Title, Type: t.Type, Params: t.Params, Points: t.Points}
		if scope == store.QuestPersonal {
			a.UserID = &userID
		} else {
			a.Guild = guild
		}
		if err := store.InsertAssignment(ctx, q, a); err != nil {
			return err
		}
		count++
	}
	return nil
}

type questContribution struct {
	IGN    string `json:"ign"`
	Amount int64  `json:"amount"`
}

type questView struct {
	ID           int64               `json:"id"`
	Title        string              `json:"title"`
	Type         string              `json:"type"`
	Params       store.QuestParams   `json:"params"`
	Points       int                 `json:"points"`
	Progress     int64               `json:"progress"`
	Target       int64               `json:"target"`
	CompletedAt  *int64              `json:"completedAt,omitempty"`
	Contributors []questContribution `json:"contributors,omitempty"` // guild quests, most first
}

// settledQuests is a player's quests of this week after checking them.
type settledQuests struct {
	personal []questView
	guild    []questView
}

// settle rolls missing quests and checks progress for a player and (if guild != "") their guild, this week and
// the previous one. activities holds the guild members' activity by user id (at least the player's).
func (s *Server) settle(ctx context.Context, userID int64, guild string, members []guildMember,
	activities map[int64]*playerActivity, now time.Time) (*settledQuests, error) {
	thisWeek := weekStart(now)
	lastWeek := thisWeek.Add(-week)
	guildKey := strings.ToLower(guild)
	var personal, guildQuests []store.QuestAssignment
	err := s.store.Tx(ctx, func(q store.Querier) error {
		if err := fillQuests(ctx, q, store.QuestPersonal, userID, "", thisWeek.UnixMilli(), personalQuestsPerWeek); err != nil {
			return err
		}
		var err error
		if personal, err = store.PersonalAssignments(ctx, q, userID, lastWeek.UnixMilli()); err != nil {
			return err
		}
		if guildKey == "" {
			return nil
		}
		if err := fillQuests(ctx, q, store.QuestGuild, 0, guildKey, thisWeek.UnixMilli(), guildQuestsPerWeek); err != nil {
			return err
		}
		guildQuests, err = store.GuildAssignments(ctx, q, guildKey, lastWeek.UnixMilli())
		return err
	})
	if err != nil {
		return nil, err
	}

	result := &settledQuests{personal: []questView{}, guild: []questView{}}
	for _, a := range personal {
		from := time.UnixMilli(a.WeekStart)
		v := view(a, activities[userID].progress(a.Type, a.Params, from, from.Add(week)))
		if err := s.complete(ctx, &v, a, now, []int64{userID}); err != nil {
			return nil, err
		}
		if a.WeekStart == thisWeek.UnixMilli() {
			result.personal = append(result.personal, v)
		}
	}
	for _, a := range guildQuests {
		from := time.UnixMilli(a.WeekStart)
		var total int64
		var contributors []questContribution
		var ids []int64
		for _, m := range members {
			act := activities[m.ID]
			if act == nil {
				continue
			}
			if n := act.progress(a.Type, a.Params, from, from.Add(week)); n > 0 {
				total += n
				contributors = append(contributors, questContribution{IGN: m.IGN, Amount: n})
				ids = append(ids, m.ID)
			}
		}
		v := view(a, total)
		sortContributions(contributors)
		v.Contributors = contributors
		if err := s.complete(ctx, &v, a, now, ids); err != nil {
			return nil, err
		}
		if a.WeekStart == thisWeek.UnixMilli() {
			result.guild = append(result.guild, v)
		}
	}
	return result, nil
}

func view(a store.QuestAssignment, progress int64) questView {
	return questView{ID: a.ID, Title: a.Title, Type: a.Type, Params: a.Params, Points: a.Points,
		Progress: min(progress, a.Params.Count), Target: a.Params.Count, CompletedAt: a.CompletedAt}
}

// complete marks a reached quest done and awards its points to players. Points count in the month the quest was
// done; a quest found done after its week ended counts on the week's last moment.
func (s *Server) complete(ctx context.Context, v *questView, a store.QuestAssignment, now time.Time, players []int64) error {
	if a.CompletedAt != nil || v.Progress < v.Target || v.Target <= 0 {
		return nil
	}
	at := min(now.UnixMilli(), a.WeekStart+week.Milliseconds()-1)
	return s.store.Tx(ctx, func(q store.Querier) error {
		done, err := store.CompleteAssignment(ctx, q, a.ID, at)
		if err != nil || !done {
			return err
		}
		for _, id := range players {
			if err := store.AwardPoints(ctx, q, a.ID, id, a.Points, at); err != nil {
				return err
			}
		}
		v.CompletedAt = &at
		s.log.Info("quest completed", "quest", a.Title, "points", a.Points, "players", len(players))
		return nil
	})
}

func sortContributions(c []questContribution) {
	sort.SliceStable(c, func(i, j int) bool { return c[i].Amount > c[j].Amount })
}

// guildActivities loads the activity of every member (and of the player, who may have no guild yet).
func (s *Server) guildActivities(ctx context.Context, userID int64, members []guildMember) (map[int64]*playerActivity, error) {
	acts := map[int64]*playerActivity{}
	ids := []int64{userID}
	for _, m := range members {
		ids = append(ids, m.ID)
	}
	for _, id := range ids {
		if acts[id] != nil {
			continue
		}
		a, err := s.activity(ctx, id)
		if err != nil {
			return nil, err
		}
		acts[id] = a
	}
	return acts, nil
}

// --- endpoints ---

type questsResponse struct {
	WeekStart   int64       `json:"weekStart"`
	WeekEnd     int64       `json:"weekEnd"`
	Personal    []questView `json:"personal"`
	Guild       string      `json:"guild,omitempty"` // empty while the player's guild is unknown
	GuildQuests []questView `json:"guildQuests"`
	Period      string      `json:"period"` // "2026-11": points count per month
	Points      int         `json:"points"` // the player's points this period
}

func (s *Server) getQuests(w http.ResponseWriter, r *http.Request) error {
	user, err := s.require(r, store.RoleUser)
	if err != nil {
		return err
	}
	ctx := r.Context()
	guild, members, err := s.guildOf(ctx, user)
	var conflict *apiError
	if err != nil && !(errors.As(err, &conflict) && conflict.Code == "no_guild") {
		return err
	}
	acts, err := s.guildActivities(ctx, user.ID, members)
	if err != nil {
		return err
	}
	now := s.now()
	settled, err := s.settle(ctx, user.ID, guild, members, acts, now)
	if err != nil {
		return err
	}
	month := monthStart(now)
	points, err := store.PointsBetween(ctx, s.store.DB(), month.UnixMilli(), month.AddDate(0, 1, 0).UnixMilli())
	if err != nil {
		return err
	}
	ws := weekStart(now)
	writeJSON(w, http.StatusOK, questsResponse{WeekStart: ws.UnixMilli(), WeekEnd: ws.Add(week).UnixMilli(),
		Personal: settled.personal, Guild: guild, GuildQuests: settled.guild, Period: month.Format("2006-01"),
		Points: points[user.ID]})
	return nil
}

// questTitle makes a title from the type and params when the suggestion has none.
func questTitle(typ string, p store.QuestParams, scope string) string {
	title := strings.ReplaceAll(questTypes[typ], "{n}", formatCount(p.Count))
	if p.Dungeon != "" && (typ == "drops" || typ == "dungeon_clears" || typ == "boss_damage") {
		title += " in " + p.Dungeon
	}
	if p.MinEnchants > 0 {
		title += fmt.Sprintf(" with %d+ enchantments", p.MinEnchants)
	}
	if p.Bag != "" {
		title += " from " + p.Bag + " bags"
	}
	if scope == store.QuestGuild {
		title = "Guild: " + strings.ToLower(title[:1]) + title[1:]
	}
	return title
}

func formatCount(n int64) string {
	s := fmt.Sprint(n)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}

type suggestion struct {
	Title  string            `json:"title"`
	Type   string            `json:"type"`
	Params store.QuestParams `json:"params"`
	Scope  string            `json:"scope"`
	Points int               `json:"points"`
}

func validScope(scope string) bool { return scope == store.QuestPersonal || scope == store.QuestGuild }

// suggestQuest adds a quest to the pool, waiting for an admin's decision.
func (s *Server) suggestQuest(w http.ResponseWriter, r *http.Request) error {
	user, err := s.require(r, store.RoleUser)
	if err != nil {
		return err
	}
	var req suggestion
	if err := decodeJSON(w, r, smallBody, &req, false); err != nil {
		return err
	}
	if _, ok := questTypes[req.Type]; !ok {
		return errBadRequest("type must be drops, new_items, dungeon_clears, boss_damage or fame")
	}
	if !validScope(req.Scope) {
		return errBadRequest("scope must be personal or guild")
	}
	p := req.Params
	p.Dungeon, p.Bag = strings.TrimSpace(p.Dungeon), strings.TrimSpace(p.Bag)
	if p.Count < 1 || p.Count > 1_000_000_000 || p.MinEnchants < 0 || p.MinEnchants > 4 ||
		utf8.RuneCountInString(p.Dungeon) > 40 || utf8.RuneCountInString(p.Bag) > 20 {
		return errBadRequest("count must be 1 or more, minEnchants 0 to 4, dungeon at most 40 and bag at most 20 characters")
	}
	title := strings.TrimSpace(req.Title)
	if title == "" {
		title = questTitle(req.Type, p, req.Scope)
	}
	if utf8.RuneCountInString(title) > 80 {
		return errBadRequest("title must be at most 80 characters")
	}
	points := req.Points
	if points <= 0 {
		points = 15
		if req.Scope == store.QuestGuild {
			points = 40
		}
	}
	points = min(points, 500)
	ctx := r.Context()
	var t store.QuestTemplate
	err = s.store.Tx(ctx, func(q store.Querier) error {
		n, err := store.PendingQuestsBy(ctx, q, user.ID)
		if err != nil {
			return err
		}
		if n >= maxPendingSuggestions {
			return errConflict("too_many_suggestions", "You already have 10 suggestions waiting for a decision")
		}
		t = store.QuestTemplate{Title: title, Type: req.Type, Params: p, Scope: req.Scope, Points: points,
			Status: store.QuestPending, ProposedBy: user.IGN, CreatedAt: s.nowMs()}
		t.ID, err = store.InsertQuestTemplate(ctx, q, t, &user.ID, t.CreatedAt)
		return err
	})
	if err != nil {
		return err
	}
	s.log.Info("quest suggested", "ign", user.IGN, "quest", title, "scope", req.Scope)
	writeJSON(w, http.StatusCreated, t)
	return nil
}

type questPoolResponse struct {
	Quests []store.QuestTemplate `json:"quests"`
}

func (s *Server) listQuestPool(w http.ResponseWriter, r *http.Request) error {
	if _, err := s.require(r, store.RoleAdmin); err != nil {
		return err
	}
	status := r.URL.Query().Get("status")
	if status != "" && status != store.QuestPending && status != store.QuestApproved && status != store.QuestDenied {
		return errBadRequest("status must be pending, approved or denied")
	}
	list, err := store.ListQuestTemplates(r.Context(), s.store.DB(), status, "")
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, questPoolResponse{list})
	return nil
}

// approveQuest accepts a quest into the pool as a personal or guild quest, optionally with other points.
func (s *Server) approveQuest(w http.ResponseWriter, r *http.Request) error {
	admin, err := s.require(r, store.RoleAdmin)
	if err != nil {
		return err
	}
	id, err := pathID(r)
	if err != nil {
		return err
	}
	var req struct {
		Scope  string `json:"scope"`
		Points int    `json:"points"`
	}
	if err := decodeJSON(w, r, smallBody, &req, false); err != nil {
		return err
	}
	if !validScope(req.Scope) {
		return errBadRequest("scope must be personal or guild")
	}
	if req.Points < 0 || req.Points > 500 {
		return errBadRequest("points must be between 1 and 500")
	}
	ok, err := store.DecideQuestTemplate(r.Context(), s.store.DB(), id, store.QuestApproved, req.Scope, req.Points, admin.ID, s.nowMs())
	if err != nil {
		return err
	}
	if !ok {
		return errNotFound("No such quest")
	}
	s.log.Info("quest accepted", "id", id, "scope", req.Scope, "by", admin.IGN)
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// denyQuest rejects a suggestion, or takes an accepted quest out of the pool (running quests keep going).
func (s *Server) denyQuest(w http.ResponseWriter, r *http.Request) error {
	admin, err := s.require(r, store.RoleAdmin)
	if err != nil {
		return err
	}
	id, err := pathID(r)
	if err != nil {
		return err
	}
	ok, err := store.DecideQuestTemplate(r.Context(), s.store.DB(), id, store.QuestDenied, "", 0, admin.ID, s.nowMs())
	if err != nil {
		return err
	}
	if !ok {
		return errNotFound("No such quest")
	}
	s.log.Info("quest denied", "id", id, "by", admin.IGN)
	w.WriteHeader(http.StatusNoContent)
	return nil
}
