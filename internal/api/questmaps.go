package api

import (
	"math/rand/v2"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Deliberated/Loot-Gobbler-Server/internal/store"
)

// Guild quest maps: a chain of 3 to 8 dungeons the guild clears in order. Any member's clear of the current stage
// unlocks the next one. A map runs 6 to 12 days; then a new one is rolled (random style, colors, dungeons and length,
// until a dungeon picking system replaces the randomness). Progress is read from the members' synced Run History.

const (
	defaultQuestMaps = 10
	maxQuestMaps     = 50
	minStages        = 3
	maxStages        = 8
	minMapDays       = 6
	maxMapDays       = 12
	questMapVariants = 4
)

// Dungeons a map can pick, by difficulty tier; a map's stages are sorted by tier so a chain gets harder. Names are
// the map names the client records in Run History.
var questMapDungeons = []struct {
	name string
	tier int
}{
	{"Pirate Cave", 1}, {"Forest Maze", 1}, {"Spider Den", 1}, {"Snake Pit", 1}, {"Forbidden Jungle", 1}, {"The Hive", 1},
	{"Magic Woods", 2}, {"Sprite World", 2}, {"Ancient Ruins", 2}, {"Candyland Hunting Grounds", 2},
	{"Haunted Cemetery", 2}, {"Toxic Sewers", 2}, {"Mad Lab", 2}, {"Cursed Library", 2},
	{"Undead Lair", 3}, {"Abyss of Demons", 3}, {"Manor of the Immortals", 3}, {"Puppet Master's Theatre", 3},
	{"Ice Cave", 3}, {"Davy Jones' Locker", 3}, {"Mountain Temple", 3}, {"Parasite Chambers", 3},
	{"Deadwater Docks", 3}, {"Woodland Labyrinth", 3}, {"The Crawling Depths", 3}, {"Ocean Trench", 3},
	{"Tomb of the Ancients", 3},
	{"The Third Dimension", 4}, {"Lair of Draconis", 4}, {"Fungal Cavern", 4}, {"Crystal Cavern", 4},
	{"Secluded Thicket", 4}, {"Cultist Hideout", 4}, {"The Nest", 4}, {"Lost Halls", 4},
	{"Spectral Penitentiary", 4}, {"Advanced Kogbold Steamworks", 4}, {"Moonlight Village", 4},
	{"The Void", 5}, {"The Shatters", 5}, {"Oryx's Sanctuary", 5},
}

// The map styles the client can draw, with names for their chains.
var questMapStyles = map[string][]string{
	"parchment":     {"The Old Road", "The Cartographer's Trail", "Beyond the Map's Edge", "The Wanderer's Route"},
	"hex":           {"The Honeycomb March", "Six Sides of War", "The Hex Frontier", "The Tiled Campaign"},
	"overworld":     {"World G", "Guild Road", "Level Select", "The Big Adventure"},
	"oryx":          {"Oryx's Gauntlet", "The Mad God's Tower", "Climb to the Throne", "The Castle Ascent"},
	"constellation": {"The Starlit Path", "The Gobbler Constellation", "Night Sky Trail", "Stars of the Realm"},
	"neon":          {"The Grid", "Light Trail", "Neon Circuit", "Data Highway"},
	"adventure":     {"The Hero's Path", "Into the Wilds", "The Hidden Shrines", "Dungeon Crawl"},
	"hunt":          {"The Great Hunt", "Field Survey", "Tracks in the Mud", "The Apex Hunt"},
	"hellforge":     {"Hellforge", "The Descent", "The Lower Sectors", "Breach Protocol"},
}

// The color family of each style's colorways (by variant), as the client draws them. A new map avoids the styles
// and color families of the last few maps; every style has four different families, so one is always left.
var questMapColors = map[string][questMapVariants]string{
	"parchment":     {"tan", "green", "ice", "fire"},    // classic, moss, frost, ember
	"hex":           {"teal", "sand", "ice", "fire"},    // teal, desert, ice, volcanic
	"overworld":     {"green", "sand", "ice", "fire"},   // spring, desert, snow, lava night
	"oryx":          {"purple", "red", "ice", "gold"},   // purple, blood, ice, gold
	"constellation": {"purple", "green", "red", "teal"}, // indigo, aurora, crimson, teal
	"neon":          {"cyan", "fire", "pink", "ice"},    // cyan, orange, magenta, white
	"adventure":     {"green", "sand", "ice", "purple"}, // meadow, desert, snowfield, dusk
	"hunt":          {"tan", "fire", "ice", "teal"},     // earth, volcanic, tundra, swamp
	"hellforge":     {"red", "green", "steel", "gold"},  // hellfire, toxic, techbase, ember
}

// noRepeatMaps is how many of the latest maps a new map must differ from, in style and in color family.
const noRepeatMaps = 3

// newQuestMap rolls a map for a guild that starts at start (Unix ms). recent are the guild's latest maps, newest
// first; the new map takes none of the styles or color families of the first noRepeatMaps of them.
func newQuestMap(guild string, start int64, recent []*store.QuestMap) *store.QuestMap {
	usedStyles, usedColors := map[string]bool{}, map[string]bool{}
	for i, m := range recent {
		if i == noRepeatMaps {
			break
		}
		usedStyles[m.Style] = true
		if c, ok := questMapColors[m.Style]; ok && m.Variant >= 0 && m.Variant < questMapVariants {
			usedColors[c[m.Variant]] = true
		}
	}
	var styles []string
	for s := range questMapStyles {
		if !usedStyles[s] {
			styles = append(styles, s)
		}
	}
	sort.Strings(styles)
	style := styles[rand.IntN(len(styles))]
	var variants []int
	for v, color := range questMapColors[style] {
		if !usedColors[color] {
			variants = append(variants, v)
		}
	}
	names := questMapStyles[style]

	n := minStages + rand.IntN(maxStages-minStages+1)
	picked := rand.Perm(len(questMapDungeons))[:n]
	sort.SliceStable(picked, func(i, j int) bool { return questMapDungeons[picked[i]].tier < questMapDungeons[picked[j]].tier })
	dungeons := make([]string, n)
	for i, p := range picked {
		dungeons[i] = questMapDungeons[p].name
	}
	days := minMapDays + rand.IntN(maxMapDays-minMapDays+1)
	return &store.QuestMap{
		Guild: guild, Name: names[rand.IntN(len(names))], Style: style, Variant: variants[rand.IntN(len(variants))],
		Dungeons: dungeons, Clears: []store.QuestMapClear{}, StartsAt: start,
		EndsAt: start + int64(days)*int64(24*time.Hour/time.Millisecond),
	}
}

// advance adds the stages the members have cleared since the map's last clear. A stage is cleared by the earliest
// run of its dungeon (not the guild hall, with boss damage of yours) that started after the previous stage was
// cleared and before the map ended. It reports whether anything changed.
func advance(m *store.QuestMap, members []guildMember, acts map[int64]*playerActivity) bool {
	changed := false
	for len(m.Clears) < len(m.Dungeons) {
		unlock := m.StartsAt
		if n := len(m.Clears); n > 0 {
			unlock = m.Clears[n-1].At
		}
		want := m.Dungeons[len(m.Clears)]
		var best *store.QuestMapClear
		for _, mem := range members {
			a := acts[mem.ID]
			if a == nil {
				continue
			}
			for _, run := range a.runs {
				if run.GuildHall || run.Start < unlock || run.Start >= m.EndsAt || !strings.EqualFold(run.Dungeon, want) || !bossDamage(run) {
					continue
				}
				if best == nil || run.Start < best.At {
					best = &store.QuestMapClear{UserID: mem.ID, IGN: mem.IGN, At: run.Start}
				}
			}
		}
		if best == nil {
			break
		}
		m.Clears = append(m.Clears, *best)
		changed = true
	}
	if changed && len(m.Clears) == len(m.Dungeons) && m.CompletedAt == nil {
		at := m.Clears[len(m.Clears)-1].At
		m.CompletedAt = &at
	}
	return changed
}

// bossDamage reports whether the player who recorded the run damaged one of its bosses.
func bossDamage(r runDoc) bool {
	for _, b := range r.Bosses {
		for _, p := range b.Players {
			if p.Me && p.Damage > 0 {
				return true
			}
		}
	}
	return false
}

type questMapsResponse struct {
	Guild   string            `json:"guild"`
	Now     int64             `json:"now"`
	Current *store.QuestMap   `json:"current"`
	Past    []*store.QuestMap `json:"past"`
}

// guildQuestMaps answers the guild's current quest map (rolling a new one when the last has ended) and the past ones,
// newest first.
func (s *Server) guildQuestMaps(w http.ResponseWriter, r *http.Request) error {
	user, err := s.require(r, store.RoleUser)
	if err != nil {
		return err
	}
	limit := defaultQuestMaps
	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > maxQuestMaps {
			return errBadRequest("limit must be between 1 and 50")
		}
		limit = n
	}
	ctx := r.Context()
	guild, members, err := s.guildOf(ctx, user)
	if err != nil {
		return err
	}
	key := strings.ToLower(guild)
	now := time.Now().UnixMilli()

	var maps []*store.QuestMap
	err = s.store.Tx(ctx, func(q store.Querier) error {
		maps, err = store.QuestMaps(ctx, q, key, max(limit+1, noRepeatMaps))
		if err != nil {
			return err
		}
		if len(maps) == 0 || maps[0].EndsAt <= now {
			m := newQuestMap(key, now, maps)
			if err := store.InsertQuestMap(ctx, q, m); err != nil {
				return err
			}
			maps = append([]*store.QuestMap{m}, maps...)
		}
		return nil
	})
	if err != nil {
		return err
	}
	if len(maps) > limit+1 {
		maps = maps[:limit+1]
	}

	acts, err := s.guildActivities(ctx, user.ID, members)
	if err != nil {
		return err
	}
	for _, m := range maps {
		if m.CompletedAt == nil && advance(m, members, acts) {
			if err := store.SaveQuestMapProgress(ctx, s.store.DB(), m); err != nil {
				return err
			}
		}
	}
	writeJSON(w, http.StatusOK, questMapsResponse{Guild: guild, Now: now, Current: maps[0], Past: maps[1:]})
	return nil
}
