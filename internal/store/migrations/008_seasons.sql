-- The game's seasons, as players' clients read them from the game (its mission seasons): the id, the name, when it
-- ends, and when a client first reported it. The guild views split everything at season changes.
CREATE TABLE seasons (
    id         INTEGER PRIMARY KEY,           -- the game's id of the season
    name       TEXT    NOT NULL,
    end_at     INTEGER NOT NULL,              -- when the game says it ends (epoch ms); moves when the game moves it
    first_seen INTEGER NOT NULL,              -- the first report of it (epoch ms)
    first_by   INTEGER REFERENCES users (id) ON DELETE SET NULL,
    updated_at INTEGER NOT NULL
);
