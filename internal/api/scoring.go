package api

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"

	"github.com/Deliberated/Loot-Gobbler-Server/internal/store"
)

// Item scores. An item is worth
//
//	feedPower * feedPower/100 + difficulty * dungeon difficulty + rarity * rarity (0-4),
//
// rounded to one decimal, with the three weights (0 to 1) set by an admin. The server does not know the items, so
// the clients compute the scores; the server keeps the weights and sends, per member, how often they found each
// item where and with how many enchantments (a tally), so totals cover more than the items it lists.

const scoringKey = "scoring"

type scoreWeights struct {
	FeedPower  float64 `json:"feedPower"`
	Difficulty float64 `json:"difficulty"`
	Rarity     float64 `json:"rarity"`
}

var defaultWeights = scoreWeights{FeedPower: 1, Difficulty: 1, Rarity: 1}

// tallyEntry is how often a member found one item in one place with one number of enchantments (-1 = unknown).
type tallyEntry struct {
	ItemID   int    `json:"itemId"`
	Where    string `json:"where"`
	Enchants int    `json:"enchants"`
	Count    int    `json:"count"`
}

type tallyKey struct {
	item     int
	where    string
	enchants int
}

// tallier adds up drops into tally entries.
type tallier map[tallyKey]int

func (t tallier) add(d itemDrop) {
	enchants := d.Enchants
	if d.EnchantIDs != nil {
		enchants = len(d.EnchantIDs)
	}
	t[tallyKey{d.itemID, d.Where, enchants}]++
}

// entries lists the tally, most found first (then by item), so the output is stable.
func (t tallier) entries() []tallyEntry {
	out := make([]tallyEntry, 0, len(t))
	for k, n := range t {
		out = append(out, tallyEntry{ItemID: k.item, Where: k.where, Enchants: k.enchants, Count: n})
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Count != b.Count {
			return a.Count > b.Count
		}
		if a.ItemID != b.ItemID {
			return a.ItemID < b.ItemID
		}
		if a.Where != b.Where {
			return a.Where < b.Where
		}
		return a.Enchants < b.Enchants
	})
	return out
}

func (s *Server) weights(ctx context.Context) (scoreWeights, error) {
	raw, err := store.Setting(ctx, s.store.DB(), scoringKey)
	if err != nil || raw == nil {
		return defaultWeights, err
	}
	w := defaultWeights
	if json.Unmarshal(raw, &w) != nil {
		return defaultWeights, nil
	}
	return w, nil
}

// getScoring: everyone logged in reads the weights, as their client computes the scores.
func (s *Server) getScoring(w http.ResponseWriter, r *http.Request) error {
	if _, err := s.require(r, store.RoleUser); err != nil {
		return err
	}
	weights, err := s.weights(r.Context())
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, weights)
	return nil
}

// putScoring: admins (not moderators) change the weights.
func (s *Server) putScoring(w http.ResponseWriter, r *http.Request) error {
	admin, err := s.require(r, store.RoleAdmin)
	if err != nil {
		return err
	}
	var req *struct {
		FeedPower  *float64 `json:"feedPower"`
		Difficulty *float64 `json:"difficulty"`
		Rarity     *float64 `json:"rarity"`
	}
	if err := decodeJSON(w, r, smallBody, &req, false); err != nil {
		return err
	}
	if req == nil || req.FeedPower == nil || req.Difficulty == nil || req.Rarity == nil {
		return errBadRequest("feedPower, difficulty and rarity are required")
	}
	for _, v := range []float64{*req.FeedPower, *req.Difficulty, *req.Rarity} {
		if !(v >= 0 && v <= 1) {
			return errBadRequest("weights must be between 0 and 1")
		}
	}
	weights := scoreWeights{FeedPower: *req.FeedPower, Difficulty: *req.Difficulty, Rarity: *req.Rarity}
	raw, _ := json.Marshal(weights)
	if err := store.PutSetting(r.Context(), s.store.DB(), scoringKey, raw, admin.ID, s.nowMs()); err != nil {
		return err
	}
	s.log.Info("score weights changed", "by", admin.IGN, "feedPower", weights.FeedPower,
		"difficulty", weights.Difficulty, "rarity", weights.Rarity)
	writeJSON(w, http.StatusOK, weights)
	return nil
}
