package api

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/Deliberated/Loot-Gobbler-Server/internal/store"
)

// The game's seasons. The game does not say when a season starts, only when it ends, so clients report the seasons
// the game lists (its mission seasons: id, name, end date) and the server keeps them. A season starts when the one
// before it ends: the season running now began at the latest end date that has passed (none known: everything so
// far belongs to it). Seasons ending at the same moment (the game runs side tracks next to the main one) are one.

const (
	maxSeasonReports = 20
	maxSeasonName    = 100
	seasonReach      = 400 * 24 * time.Hour // end dates further off than this are not believed
)

type seasonReport struct {
	Seasons []struct {
		ID      int64  `json:"id"`
		Name    string `json:"name"`
		EndDate int64  `json:"endDate"` // epoch seconds, as the game sends it
	} `json:"seasons"`
}

// currentSeason is the season running now: its names (the game's, lowest id first), its ids, when it started
// (epoch ms; 0 if no season before it is known) and when it ends (0 if the game has not listed the next yet).
type currentSeason struct {
	Names []string `json:"names"`
	IDs   []int64  `json:"ids"`
	Start int64    `json:"start"`
	End   int64    `json:"end"`
}

type seasonRow struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	End       int64  `json:"end"`
	FirstSeen int64  `json:"firstSeen"`
}

type seasonsResponse struct {
	Current currentSeason `json:"current"`
	Seasons []seasonRow   `json:"seasons"`
}

// postSeasons takes the seasons a player's client read from the game.
func (s *Server) postSeasons(w http.ResponseWriter, r *http.Request) error {
	user, err := s.require(r, store.RoleUser)
	if err != nil {
		return err
	}
	var rep seasonReport
	if err := decodeJSON(w, r, 16<<10, &rep, false); err != nil {
		return err
	}
	if len(rep.Seasons) > maxSeasonReports {
		return errBadRequest("At most 20 seasons at a time")
	}
	now := s.now()
	ctx := r.Context()
	err = s.store.Tx(ctx, func(q store.Querier) error {
		for _, x := range rep.Seasons {
			name := strings.TrimSpace(x.Name)
			end := time.Unix(x.EndDate, 0)
			// over already (nothing to learn), or not believable
			if x.ID <= 0 || name == "" || len(name) > maxSeasonName || !end.After(now) || end.After(now.Add(seasonReach)) {
				continue
			}
			if err := store.ReportSeason(ctx, q, store.Season{ID: x.ID, Name: name, EndAt: end.UnixMilli()}, user.ID, now.UnixMilli()); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	cur, err := s.currentSeason(ctx)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, map[string]any{"current": cur})
	return nil
}

func (s *Server) getSeasons(w http.ResponseWriter, r *http.Request) error {
	if _, err := s.require(r, store.RoleUser); err != nil {
		return err
	}
	ctx := r.Context()
	all, err := store.Seasons(ctx, s.store.DB())
	if err != nil {
		return err
	}
	resp := seasonsResponse{Current: seasonNow(all, s.nowMs()), Seasons: make([]seasonRow, 0, len(all))}
	for _, x := range all {
		resp.Seasons = append(resp.Seasons, seasonRow{ID: x.ID, Name: x.Name, End: x.EndAt, FirstSeen: x.FirstSeen})
	}
	writeJSON(w, http.StatusOK, resp)
	return nil
}

func (s *Server) currentSeason(ctx context.Context) (currentSeason, error) {
	all, err := store.Seasons(ctx, s.store.DB())
	if err != nil {
		return currentSeason{}, err
	}
	return seasonNow(all, s.nowMs()), nil
}

// seasonNow picks the season running at now out of all known (ordered by end date, then id).
func seasonNow(all []store.Season, now int64) currentSeason {
	cur := currentSeason{Names: []string{}, IDs: []int64{}}
	for _, x := range all {
		switch {
		case x.EndAt <= now:
			cur.Start = max(cur.Start, x.EndAt)
		case cur.End == 0 || x.EndAt == cur.End:
			cur.End = x.EndAt
			cur.Names = append(cur.Names, x.Name)
			cur.IDs = append(cur.IDs, x.ID)
		}
	}
	return cur
}
