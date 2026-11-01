-- Times are Unix milliseconds.

CREATE TABLE users (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    ign           TEXT    NOT NULL UNIQUE COLLATE NOCASE,
    password_hash TEXT    NOT NULL,
    status        TEXT    NOT NULL CHECK (status IN ('pending', 'active')),
    role          TEXT    NOT NULL DEFAULT 'user' CHECK (role IN ('user', 'moderator', 'admin')),
    is_root       INTEGER NOT NULL DEFAULT 0,
    created_at    INTEGER NOT NULL,
    approved_at   INTEGER,
    approved_by   INTEGER REFERENCES users (id) ON DELETE SET NULL,
    last_login_at INTEGER
);

-- At most one root admin can ever exist.
CREATE UNIQUE INDEX users_one_root ON users (is_root) WHERE is_root = 1;

CREATE TABLE sessions (
    token_hash   TEXT PRIMARY KEY,
    user_id      INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    created_at   INTEGER NOT NULL,
    last_used_at INTEGER NOT NULL,
    expires_at   INTEGER NOT NULL,
    client       TEXT
);
CREATE INDEX sessions_user ON sessions (user_id);

-- Single-use tokens that grant a role. created_by NULL = admin claim token printed in the server console.
CREATE TABLE role_tokens (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    token_hash TEXT    NOT NULL UNIQUE,
    kind       TEXT    NOT NULL CHECK (kind IN ('admin', 'moderator')),
    note       TEXT,
    created_by INTEGER REFERENCES users (id) ON DELETE CASCADE,
    created_at INTEGER NOT NULL,
    expires_at INTEGER,
    used_by    INTEGER REFERENCES users (id) ON DELETE SET NULL,
    used_at    INTEGER
);

-- One JSON document per user and kind (collection, characters, ...). revision increases by 1 on every write.
CREATE TABLE user_data (
    user_id    INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    kind       TEXT    NOT NULL,
    revision   INTEGER NOT NULL,
    data       TEXT    NOT NULL,
    updated_at INTEGER NOT NULL,
    PRIMARY KEY (user_id, kind)
);

-- Every write to user_data, in order. Lets clients catch up from a sequence number (live updates later).
CREATE TABLE changes (
    seq      INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id  INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    kind     TEXT    NOT NULL,
    revision INTEGER NOT NULL,
    at       INTEGER NOT NULL
);
