package api

import (
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/Deliberated/Loot-Gobbler-Server/internal/store"
)

// The loot caravan: per guild member the items they found in a time range (counted by bag color), the items and the
// character they played most in that range. The client draws each member on a wagon piled with their items.

const (
	maxCaravanItems = 120
	findTimeLayout  = "2006-01-02T15:04:05" // the client's local time, without a zone
)

var caravanRanges = map[string]time.Duration{"week": 7 * 24 * time.Hour, "month": 30 * 24 * time.Hour, "all": 0}

type caravanItem struct {
	ItemID   int    `json:"itemId"`
	Enchants int    `json:"enchants"` // -1 if unknown
	Bag      string `json:"bag"`
	Time     string `json:"time"`
	Where    string `json:"where"`
}

type caravanCharacter struct {
	ClassType int    `json:"classType"`
	ClassName string `json:"className"`
	Skin      int    `json:"skin"`
	Tex1      int    `json:"tex1"`
	Tex2      int    `json:"tex2"`
	Fame      int64  `json:"fame"`
}

type caravanMember struct {
	guildMember
	ByBag     map[string]int    `json:"byBag"` // bag color ("White", "Orange", ...; boosted counts as its color) -> items
	Total     int               `json:"total"` // all items found
	Items     []caravanItem     `json:"items"` // newest first, at most 120
	Tally     []tallyEntry      `json:"tally"` // all items of the range, for the score
	LastDrop  string            `json:"lastDrop,omitempty"`
	Character *caravanCharacter `json:"character,omitempty"` // most played in the range; nil if no character is known
}

type caravanResponse struct {
	Guild   string          `json:"guild"`
	Range   string          `json:"range"`
	Members []caravanMember `json:"members"`
}

// The parts of a synced character the caravan reads.
type caravanCharDoc struct {
	caravanCharacter
	Alive      bool  `json:"alive"`
	LastPlayed int64 `json:"lastPlayed"`
	Samples    []struct {
		T    int64 `json:"t"`
		Fame int64 `json:"fame"`
	} `json:"samples"`
}

func (s *Server) guildCaravan(w http.ResponseWriter, r *http.Request) error {
	user, err := s.require(r, store.RoleUser)
	if err != nil {
		return err
	}
	rng := r.URL.Query().Get("range")
	if rng == "" {
		rng = "week"
	}
	span, ok := caravanRanges[rng]
	if !ok {
		return errBadRequest("range must be week, month or all")
	}
	ctx := r.Context()
	guild, members, err := s.guildOf(ctx, user)
	if err != nil {
		return err
	}
	var since time.Time // zero: everything
	if span > 0 {
		since = time.Now().Add(-span)
	}

	rows := make([]caravanMember, 0, len(members))
	for _, m := range members {
		items, err := store.ListData(ctx, s.store.DB(), m.ID, []string{"characters"}, true)
		if err != nil {
			return err
		}
		logged, err := s.drops(ctx, m.ID)
		if err != nil {
			return err
		}
		row := caravanMember{guildMember: m, ByBag: map[string]int{}, Items: []caravanItem{}}
		tally := tallier{}
		for _, d := range logged {
			if !inRange(d.Time, since) {
				continue
			}
			color := bagColor(d.Bag)
			row.ByBag[color]++
			row.Total++
			enchants := d.Enchants
			if d.EnchantIDs != nil {
				enchants = len(d.EnchantIDs)
			}
			row.Items = append(row.Items, caravanItem{ItemID: d.itemID, Enchants: enchants, Bag: color, Time: d.Time, Where: d.Where})
			tally.add(d)
		}
		row.Tally = tally.entries()
		sort.SliceStable(row.Items, func(i, j int) bool { return row.Items[i].Time > row.Items[j].Time })
		if len(row.Items) > 0 {
			row.LastDrop = row.Items[0].Time
		}
		if len(row.Items) > maxCaravanItems {
			row.Items = row.Items[:maxCaravanItems]
		}
		var chars map[string]caravanCharDoc
		if json.Unmarshal(items["characters"].Data, &chars) == nil {
			row.Character = mostPlayed(chars, since)
		}
		rows = append(rows, row)
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].Total != rows[j].Total {
			return rows[i].Total > rows[j].Total
		}
		return strings.ToLower(rows[i].IGN) < strings.ToLower(rows[j].IGN)
	})
	writeJSON(w, http.StatusOK, caravanResponse{Guild: guild, Range: rng, Members: rows})
	return nil
}

// inRange reports whether a find's time is not before since. Find times are the player's local time without a
// zone; for ranges of a week or more the few hours that can be off do not matter.
func inRange(t string, since time.Time) bool {
	if since.IsZero() {
		return true
	}
	parsed, err := time.Parse(findTimeLayout, t) // read as UTC
	return err == nil && !parsed.Before(since.UTC())
}

// bagColor turns the bag name a client stored ("White", "Boosted Orange", "") into its color.
func bagColor(bag string) string {
	bag = strings.TrimSpace(strings.TrimPrefix(bag, "Boosted "))
	if bag == "" {
		return "Other"
	}
	return strings.ToUpper(bag[:1]) + bag[1:]
}

// mostPlayed picks the living character that gained the most fame since the given time; without any gain the one
// played last. Dead characters only when nothing else is known.
func mostPlayed(chars map[string]caravanCharDoc, since time.Time) *caravanCharacter {
	var best *caravanCharDoc
	var bestGain, bestLast int64 = 0, -1
	from := since.UnixMilli()
	for id := range chars {
		c := chars[id]
		var gain, first int64 = 0, -1
		for _, smp := range c.Samples {
			if since.IsZero() || smp.T >= from {
				if first < 0 {
					first = smp.Fame
				}
				gain = smp.Fame - first
			}
		}
		better := false
		switch {
		case best == nil:
			better = true
		case c.Alive != best.Alive:
			better = c.Alive
		case gain != bestGain:
			better = gain > bestGain
		default:
			better = c.LastPlayed > bestLast
		}
		if better {
			best, bestGain, bestLast = &c, gain, c.LastPlayed
		}
	}
	if best == nil {
		return nil
	}
	ch := best.caravanCharacter
	return &ch
}
