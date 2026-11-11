package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Deliberated/Loot-Gobbler-Server/internal/store"
)

// Guild facts: numbers about every guildmate (the requester too) over the last few days (3 unless asked otherwise),
// which the clients rank to show who stands out: the dungeon they love (more than 85% of their completed runs; with
// the look they had in the latest of those runs), characters that died, fame gained, damage dealt and taken in dungeons and the time and hits there,
// their best boss DPS per run, exaltations earned, items found, characters that gained fame, ST sets completed, and the
// dungeon visit they gained the most fame in.
// With each member the look of the character they played last. What the clients make of it is theirs.

const (
	defaultFactDays = 3
	maxFactDays     = 14
)

// characterFactsDoc is the part of a synced character read here.
type characterFactsDoc struct {
	ClassType  int   `json:"classType"`
	Skin       int   `json:"skin"`
	Tex1       int   `json:"tex1"`
	Tex2       int   `json:"tex2"`
	Alive      bool  `json:"alive"`
	LastPlayed int64 `json:"lastPlayed"`
	Samples    []struct {
		T    int64 `json:"t"`
		Fame int64 `json:"fame"`
	} `json:"samples"` // fame over time
}

// exaltChangeDoc is one stat's exaltation progress going up (in a visit, or as the client saw it at login).
type exaltChangeDoc struct {
	T         int64 `json:"t"` // only in the profile's list; a visit's changes take the visit's start
	ClassType int   `json:"classType"`
	Stat      int   `json:"stat"`
	Before    int   `json:"before"`
	After     int   `json:"after"`
}

// visitFactsDoc is the part of a synced dungeon visit read here.
type visitFactsDoc struct {
	Dungeon    string           `json:"dungeon"`
	Start      int64            `json:"start"`
	DurationMs int64            `json:"durationMs"`
	Completed  *bool            `json:"completed"` // as the client judged it; nil from older clients
	RunStart   int64            `json:"runStart"`  // the DPS run of it (its start), 0 = no boss fought
	Victory    bool             `json:"victory"`   // the game said "Victory"
	Look       *lookDoc         `json:"look"`
	Dealt      []int64          `json:"dealt"`     // damage dealt to enemies, per second
	HitDamage  []int64          `json:"hitDamage"` // every hit taken
	Exalts     []exaltChangeDoc `json:"exalts"`
}

// profileFactsDoc is the part of the synced profile read here.
type profileFactsDoc struct {
	ExaltChanges []exaltChangeDoc `json:"exaltChanges"` // the last week's, as the client saw them
	StSetsDone   []int64          `json:"stSetsDone"`   // when each ST set was completed (its last piece found)
}

// runFactsDoc is the part of a synced DPS run read here.
type runFactsDoc struct {
	Start     int64 `json:"start"`
	GuildHall bool  `json:"guildHall"`
	Bosses    []struct {
		MaxHP   int64 `json:"maxHp"`
		EndHP   *int64 `json:"endHp"` // -1 or missing in older runs
		FightMs int64 `json:"fightMs"`
		Players []struct {
			Damage int64 `json:"damage"`
			Me     bool  `json:"me"`
		} `json:"players"`
	} `json:"bosses"`
}

type favoriteDungeon struct {
	lookDoc
	Dungeon string `json:"dungeon"`
	Count   int    `json:"count"` // runs of it completed
}

type memberFacts struct {
	UserID   int64            `json:"userId"`
	IGN      string           `json:"ign"`
	Favorite *favoriteDungeon `json:"favorite,omitempty"` // their most completed dungeon; none without completed runs
	Deaths   int              `json:"deaths"`             // characters that died
	Fame     int64            `json:"fame"`               // fame gained by all their characters
	Dealt    int64            `json:"dealt"`              // damage dealt in dungeons
	Taken    int64            `json:"taken"`              // damage taken in dungeons
	Hits     int              `json:"hits"`               // hits taken in dungeons ...
	InMs     int64            `json:"inMs"`               // ... in this much time spent in them
	PeakDps  float64          `json:"peakDps"`            // their best boss DPS per run, on average ...
	Runs     int              `json:"runs"`               // ... over this many runs
	Exalts   int              `json:"exalts"`             // exaltations earned
	Look     *lookDoc         `json:"look,omitempty"`     // the character they played last
	Loot     int              `json:"loot"`               // items they found (their drop log)
	Played   int              `json:"played"`             // characters that gained fame
	StSets   int              `json:"stSets"`             // ST sets completed
	BestRun  *bestRun         `json:"bestRun,omitempty"`  // the dungeon visit they gained the most fame in; none without fame gained in one
}

// bestRun is the dungeon visit a member's character gained the most fame in.
type bestRun struct {
	Dungeon string `json:"dungeon"`
	Fame    int64  `json:"fame"`
}

// bestFriends is the pair of members who did the most dungeon runs together.
type bestFriends struct {
	A     int64 `json:"a"` // user ids (A's name comes first by name)
	B     int64 `json:"b"`
	Count int   `json:"count"`
}

type factsResponse struct {
	Days    int           `json:"days"`
	Members []memberFacts `json:"members"`
	Friends *bestFriends  `json:"friends,omitempty"` // none if no two members ran a dungeon together
}

// guildFacts answers GET /api/v1/guild/facts?days=3.
func (s *Server) guildFacts(w http.ResponseWriter, r *http.Request) error {
	user, err := s.require(r, store.RoleUser)
	if err != nil {
		return err
	}
	days := defaultFactDays
	if v := r.URL.Query().Get("days"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > maxFactDays {
			return errBadRequest("days must be between 1 and 14")
		}
		days = n
	}
	ctx := r.Context()
	_, members, err := s.guildOf(ctx, user)
	if err != nil {
		return err
	}
	since := s.now().Add(-time.Duration(days) * 24 * time.Hour).UnixMilli()
	resp := factsResponse{Days: days, Members: []memberFacts{}}
	visitsOf := map[int64][]visitFactsDoc{} // each member's visits in the time, for the best friends
	for _, m := range members {
		items, err := store.ListData(ctx, s.store.DB(), m.ID,
			[]string{"characters", "dungeon_visits", "profile", "dps_history", "exalts"}, true)
		if err != nil {
			return err
		}
		f := memberFacts{UserID: m.ID, IGN: m.IGN}

		var chars map[string]characterFactsDoc
		if json.Unmarshal(items["characters"].Data, &chars) == nil {
			var last int64 = -1
			for _, c := range chars {
				if c.ClassType > 0 && c.LastPlayed > last {
					last = c.LastPlayed
					f.Look = &lookDoc{Player: m.IGN, ClassType: c.ClassType, Skin: c.Skin, Tex1: c.Tex1, Tex2: c.Tex2}
				}
				if !c.Alive && c.LastPlayed >= since { // a character dies while played: its last play is its death
					f.Deaths++
				}
				gained := fameSince(c, since)
				if gained > 0 {
					f.Played++
				}
				f.Fame += gained
			}
		}

		var runs []runFactsDoc
		_ = json.Unmarshal(items["dps_history"].Data, &runs)
		runByStart := map[int64]*runFactsDoc{}
		for i := range runs {
			runByStart[runs[i].Start] = &runs[i]
		}

		var visits []visitFactsDoc
		var changes []exaltChangeDoc // exaltation progress in the time, from the visits and the profile
		if json.Unmarshal(items["dungeon_visits"].Data, &visits) == nil {
			done := map[string]int{}
			looks := map[string]*lookDoc{} // the look in the latest completed run of each dungeon
			latest := map[string]int64{}
			for _, v := range visits {
				if v.Start < since {
					continue
				}
				for _, d := range v.Dealt {
					f.Dealt += d
				}
				for _, d := range v.HitDamage {
					f.Taken += d
				}
				f.Hits += len(v.HitDamage)
				f.InMs += max(0, v.DurationMs)
				for _, e := range v.Exalts {
					e.T = v.Start
					changes = append(changes, e)
				}
				if v.Dungeon != "" {
					if gained := fameGainedIn(v, chars); gained > 0 && (f.BestRun == nil || gained > f.BestRun.Fame) {
						f.BestRun = &bestRun{Dungeon: v.Dungeon, Fame: gained}
					}
				}
				if completed(v, runByStart[v.RunStart]) && v.Dungeon != "" {
					done[v.Dungeon]++
					if v.Look != nil && v.Start >= latest[v.Dungeon] {
						latest[v.Dungeon], looks[v.Dungeon] = v.Start, v.Look
					}
				}
			}
			if fav := favoriteOf(done); fav != nil {
				if l := looks[fav.Dungeon]; l != nil {
					fav.lookDoc = *l
				}
				fav.Player = m.IGN
				f.Favorite = fav
			}
		}

		var prof profileFactsDoc
		if json.Unmarshal(items["profile"].Data, &prof) == nil {
			for _, c := range prof.ExaltChanges {
				if c.T >= since {
					changes = append(changes, c)
				}
			}
			for _, t := range prof.StSetsDone {
				if t >= since {
					f.StSets++
				}
			}
		}
		var progress map[string][]int
		_ = json.Unmarshal(items["exalts"].Data, &progress)
		f.Exalts = exaltsEarned(changes, progress)

		{
			var sum float64
			for _, r := range runs {
				if r.Start < since || r.GuildHall {
					continue
				}
				if best := bestBossDps(r); best > 0 {
					sum += best
					f.Runs++
				}
			}
			if f.Runs > 0 {
				f.PeakDps = sum / float64(f.Runs)
			}
		}

		logged, err := s.drops(ctx, m.ID)
		if err != nil {
			return err
		}
		for _, d := range logged {
			if t := findTime(d.findDoc); !t.IsZero() && t.UnixMilli() >= since {
				f.Loot++
			}
		}
		for _, v := range visits {
			if v.Start >= since && v.Dungeon != "" {
				visitsOf[m.ID] = append(visitsOf[m.ID], v)
			}
		}
		resp.Members = append(resp.Members, f)
	}
	sort.SliceStable(resp.Members, func(i, j int) bool { return resp.Members[i].IGN < resp.Members[j].IGN })
	resp.Friends = bestFriendsOf(resp.Members, visitsOf)
	writeJSON(w, http.StatusOK, resp)
	return nil
}

// loverShare is how much of their completed runs one dungeon must be for a member to love it.
const loverShare = 0.85

// bestFriendsOf is the pair of members with the most dungeon runs together (a visit of one overlapping a visit of the
// other to the same dungeon), ties going to the first pair by name; nil if no two members ran one together.
func bestFriendsOf(members []memberFacts, visitsOf map[int64][]visitFactsDoc) *bestFriends {
	var best *bestFriends
	for i := 0; i < len(members); i++ { // members are in name order
		for j := i + 1; j < len(members); j++ {
			n := together(visitsOf[members[i].UserID], visitsOf[members[j].UserID])
			if n > 0 && (best == nil || n > best.Count) {
				best = &bestFriends{A: members[i].UserID, B: members[j].UserID, Count: n}
			}
		}
	}
	return best
}

// together counts the runs of a that b was in too: same dungeon, at overlapping times.
func together(a, b []visitFactsDoc) int {
	n := 0
	for _, x := range a {
		for _, y := range b {
			if strings.EqualFold(x.Dungeon, y.Dungeon) && x.Start < y.Start+max(y.DurationMs, 1) && y.Start < x.Start+max(x.DurationMs, 1) {
				n++
				break
			}
		}
	}
	return n
}

// completed tells whether a visit was completed: as its client judged it (newer clients), else as the client would:
// the game said "Victory", or the run's main boss (the one with the most life) was left at 8% of its life or less;
// for runs that did not keep that, the damage everyone did to it reached 88% of its life.
func completed(v visitFactsDoc, run *runFactsDoc) bool {
	if v.Completed != nil {
		return *v.Completed
	}
	if v.Victory {
		return true
	}
	if run == nil || v.RunStart == 0 {
		return false
	}
	main := -1
	for i, b := range run.Bosses {
		if main < 0 || b.MaxHP > run.Bosses[main].MaxHP {
			main = i
		}
	}
	if main < 0 || run.Bosses[main].MaxHP <= 0 {
		return false
	}
	b := run.Bosses[main]
	if b.EndHP != nil && *b.EndHP >= 0 {
		return float64(*b.EndHP) <= 0.08*float64(b.MaxHP)
	}
	var damage int64
	for _, p := range b.Players {
		damage += p.Damage
	}
	return len(b.Players) <= 15 && float64(damage) >= 0.88*float64(b.MaxHP)
}

// favoriteOf is the dungeon the member loves: the one completed most, if it is more than 85% of all their completed
// runs (ties: by name, so it does not flicker); nil if there is none.
func favoriteOf(done map[string]int) *favoriteDungeon {
	var f *favoriteDungeon
	total := 0
	for d, n := range done {
		total += n
		if n > 0 && (f == nil || n > f.Count || n == f.Count && d < f.Dungeon) {
			f = &favoriteDungeon{Dungeon: d, Count: n}
		}
	}
	if f == nil || float64(f.Count) <= loverShare*float64(total) {
		return nil
	}
	return f
}

// bestBossDps is the player's best DPS on a boss in the run, as the DPS logger shows it: their damage over the fight.
func bestBossDps(r runFactsDoc) float64 {
	var best float64
	for _, b := range r.Bosses {
		for _, p := range b.Players {
			if p.Me {
				best = max(best, float64(p.Damage)/max(1, float64(b.FightMs)/1000))
			}
		}
	}
	return best
}

// exaltsEarned counts the exaltation progress the changes made, in points as the game shows them (each boss kill or
// potion that counts): per class and stat, from the lowest progress it went up from to where it is now (or the
// highest it was seen going to), up to the last tier (75: nothing counts beyond it).
func exaltsEarned(changes []exaltChangeDoc, progress map[string][]int) int {
	type key struct{ class, stat int }
	from, to := map[key]int{}, map[key]int{}
	for _, c := range changes {
		k := key{c.ClassType, c.Stat}
		if b, ok := from[k]; !ok || c.Before < b {
			from[k] = c.Before
		}
		to[k] = max(to[k], c.After)
	}
	n := 0
	for k, b := range from {
		now := to[k]
		if p := progress[fmt.Sprint(k.class)]; k.stat >= 0 && k.stat < len(p) {
			now = max(now, p[k.stat])
		}
		top := exaltThresholds[len(exaltThresholds)-1]
		n += max(0, min(now, top)-min(b, top))
	}
	return n
}

// fameGainedIn is the fame gained in a dungeon visit, as the client counts it: the most any one character's fame went
// up between entering and leaving (its fame samples, see fameIn), 0 if none did.
func fameGainedIn(v visitFactsDoc, chars map[string]characterFactsDoc) int64 {
	end := v.Start + max(0, v.DurationMs)
	var best int64
	for _, c := range chars {
		if gained, ok := fameIn(c, v.Start, end); ok {
			best = max(best, gained)
		}
	}
	return best
}

// entrySampleMs is how old a sample from before a visit may be to still count as the fame the character entered with.
const entrySampleMs = 15_000

// fameIn is the fame a character gained between start and end. It starts from the last sample before start only if
// that was taken just before (samples come every few seconds while fame changes); otherwise from the first sample
// inside. Fame that went up while nothing was sampled shows up in the next sample and must not count for this visit.
// ok is false when the samples do not show the start.
func fameIn(c characterFactsDoc, start, end int64) (gained int64, ok bool) {
	var a, b int64
	haveA, haveB := false, false
	for _, s := range c.Samples { // in time order
		switch {
		case s.T <= start:
			a, haveA = s.Fame, start-s.T <= entrySampleMs
		case s.T <= end:
			if !haveA {
				a, haveA = s.Fame, true
			}
			b, haveB = s.Fame, true
		}
		if s.T > end {
			break
		}
	}
	if !haveA {
		return 0, false
	}
	if !haveB {
		return 0, true
	}
	return max(0, b-a), true
}

// fameSince is the fame the character gained since then: its latest fame against the last one before (or, without
// one, its first in the time).
func fameSince(c characterFactsDoc, since int64) int64 {
	base, last, seen := int64(-1), int64(-1), false
	for _, s := range c.Samples { // in time order
		if s.T < since {
			base = s.Fame
			continue
		}
		if !seen && base < 0 {
			base = s.Fame
		}
		seen = true
		last = s.Fame
	}
	if !seen || last <= base {
		return 0
	}
	return last - base
}
