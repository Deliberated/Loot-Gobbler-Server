package api

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/Deliberated/Loot-Gobbler-Server/internal/store"
)

// Guild settings: what the clients' guild views compute with, set by an admin. baselineHpm is the hits per minute
// a dungeon run counts as average: a player's "hit%" is HPM / (HPM + baselineHpm) × 100, worked out by the clients.

const guildSettingsKey = "guild"

type guildSettings struct {
	BaselineHPM float64 `json:"baselineHpm"`
}

var defaultGuildSettings = guildSettings{BaselineHPM: 50}

func (s *Server) guildSettings(ctx context.Context) (guildSettings, error) {
	raw, err := store.Setting(ctx, s.store.DB(), guildSettingsKey)
	if err != nil || raw == nil {
		return defaultGuildSettings, err
	}
	g := defaultGuildSettings
	if json.Unmarshal(raw, &g) != nil || !(g.BaselineHPM > 0) {
		return defaultGuildSettings, nil
	}
	return g, nil
}

// getGuildSettings: everyone logged in reads them, as their client uses them.
func (s *Server) getGuildSettings(w http.ResponseWriter, r *http.Request) error {
	if _, err := s.require(r, store.RoleUser); err != nil {
		return err
	}
	g, err := s.guildSettings(r.Context())
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, g)
	return nil
}

// putGuildSettings: admins (not moderators) change them.
func (s *Server) putGuildSettings(w http.ResponseWriter, r *http.Request) error {
	admin, err := s.require(r, store.RoleAdmin)
	if err != nil {
		return err
	}
	var req *struct {
		BaselineHPM *float64 `json:"baselineHpm"`
	}
	if err := decodeJSON(w, r, smallBody, &req, false); err != nil {
		return err
	}
	if req == nil || req.BaselineHPM == nil {
		return errBadRequest("baselineHpm is required")
	}
	if v := *req.BaselineHPM; !(v >= 1 && v <= 1000) {
		return errBadRequest("baselineHpm must be between 1 and 1000")
	}
	g := guildSettings{BaselineHPM: *req.BaselineHPM}
	raw, _ := json.Marshal(g)
	if err := store.PutSetting(r.Context(), s.store.DB(), guildSettingsKey, raw, admin.ID, s.nowMs()); err != nil {
		return err
	}
	s.log.Info("guild settings changed", "by", admin.IGN, "baselineHpm", g.BaselineHPM)
	writeJSON(w, http.StatusOK, g)
	return nil
}
