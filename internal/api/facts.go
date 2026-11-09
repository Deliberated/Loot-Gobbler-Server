package api

import (
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/Deliberated/Loot-Gobbler-Server/internal/store"
)

// Guild facts: numbers about every guildmate (the requester too) that the clients rank to show who stands out. Their
// favourite dungeon, the one they completed most, comes from the game's own per-character completion counts over all
// their characters (also the dead ones, as their client last saw them), with the look of the character that completed
// it most. The rest covers the last few days (3 unless asked otherwise): characters that died, fame gained, damage
// dealt and taken in dungeons, exaltations earned there, the hits taken for the time spent in them (how well they
// dodged), the items they found and how many different characters they played. With each member the look of the character they played last. What the clients make of it (the guild's
// biggest dungeon lover, ...) is theirs.

const (
	defaultFactDays = 3
	maxFactDays     = 14
)

// characterFactsDoc is the part of a synced character read here.
type characterFactsDoc struct {
	ClassType  int            `json:"classType"`
	Skin       int            `json:"skin"`
	Tex1       int            `json:"tex1"`
	Tex2       int            `json:"tex2"`
	Alive      bool           `json:"alive"`
	LastPlayed int64          `json:"lastPlayed"`
	Dungeons   map[string]int `json:"dungeons"` // dungeon name -> times completed with this character
	Samples    []struct {
		T    int64 `json:"t"`
		Fame int64 `json:"fame"`
	} `json:"samples"` // fame over time
}

// visitFactsDoc is the part of a synced dungeon visit read here.
type visitFactsDoc struct {
	Start      int64   `json:"start"`
	DurationMs int64   `json:"durationMs"`
	Dealt      []int64 `json:"dealt"`     // damage dealt to enemies, per second
	HitDamage  []int64 `json:"hitDamage"` // every hit taken
	Exalts     []struct {
		Before int `json:"before"`
		After  int `json:"after"`
	} `json:"exalts"` // exaltation progress the game reported in it
}

type favoriteDungeon struct {
	lookDoc
	Dungeon string `json:"dungeon"`
	Count   int    `json:"count"` // over all their characters
}

type memberFacts struct {
	UserID   int64            `json:"userId"`
	IGN      string           `json:"ign"`
	Favorite *favoriteDungeon `json:"favorite,omitempty"` // none from clients that do not send completion counts
	Deaths   int              `json:"deaths"`             // characters that died in the days asked
	Fame     int64            `json:"fame"`               // fame gained by all their characters
	Dealt    int64            `json:"dealt"`              // damage dealt in dungeons
	Taken    int64            `json:"taken"`              // damage taken in dungeons
	Hits     int              `json:"hits"`               // hits taken in dungeons ...
	InMs     int64            `json:"inMs"`               // ... in this much time spent in them
	Exalts   int              `json:"exalts"`             // exaltations earned in dungeons
	Look     *lookDoc         `json:"look,omitempty"`     // the character they played last
	Loot     int              `json:"loot"`               // items they found (their drop log)
	Played   int              `json:"played"`             // different characters they played
}

type factsResponse struct {
	Days    int           `json:"days"`
	Members []memberFacts `json:"members"`
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
	for _, m := range members {
		items, err := store.ListData(ctx, s.store.DB(), m.ID, []string{"characters", "dungeon_visits"}, true)
		if err != nil {
			return err
		}
		f := memberFacts{UserID: m.ID, IGN: m.IGN}
		var chars map[string]characterFactsDoc
		if json.Unmarshal(items["characters"].Data, &chars) == nil {
			if fav, ok := favoriteOf(chars); ok {
				fav.Player = m.IGN
				f.Favorite = &fav
			}
			var last int64 = -1
			for _, c := range chars {
				if c.ClassType > 0 && c.LastPlayed > last {
					last = c.LastPlayed
					f.Look = &lookDoc{Player: m.IGN, ClassType: c.ClassType, Skin: c.Skin, Tex1: c.Tex1, Tex2: c.Tex2}
				}
				if c.LastPlayed >= since {
					f.Played++
				}
				if !c.Alive && c.LastPlayed >= since { // a character dies while played: its last play is its death
					f.Deaths++
				}
				f.Fame += fameSince(c, since)
			}
		}
		var visits []visitFactsDoc
		if json.Unmarshal(items["dungeon_visits"].Data, &visits) == nil {
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
					f.Exalts += exaltLevel(e.After) - exaltLevel(e.Before)
				}
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
		resp.Members = append(resp.Members, f)
	}
	sort.SliceStable(resp.Members, func(i, j int) bool { return resp.Members[i].IGN < resp.Members[j].IGN })
	writeJSON(w, http.StatusOK, resp)
	return nil
}

// exaltLevel is how many exaltations that much progress of one stat is.
func exaltLevel(progress int) int {
	n := 0
	for _, t := range exaltThresholds {
		if progress >= t {
			n++
		}
	}
	return n
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

// favoriteOf is the dungeon completed most over all the characters, with the look of the one that did it most.
func favoriteOf(chars map[string]characterFactsDoc) (favoriteDungeon, bool) {
	totals := map[string]int{}
	for _, c := range chars {
		for d, n := range c.Dungeons {
			totals[d] += n
		}
	}
	var f favoriteDungeon
	for d, n := range totals {
		if n > f.Count || n == f.Count && n > 0 && d < f.Dungeon { // ties: by name, so it does not flicker
			f.Dungeon, f.Count = d, n
		}
	}
	if f.Count <= 0 {
		return f, false
	}
	best := -1
	for _, c := range chars {
		if n := c.Dungeons[f.Dungeon]; n > best || n == best && c.ClassType < f.ClassType {
			best = n
			f.lookDoc = lookDoc{ClassType: c.ClassType, Skin: c.Skin, Tex1: c.Tex1, Tex2: c.Tex2}
		}
	}
	return f, true
}
