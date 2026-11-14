-- One-time codes a moderator or admin (or the host, in the console) makes for a player who forgot their password.
-- Only the code's hash is kept. A player has at most one unused code: a new one replaces it.
CREATE TABLE password_resets (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id    INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    code_hash  TEXT    NOT NULL UNIQUE,
    created_by INTEGER REFERENCES users (id) ON DELETE SET NULL, -- NULL: made in the server console
    created_at INTEGER NOT NULL,
    expires_at INTEGER NOT NULL,
    used_at    INTEGER
);

CREATE INDEX password_resets_user ON password_resets (user_id);
