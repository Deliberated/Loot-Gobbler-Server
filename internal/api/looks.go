package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/Deliberated/Loot-Gobbler-Server/internal/store"
)

// How guildmates looked in a run, for the clients' dungeon views. A client only sees the other players' skins and
// dyes from the outside, where an ability's transformation can hide them; every client knows its own player's
// look for sure and saves it with each dungeon visit it syncs. This hands those snapshots to the guildmates, and
// for visits without one (older clients, older visits) the looks of the member's characters now.

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
}

type characterLookDoc struct {
	lookDoc
	Alive      bool  `json:"alive"`
	LastPlayed int64 `json:"lastPlayed"`
}

// runLook is one answer: from the member's own visit of that run ("visit"), or one of their characters as it
// looks now ("character", with alive and lastPlayed so the client can pick the likeliest of a class).
type runLook struct {
	lookDoc
	Source     string `json:"source"`
	Alive      bool   `json:"alive,omitempty"`
	LastPlayed int64  `json:"lastPlayed,omitempty"`
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
		if l := visitLook(items["dungeon_visits"].Data, dungeon, from, to); l != nil {
			if l.Player == "" {
				l.Player = m.IGN
			}
			resp.Looks = append(resp.Looks, runLook{lookDoc: *l, Source: "visit"})
			continue
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

// visitLook is the look saved with the member's visit of the dungeon overlapping [from, to], or nil.
func visitLook(data json.RawMessage, dungeon string, from, to int64) *lookDoc {
	var visits []visitLookDoc
	if json.Unmarshal(data, &visits) != nil {
		return nil
	}
	for _, v := range visits {
		end := v.Start + v.DurationMs
		if v.Look == nil || v.Look.ClassType <= 0 || !strings.EqualFold(v.Dungeon, dungeon) {
			continue
		}
		if v.Start <= to+lookSlackMs && end >= from-lookSlackMs {
			return v.Look
		}
	}
	return nil
}
