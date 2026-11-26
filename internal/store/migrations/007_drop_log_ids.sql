-- Every logged drop gets a number that only goes up and is never reused (not even when an account and its drops are
-- deleted), so clients can keep a copy of their guild's drops and ask only for what came after the last one they have.
CREATE TABLE drop_log_ids (
    id      INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    item_id INTEGER NOT NULL,
    time    TEXT    NOT NULL,           -- the player's local time, as the client sent it
    place   TEXT    NOT NULL DEFAULT '',
    bag     TEXT    NOT NULL DEFAULT '',
    copy    INTEGER NOT NULL DEFAULT 0, -- identical drops (same item, time, place and bag): 0, 1, ...
    data    TEXT    NOT NULL,           -- the find as the client sent it (JSON)
    UNIQUE (user_id, item_id, time, place, bag, copy)
);
INSERT INTO drop_log_ids (user_id, item_id, time, place, bag, copy, data)
    SELECT user_id, item_id, time, place, bag, copy, data FROM drop_log ORDER BY rowid;
DROP TABLE drop_log;
ALTER TABLE drop_log_ids RENAME TO drop_log;
CREATE INDEX drop_log_user_time ON drop_log (user_id, time);

-- This server's database, told apart from any other (or from itself started over): a client's copy of the drops is
-- only good for the database it came from.
INSERT INTO settings (key, value, updated_at) VALUES ('instance', '"' || lower(hex(randomblob(8))) || '"', 0);
