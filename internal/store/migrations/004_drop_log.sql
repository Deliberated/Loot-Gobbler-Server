-- Every drop a player's client has reported, kept for good: clearing or editing the collection on the client later
-- does not take drops away from the guild views (loot caravan, recent drops, quests), which read this table.
CREATE TABLE drop_log (
    user_id INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    item_id INTEGER NOT NULL,
    time    TEXT    NOT NULL,           -- the player's local time, as the client sent it
    place   TEXT    NOT NULL DEFAULT '',
    bag     TEXT    NOT NULL DEFAULT '',
    copy    INTEGER NOT NULL DEFAULT 0, -- identical drops (same item, time, place and bag): 0, 1, ...
    data    TEXT    NOT NULL,           -- the find as the client sent it (JSON)
    PRIMARY KEY (user_id, item_id, time, place, bag, copy)
);
CREATE INDEX drop_log_user_time ON drop_log (user_id, time);
