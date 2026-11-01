-- Quests. Times are Unix milliseconds; weeks start Monday 00:00 UTC.

-- The quest pool. Members suggest quests, admins accept them as personal or guild quests (or deny them).
-- params is JSON: {"count": 5, "dungeon": "Void", "minEnchants": 2, "bag": "White"} (all but count optional).
CREATE TABLE quest_templates (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    title       TEXT    NOT NULL,
    type        TEXT    NOT NULL,
    params      TEXT    NOT NULL,
    scope       TEXT    NOT NULL CHECK (scope IN ('personal', 'guild')),
    points      INTEGER NOT NULL,
    status      TEXT    NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'approved', 'denied')),
    proposed_by INTEGER REFERENCES users (id) ON DELETE SET NULL,   -- NULL = built-in suggestion
    created_at  INTEGER NOT NULL,
    decided_by  INTEGER REFERENCES users (id) ON DELETE SET NULL,
    decided_at  INTEGER
);

-- A quest of one week: personal (user_id) or for a whole guild (guild, lower case). Title, type, params and
-- points are copied from the template, so changing the pool later does not change running quests.
CREATE TABLE quest_assignments (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    week_start   INTEGER NOT NULL,
    user_id      INTEGER REFERENCES users (id) ON DELETE CASCADE,
    guild        TEXT,
    template_id  INTEGER REFERENCES quest_templates (id) ON DELETE SET NULL,
    title        TEXT    NOT NULL,
    type         TEXT    NOT NULL,
    params       TEXT    NOT NULL,
    points       INTEGER NOT NULL,
    completed_at INTEGER,
    CHECK ((user_id IS NULL) <> (guild IS NULL))
);
CREATE INDEX quest_assignments_user ON quest_assignments (user_id, week_start);
CREATE INDEX quest_assignments_guild ON quest_assignments (guild, week_start);

-- Points earned: one row per finished quest and player (guild quests: every member who contributed).
CREATE TABLE quest_points (
    assignment_id INTEGER NOT NULL REFERENCES quest_assignments (id) ON DELETE CASCADE,
    user_id       INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    points        INTEGER NOT NULL,
    awarded_at    INTEGER NOT NULL,
    PRIMARY KEY (assignment_id, user_id)
);
CREATE INDEX quest_points_time ON quest_points (awarded_at);

-- Starter suggestions, waiting for an admin to accept or deny them.
INSERT INTO quest_templates (title, type, params, scope, points, created_at) VALUES
    ('Drop 3 UT/ST items', 'drops', '{"count":3}', 'personal', 10, 0),
    ('Drop 8 UT/ST items', 'drops', '{"count":8}', 'personal', 25, 0),
    ('Drop a UT/ST item with 2 or more enchantments', 'drops', '{"count":1,"minEnchants":2}', 'personal', 15, 0),
    ('Find 2 items that are new to your collection', 'new_items', '{"count":2}', 'personal', 20, 0),
    ('Clear 5 dungeons', 'dungeon_clears', '{"count":5}', 'personal', 10, 0),
    ('Clear 3 Voids', 'dungeon_clears', '{"count":3,"dungeon":"Void"}', 'personal', 25, 0),
    ('Clear 2 Oryx Sanctuaries', 'dungeon_clears', '{"count":2,"dungeon":"Oryx''s Sanctuary"}', 'personal', 25, 0),
    ('Earn 2,000 fame', 'fame', '{"count":2000}', 'personal', 15, 0),
    ('Deal 500,000 damage to bosses', 'boss_damage', '{"count":500000}', 'personal', 20, 0),
    ('Guild: drop 50 UT/ST items', 'drops', '{"count":50}', 'guild', 50, 0),
    ('Guild: clear 40 dungeons', 'dungeon_clears', '{"count":40}', 'guild', 40, 0),
    ('Guild: earn 50,000 fame', 'fame', '{"count":50000}', 'guild', 50, 0);
