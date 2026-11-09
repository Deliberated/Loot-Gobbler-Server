package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/Deliberated/Loot-Gobbler-Server/internal/store"
)

// How guildmates looked in a run, and the hits they took in it, for the clients' dungeon views. A client only sees
// the other players' skins and dyes from the outside, where an ability's transformation can hide them; every client
// knows its own player's look for sure and saves it with each dungeon visit it syncs. This hands those snapshots to
// the guildmates, and for visits without one (older clients, older visits) the looks of the member's characters
// now. Every hit a member took is in their synced visit too, so the run's damage taken can be shown for the group.

const lookSlackMs = 60_000 // a visit may start or end a little apart from the asking client's run

// lookDoc is a character's look: class, skin (0 = the class's own) and the two dye textures.
type lookDoc struct {
	Player    string `json:"player,omitempty"`
	ClassType int    `json:"classType"`
	Skin      int    `json:"skin"`
	Tex1      int    `json:"tex1"`
	Tex2      int    `json:"tex2"`
}

// The parts of a synced dungeon visit read here.
type visitLookDoc struct {
	Dungeon    string   `json:"dungeon"`
	Start      int64    `json:"start"`
	DurationMs int64    `json:"durationMs"`
	Look       *lookDoc `json:"look"`
	MaxHP      int      `json:"maxHp"`
	HitTime    []int64  `json:"hitTime"` // ms after the visit's start
	HitDamage  []int    `json:"hitDamage"`
	Dealt      []int64  `json:"dealt"`     // damage dealt to enemies, per second after the visit's start
	QuickItem  []int    `json:"quickItem"` // the items used from a quick slot, in order
	QuickTime  []int64  `json:"quickTime"` // when each was used, ms after the visit's start
	Moved      []int    `json:"moved"`     // tenths of a tile walked, per second after the visit's start
}

type characterLookDoc struct {
	lookDoc
	Alive      bool  `json:"alive"`
	LastPlayed int64 `json:"lastPlayed"`
}

// runLook is one answer: from the member's own visit of that run ("visit": its look, which is empty for visits
// saved before looks were, and the hits they took, at ms after the asked "from"), or one of their characters as it
// looks now ("character", with alive and lastPlayed so the client can pick the likeliest of a class). A visit also
// tells the damage the member dealt in it, the items they used from a quick slot (and when), and how far they walked per second
// (moved, its first second starting moveAt ms after "from"); none of these from older clients.
type runLook struct {
	lookDoc
	Source     string  `json:"source"`
	Alive      bool    `json:"alive,omitempty"`
	LastPlayed int64   `json:"lastPlayed,omitempty"`
	MaxHP      int     `json:"maxHp,omitempty"`
	HitAt      []int64 `json:"hitAt,omitempty"`
	HitDamage  []int   `json:"hitDamage,omitempty"`
	Dealt      int64   `json:"dealt,omitempty"`
	QuickUsed  []int   `json:"quickUsed,omitempty"`
	QuickAt    []int64 `json:"quickAt,omitempty"` // when each was used, ms after the asked "from"
	MoveAt     int64   `json:"moveAt,omitempty"`
	Moved      []int   `json:"moved,omitempty"`
}

type runLooksResponse struct {
	Looks []runLook `json:"looks"`
}

// guildLooks answers GET /api/v1/guild/looks?dungeon=..&from=..&to=.. (epoch ms): the looks of the requester's
// guildmates who may have been in that dungeon then.
func (s *Server) guildLooks(w http.ResponseWriter, r *http.Request) error {
	user, err := s.require(r, store.RoleUser)
	if err != nil {
		return err
	}
	q := r.URL.Query()
	dungeon := strings.TrimSpace(q.Get("dungeon"))
	from, errFrom := strconv.ParseInt(q.Get("from"), 10, 64)
	to, errTo := strconv.ParseInt(q.Get("to"), 10, 64)
	if dungeon == "" || errFrom != nil || errTo != nil || to < from {
		return errBadRequest("dungeon, from and to are needed")
	}
	ctx := r.Context()
	_, members, err := s.guildOf(ctx, user)
	if err != nil {
		return err
	}
	resp := runLooksResponse{Looks: []runLook{}}
	for _, m := range members {
		if m.ID == user.ID {
			continue
		}
		items, err := store.ListData(ctx, s.store.DB(), m.ID, []string{"dungeon_visits", "characters"}, true)
		if err != nil {
			return err
		}
		if v := runVisit(items["dungeon_visits"].Data, dungeon, from, to); v != nil {
			e := runLook{Source: "visit", MaxHP: v.MaxHP, HitAt: []int64{}, HitDamage: []int{}}
			if v.Look != nil {
				e.lookDoc = *v.Look
			}
			if e.Player == "" {
				e.Player = m.IGN
			}
			// the hits within the run, timed from the asking client's start (visits start a little apart)
			for i, t := range v.HitTime {
				at := v.Start + t - from
				if i < len(v.HitDamage) && at >= -lookSlackMs && at <= to-from+lookSlackMs {
					e.HitAt = append(e.HitAt, at)
					e.HitDamage = append(e.HitDamage, v.HitDamage[i])
				}
			}
			for _, d := range v.Dealt {
				e.Dealt += d
			}
			e.QuickUsed = v.QuickItem
			if len(v.QuickTime) == len(v.QuickItem) && len(v.QuickItem) > 0 {
				e.QuickAt = make([]int64, len(v.QuickTime))
				for i, t := range v.QuickTime {
					e.QuickAt[i] = v.Start + t - from
				}
			}
			if len(v.Moved) > 0 {
				e.MoveAt, e.Moved = v.Start-from, v.Moved
			}
			resp.Looks = append(resp.Looks, e)
			if v.Look != nil && v.Look.ClassType > 0 {
				continue
			}
		}
		var chars map[string]characterLookDoc
		if json.Unmarshal(items["characters"].Data, &chars) != nil {
			continue
		}
		for _, c := range chars {
			if c.ClassType <= 0 {
				continue
			}
			c.Player = m.IGN
			resp.Looks = append(resp.Looks, runLook{lookDoc: c.lookDoc, Source: "character", Alive: c.Alive, LastPlayed: c.LastPlayed})
		}
	}
	writeJSON(w, http.StatusOK, resp)
	return nil
}

// runVisit is the member's visit of the dungeon that overlaps [from, to] the most, or nil. Runs of the same
// dungeon done back to back can both be within the slack of the asked run; the one shared longest is that run.
func runVisit(data json.RawMessage, dungeon string, from, to int64) *visitLookDoc {
	var visits []visitLookDoc
	if json.Unmarshal(data, &visits) != nil {
		return nil
	}
	var best *visitLookDoc
	var bestOverlap int64
	for i, v := range visits {
		end := v.Start + v.DurationMs
		if !strings.EqualFold(v.Dungeon, dungeon) || v.Start > to+lookSlackMs || end < from-lookSlackMs {
			continue
		}
		overlap := min(end, to) - max(v.Start, from) // negative: only within the slack, by that far
		if best == nil || overlap > bestOverlap {
			best, bestOverlap = &visits[i], overlap
		}
	}
	return best
}
