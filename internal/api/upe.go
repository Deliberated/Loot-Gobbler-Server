package api

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/Deliberated/Loot-Gobbler-Server/internal/store"
)

// gUPE trades. Guild mates who play a guild UPE (an Untiered Player Experience shared by the guild) may trade what
// their runs gathered. Each client reports its own trades in its synced "upe" document (what it gave, whether its
// run had gathered it, what it got, the run's mode and standing); the partner's client fetches those reports here
// and checks its trade against them. The server only hands the reports to the guild mates; the clients judge.

// The parts of a synced upe document read here: per character its run, with the trades it reported.
type upeDoc struct {
	Runs map[string]struct {
		Trades []json.RawMessage `json:"trades"`
	} `json:"runs"`
}

// The time of a reported trade, to leave out old ones; the rest is passed on as the client wrote it.
type upeTradeTime struct {
	At int64 `json:"at"`
}

type guildUpeMember struct {
	UserID int64             `json:"userId"`
	IGN    string            `json:"ign"`
	Trades []json.RawMessage `json:"trades"`
}

type guildUpeResponse struct {
	Members []guildUpeMember `json:"members"`
}

// guildUpe answers GET /api/v1/guild/upe?since=.. (epoch ms): every guild mate (not the requester) with the gUPE
// trades they reported since then. Members without any are listed too, with none: the client can tell a guild mate
// who did not report a trade (yet) from someone outside the guild.
func (s *Server) guildUpe(w http.ResponseWriter, r *http.Request) error {
	user, err := s.require(r, store.RoleUser)
	if err != nil {
		return err
	}
	since, err := strconv.ParseInt(r.URL.Query().Get("since"), 10, 64)
	if err != nil {
		return errBadRequest("since is needed")
	}
	ctx := r.Context()
	_, members, err := s.guildOf(ctx, user)
	if err != nil {
		return err
	}
	resp := guildUpeResponse{Members: []guildUpeMember{}}
	for _, m := range members {
		if m.ID == user.ID {
			continue
		}
		e := guildUpeMember{UserID: m.ID, IGN: m.IGN, Trades: []json.RawMessage{}}
		items, err := store.ListData(ctx, s.store.DB(), m.ID, []string{"upe"}, true)
		if err != nil {
			return err
		}
		var doc upeDoc
		if json.Unmarshal(items["upe"].Data, &doc) == nil {
			for _, run := range doc.Runs {
				for _, t := range run.Trades {
					var at upeTradeTime
					if json.Unmarshal(t, &at) == nil && at.At >= since {
						e.Trades = append(e.Trades, t)
					}
				}
			}
		}
		resp.Members = append(resp.Members, e)
	}
	writeJSON(w, http.StatusOK, resp)
	return nil
}
