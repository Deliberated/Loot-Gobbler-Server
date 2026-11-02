package api

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/Deliberated/Loot-Gobbler-Server/internal/store"
)

// Guild views. A player's guild is the in-game guild their client reports in the "profile" document, so the
// views only work once a player has been in the game with Loot Gobbler running. Everything here is read-only and
// computed from the documents players sync (collection, characters, exalts); members only see their own guild.

const (
	defaultDrops = 50
	maxDrops     = 200
)

// Exaltation progress thresholds: a stat gains one exaltation at each.
var exaltThresholds = []int{5, 15, 30, 50, 75}

// profileDoc is the "profile" document the client syncs.
type profileDoc struct {
	IGN       string `json:"ign"`
	Guild     string `json:"guild"`
	GuildRank int    `json:"guildRank"` // 0 initiate, 10 member, 20 officer, 30 leader, 40 founder
}

type guildMember struct {
	ID         int64  `json:"id"`
	IGN        string `json:"ign"`
	GuildRank  int    `json:"guildRank"`
	LastActive int64  `json:"lastActive,omitempty"` // last upload of any data
}

type guildResponse struct {
	Guild   string        `json:"guild"`
	Members []guildMember `json:"members"`
}

// The parts of the synced documents the guild views read.
type collectionDoc map[string]struct {
	First   string    `json:"first"`
	Count   int       `json:"count"`
	History []findDoc `json:"history"`
}

type findDoc struct {
	Time       string `json:"time"`
	At         int64  `json:"at,omitempty"` // exact time (epoch ms), sent by newer clients
	Source     string `json:"source"`
	Enchants   int    `json:"enchants"`
	Where      string `json:"where"`
	Bag        string `json:"bag"`
	EnchantIDs []int  `json:"enchantIds,omitempty"`
}

type characterDoc struct {
	Fame       int64 `json:"fame"`
	Alive      bool  `json:"alive"`
	StatsMaxed int   `json:"statsMaxed"`
	Seasonal   bool  `json:"seasonal"`
}

func errNoGuild() error {
	return errConflict("no_guild", "Your guild is not known yet: enter the game once with Loot Gobbler running")
}

// guildOf returns the requester's guild and its members (by name). Guild names are compared case-insensitively.
// itemDrop is one logged drop of a player: the item and the find as the client reported it.
type itemDrop struct {
	itemID int
	findDoc
}

// drops is everything the player's client ever reported dropping (the drop log), newest first. Unlike the synced
// collection it never loses entries, so the guild views keep a player's drops after they clear their collection.
func (s *Server) drops(ctx context.Context, userID int64) ([]itemDrop, error) {
	logged, err := store.Drops(ctx, s.store.DB(), userID)
	if err != nil {
		return nil, err
	}
	out := make([]itemDrop, 0, len(logged))
	for _, d := range logged {
		var f findDoc
		if json.Unmarshal(d.Data, &f) == nil {
			out = append(out, itemDrop{itemID: d.ItemID, findDoc: f})
		}
	}
	return out, nil
}

func (s *Server) guildOf(ctx context.Context, user *store.User) (string, []guildMember, error) {
	docs, err := store.DocsOfKind(ctx, s.store.DB(), "profile")
	if err != nil {
		return "", nil, err
	}
	profiles := map[int64]profileDoc{}
	for _, d := range docs {
		var p profileDoc
		if json.Unmarshal(d.Data, &p) == nil {
			p.Guild = strings.TrimSpace(p.Guild)
			profiles[d.UserID] = p
		}
	}
	guild := profiles[user.ID].Guild
	if guild == "" {
		return "", nil, errNoGuild()
	}
	last, err := store.LastActivity(ctx, s.store.DB())
	if err != nil {
		return "", nil, err
	}
	var members []guildMember
	name, nameRank := guild, -1 // shown as spelled by the highest-ranked member
	for _, d := range docs {
		p, ok := profiles[d.UserID]
		if ok && strings.EqualFold(p.Guild, guild) {
			members = append(members, guildMember{ID: d.UserID, IGN: d.IGN, GuildRank: p.GuildRank, LastActive: last[d.UserID]})
			if p.GuildRank > nameRank {
				name, nameRank = p.Guild, p.GuildRank
			}
		}
	}
	sort.Slice(members, func(i, j int) bool { return strings.ToLower(members[i].IGN) < strings.ToLower(members[j].IGN) })
	return name, members, nil
}

func (s *Server) getGuild(w http.ResponseWriter, r *http.Request) error {
	user, err := s.require(r, store.RoleUser)
	if err != nil {
		return err
	}
	guild, members, err := s.guildOf(r.Context(), user)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, guildResponse{Guild: guild, Members: members})
	return nil
}

// --- leaderboard ---

type leaderRow struct {
	guildMember
	Items      int   `json:"items"`      // different UT/ST items collected
	Finds      int   `json:"finds"`      // all copies found
	Fame       int64 `json:"fame"`       // current fame of all living characters
	Exalts     int   `json:"exalts"`     // exaltations over all classes
	Characters int   `json:"characters"` // living characters
	Maxed      int   `json:"maxed"`      // living characters with all 8 stats maxed
	Points     int   `json:"points"`     // quest points this month
}

type leaderboardResponse struct {
	Guild   string      `json:"guild"`
	Members []leaderRow `json:"members"`
}

// stats computes a member's leaderboard numbers from their synced documents.
func (s *Server) stats(ctx context.Context, m guildMember) (leaderRow, error) {
	row := leaderRow{guildMember: m}
	items, err := store.ListData(ctx, s.store.DB(), m.ID, []string{"collection", "characters", "exalts"}, true)
	if err != nil {
		return row, err
	}
	var coll collectionDoc
	if json.Unmarshal(items["collection"].Data, &coll) == nil {
		for _, e := range coll {
			if e.Count > 0 {
				row.Items++
				row.Finds += e.Count
			}
		}
	}
	var chars map[string]characterDoc
	if json.Unmarshal(items["characters"].Data, &chars) == nil {
		for _, c := range chars {
			if !c.Alive {
				continue
			}
			row.Characters++
			row.Fame += c.Fame
			if c.StatsMaxed >= 8 {
				row.Maxed++
			}
		}
	}
	var exalts map[string][]int
	if json.Unmarshal(items["exalts"].Data, &exalts) == nil {
		for _, progress := range exalts {
			for _, p := range progress {
				for _, t := range exaltThresholds {
					if p >= t {
						row.Exalts++
					}
				}
			}
		}
	}
	return row, nil
}

func (s *Server) guildLeaderboard(w http.ResponseWriter, r *http.Request) error {
	user, err := s.require(r, store.RoleUser)
	if err != nil {
		return err
	}
	ctx := r.Context()
	guild, members, err := s.guildOf(ctx, user)
	if err != nil {
		return err
	}
	// check everyone's quests first, so the points are up to date
	acts, err := s.guildActivities(ctx, user.ID, members)
	if err != nil {
		return err
	}
	now := s.now()
	for _, m := range members {
		g := ""
		if m.ID == user.ID {
			g = guild // the guild quests once
		}
		if _, err := s.settle(ctx, m.ID, g, members, acts, now); err != nil {
			return err
		}
	}
	month := monthStart(now)
	points, err := store.PointsBetween(ctx, s.store.DB(), month.UnixMilli(), month.AddDate(0, 1, 0).UnixMilli())
	if err != nil {
		return err
	}
	rows := make([]leaderRow, 0, len(members))
	for _, m := range members {
		row, err := s.stats(ctx, m)
		if err != nil {
			return err
		}
		row.Points = points[m.ID]
		rows = append(rows, row)
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].Items > rows[j].Items })
	writeJSON(w, http.StatusOK, leaderboardResponse{Guild: guild, Members: rows})
	return nil
}

// --- recent drops ---

type guildDrop struct {
	UserID int64  `json:"userId"`
	IGN    string `json:"ign"`
	ItemID int    `json:"itemId"`
	findDoc
}

type dropsResponse struct {
	Guild string      `json:"guild"`
	Drops []guildDrop `json:"drops"`
}

// guildDrops lists the guild's most recent drops (not vault or inventory sightings), newest first. Times are each
// player's local time, as their client recorded them.
func (s *Server) guildDrops(w http.ResponseWriter, r *http.Request) error {
	user, err := s.require(r, store.RoleUser)
	if err != nil {
		return err
	}
	limit := defaultDrops
	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > maxDrops {
			return errBadRequest("limit must be between 1 and 200")
		}
		limit = n
	}
	ctx := r.Context()
	guild, members, err := s.guildOf(ctx, user)
	if err != nil {
		return err
	}
	drops := []guildDrop{}
	for _, m := range members {
		logged, err := s.drops(ctx, m.ID)
		if err != nil {
			return err
		}
		for _, d := range logged {
			drops = append(drops, guildDrop{UserID: m.ID, IGN: m.IGN, ItemID: d.itemID, findDoc: d.findDoc})
		}
	}
	sort.SliceStable(drops, func(i, j int) bool { return drops[i].Time > drops[j].Time })
	if len(drops) > limit {
		drops = drops[:limit]
	}
	writeJSON(w, http.StatusOK, dropsResponse{Guild: guild, Drops: drops})
	return nil
}

// --- member profile ---

type memberResponse struct {
	Guild  string    `json:"guild"`
	Member leaderRow `json:"member"`
	// Characters as the client stores them, without the fame samples and full inventories (too big, not shown).
	Characters map[string]map[string]json.RawMessage `json:"characters"`
	Exalts     map[string][]int                      `json:"exalts"`
	Collection map[string]int                        `json:"collection"` // item id -> copies found
}

func (s *Server) guildMemberProfile(w http.ResponseWriter, r *http.Request) error {
	user, err := s.require(r, store.RoleUser)
	if err != nil {
		return err
	}
	id, err := pathID(r)
	if err != nil {
		return err
	}
	ctx := r.Context()
	guild, members, err := s.guildOf(ctx, user)
	if err != nil {
		return err
	}
	var member *guildMember
	for i := range members {
		if members[i].ID == id {
			member = &members[i]
		}
	}
	if member == nil {
		return errNotFound("No such member in your guild")
	}
	row, err := s.stats(ctx, *member)
	if err != nil {
		return err
	}
	items, err := store.ListData(ctx, s.store.DB(), id, []string{"collection", "characters", "exalts"}, true)
	if err != nil {
		return err
	}
	resp := memberResponse{Guild: guild, Member: row, Characters: map[string]map[string]json.RawMessage{},
		Exalts: map[string][]int{}, Collection: map[string]int{}}
	if json.Unmarshal(items["characters"].Data, &resp.Characters) == nil {
		for _, c := range resp.Characters {
			delete(c, "samples")
			delete(c, "slots")
			delete(c, "slotEnchantIds")
		}
	}
	json.Unmarshal(items["exalts"].Data, &resp.Exalts)
	var coll collectionDoc
	if json.Unmarshal(items["collection"].Data, &coll) == nil {
		for id, e := range coll {
			resp.Collection[id] = e.Count
		}
	}
	writeJSON(w, http.StatusOK, resp)
	return nil
}
