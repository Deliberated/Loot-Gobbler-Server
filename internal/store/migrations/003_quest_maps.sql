-- Guild quest maps: a chain of dungeons a guild clears in order; any member's clear unlocks the next one. A new map
-- replaces the old one when it ends. Times are Unix milliseconds.
CREATE TABLE quest_maps (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    guild        TEXT    NOT NULL,              -- lower case
    name         TEXT    NOT NULL,
    style        TEXT    NOT NULL,              -- parchment, hex, overworld, oryx, constellation
    variant      INTEGER NOT NULL,              -- color variation 0..3
    dungeons     TEXT    NOT NULL,              -- JSON array of dungeon names, in order
    clears       TEXT    NOT NULL DEFAULT '[]', -- JSON array of {"userId", "ign", "at"}, one per cleared stage
    starts_at    INTEGER NOT NULL,
    ends_at      INTEGER NOT NULL,
    completed_at INTEGER
);
CREATE INDEX quest_maps_guild ON quest_maps (guild, starts_at);
