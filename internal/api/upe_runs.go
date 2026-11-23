package api

import (
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/Deliberated/Loot-Gobbler-Server/internal/store"
)

// GET /api/v1/guild/upe/runs: the guild's game mode runs (UPE and its variants), to compare them: per member the
// runs they marked, with the character and what each run gathered. The server does not know the items, so the
// clients compute the points. (GET /api/v1/guild/upe is the gUPE trade check, upe.go.)

type upeGathered struct {
	ItemID   int    `json:"itemId"`
	Enchants int    `json:"enchants"` // -1 if unknown
	Where    string `json:"where"`
	At       int64  `json:"at"`
	How      string `json:"how"`
}

type upeRun struct {
	CharID    int64             `json:"charId"`
	Mode      string            `json:"mode"`
	MarkedAt  int64             `json:"markedAt,omitempty"`
	Lost      bool              `json:"lost"`             // a rule was broken
	Why       string            `json:"why,omitempty"`    // which rule, as the client wrote it
	LostAt    int64             `json:"lostAt,omitempty"` // when
	Notes     []string          `json:"notes,omitempty"`  // what the client found worth a look (not against the rules)
	Alive     bool              `json:"alive"`
	Character *caravanCharacter `json:"character,omitempty"`
	Gathered  []upeGathered     `json:"gathered"`
}

type upeMember struct {
	guildMember
	Runs []upeRun `json:"runs"`
}

type upeResponse struct {
	Guild   string      `json:"guild"`
	Members []upeMember `json:"members"`
}

// The parts of a synced UPE document ("upe", the client's upe.json) read here.
type upeRunsDoc struct {
	Runs map[string]struct {
		CharID   int64    `json:"charId"`
		Mode     string   `json:"mode"`
		Marked   bool     `json:"marked"`
		MarkedAt int64    `json:"markedAt"`
		BrokenAt int64    `json:"brokenAt"`
		Broken   string   `json:"broken"`
		Notes    []string `json:"notes"`
		Gathered []struct {
			Item     int    `json:"item"`
			Enchants []int  `json:"enchants"`
			At       int64  `json:"at"`
			How      string `json:"how"`
			Where    string `json:"where"`
		} `json:"gathered"`
	} `json:"runs"`
}

func (s *Server) guildUpeRuns(w http.ResponseWriter, r *http.Request) error {
	user, err := s.require(r, store.RoleUser)
	if err != nil {
		return err
	}
	ctx := r.Context()
	guild, members, err := s.guildOf(ctx, user)
	if err != nil {
		return err
	}
	rows := make([]upeMember, 0, len(members))
	for _, m := range members {
		docs, err := store.ListData(ctx, s.store.DB(), m.ID, []string{"upe", "characters"}, true)
		if err != nil {
			return err
		}
		row := upeMember{guildMember: m, Runs: []upeRun{}}
		var doc upeRunsDoc
		if json.Unmarshal(docs["upe"].Data, &doc) == nil {
			var chars map[string]caravanCharDoc
			_ = json.Unmarshal(docs["characters"].Data, &chars)
			for key, run := range doc.Runs {
				if !run.Marked {
					continue
				}
				id := run.CharID
				if id == 0 {
					id, _ = strconv.ParseInt(key, 10, 64)
				}
				out := upeRun{CharID: id, Mode: run.Mode, MarkedAt: run.MarkedAt, Lost: run.BrokenAt > 0, Gathered: []upeGathered{}}
				if out.Lost {
					out.Why, out.LostAt = run.Broken, run.BrokenAt
				}
				out.Notes = run.Notes
				if c, ok := chars[strconv.FormatInt(id, 10)]; ok {
					character := c.caravanCharacter
					out.Character = &character
					out.Alive = c.Alive
				}
				for _, g := range run.Gathered {
					enchants := -1
					if g.Enchants != nil {
						enchants = len(g.Enchants)
					}
					out.Gathered = append(out.Gathered, upeGathered{ItemID: g.Item, Enchants: enchants, Where: g.Where, At: g.At, How: g.How})
				}
				row.Runs = append(row.Runs, out)
			}
		}
		sort.Slice(row.Runs, func(i, j int) bool { return row.Runs[i].CharID > row.Runs[j].CharID })
		rows = append(rows, row)
	}
	sort.SliceStable(rows, func(i, j int) bool { return strings.ToLower(rows[i].IGN) < strings.ToLower(rows[j].IGN) })
	writeJSON(w, http.StatusOK, upeResponse{Guild: guild, Members: rows})
	return nil
}
