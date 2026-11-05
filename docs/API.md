# Loot Gobbler Server API (v1)

JSON over HTTPS. All paths start with `/api/v1`. Times are Unix milliseconds.

## Conventions

**Authentication.** Log in to get a session token and send it on every request:

```
Authorization: Bearer lgs_...
```

A session lasts `SESSION_DAYS` (default 30) after its last use. `401` always means "log in again".

**Errors** look like this:

```json
{ "error": "invalid_credentials", "message": "Wrong IGN or password" }
```

`error` is a stable code for the client; `message` can be shown to the player. `429` responses also carry `retryAfter` (seconds) and a `Retry-After` header.

**User object** (`user` in many responses):

```json
{ "id": 1, "ign": "Gunshit", "status": "active", "role": "admin", "root": true, "createdAt": 1793551823006, "lastLoginAt": 1793551900000 }
```

`status` is `pending` or `active`. `role` is `user`, `moderator` or `admin`. `root` marks the root admin.

**Role tokens** look like `LGA-XXXX-XXXX-XXXX-XXXX-XXXX-XXXX-XXXX-XXXX` (`LGA` = admin, `LGM` = moderator). The server ignores case, spaces and dashes, and reads `O` as `0` and `I`/`L` as `1`, so players can type them loosely.

## Public

| Method | Path | |
|---|---|---|
| GET | `/health` | `{"ok": true}`. Works for any host name. |
| GET | `/status` | `{"service": "loot-gobbler-server", "version": "0.1.0", "apiVersion": 1, "adminClaimed": true, "registration": "approval"}` |

Clients should check `apiVersion` when connecting.

### `POST /ping`

This is the client's connection test. It needs no body, and you can send a session token with it. Each call writes a `connection test` line to the server log, so the host sees it arrive. Limit: 10 per minute per IP.

```json
{ "ok": true, "serverTime": 1793551900000, "yourIp": "203.0.113.5", "user": { ... } }
```

- `yourIp` is the address the server sees for you. If it is a private address (e.g. `172.18.0.1`) even though you connect over the internet, the proxy isn't passing on the real IP; check `TRUSTED_PROXIES`.
- `user` is only included when a valid session token was sent.

## Accounts

### `POST /auth/register`

```json
{ "ign": "Gunshit", "password": "at least 8 characters", "token": "LGA-... (optional)" }
```

- `202` + `{"user": ...}` with `status: "pending"`: waiting for approval
- `201` + `{"user": ...}` with `status: "active"`: a valid token approved the account and gave it the token's role
- `400 bad_request` (IGN must be 1–10 letters, password 8–128 characters), `400 invalid_token`, `409 ign_taken`, `429 rate_limited`

### `POST /auth/login`

```json
{ "ign": "Gunshit", "password": "...", "client": "Loot Gobbler v0.5.0 (optional)" }
```

- `200` + `{"token": "lgs_...", "expiresAt": ..., "user": ...}`
- `401 invalid_credentials`, `403 pending_approval`, `429 rate_limited`

IGNs are case-insensitive.

### Logged in

| Method | Path | Body | Result |
|---|---|---|---|
| POST | `/auth/logout` | | `204`: this session ends |
| GET | `/me` | | `{"user": ...}` |
| PATCH | `/me/password` | `{"currentPassword", "newPassword"}` | `204`; other sessions are signed out. `403 wrong_password` |
| DELETE | `/me` | `{"password"}` | `204`. `403 root_protected` for the root admin, `403 wrong_password` |
| POST | `/me/redeem` | `{"token"}` | `{"user": ...}` with the new role. `400 invalid_token`, `409 role_already_held`, `429` |

## Player data

The server is the main store. Each kind of data (`collection`, `characters`, `exalts`, `pets`, `dps_history`, ...) is one JSON document per player with a `revision` that increases by 1 on every write. A kind name is 1–32 lower case letters, digits or `_`; a player can have at most 32 kinds.

**On connect:** ask what the server has, then fetch what you need.

```
GET /me/data?meta=true                → revisions only
GET /me/data?kinds=collection,pets    → those documents with data
GET /me/data                          → everything
```

```json
{
  "seq": 1234,
  "items": {
    "collection": { "revision": 7, "updatedAt": 1793551900000, "data": { "14410": { "count": 1 } } },
    "pets":       { "revision": 2, "updatedAt": 1793551800000 }
  }
}
```

`seq` is the server's newest change number (used for live updates later).

**Uploading:** send the whole document with the revision you based it on (`0` for a new document).

```
PUT /me/data/collection
{ "baseRevision": 7, "data": { ... } }
```

- `200` + `{"kind": "collection", "revision": 8, "updatedAt": ..., "seq": 1235}`
- `409 revision_conflict` + `"current": {"revision", "updatedAt", "data"}`: someone (for example the same player on another PC) wrote in between. Merge `current.data` with your local changes and retry with `baseRevision: current.revision`.
- `413 too_large` above 16 MiB

## Guild views

Players are grouped by the in-game guild their client reports in the `profile` document (`{"ign", "guild", "guildRank", "updatedAt"}`, synced like any other kind). Guild names are compared ignoring case and are shown as the highest-ranked member spells them. Everything is computed from members' synced documents, and each player only sees their own guild. Drops (recent drops, loot caravan, drop quests) come from the server's drop log instead: every drop a client has uploaded in its `collection` is kept there, so a player clearing or editing their collection later does not take drops away. Until a player's guild is known, these endpoints answer `409 no_guild`.

| Method | Path | Result |
|---|---|---|
| GET | `/guild` | `{"guild", "members": [{"id", "ign", "guildRank", "lastActive"}]}` |
| GET | `/guild/leaderboard` | `{"guild", "members": [...]}`: each member plus `points` (quest points this month), `items` (different items), `finds` (all copies), `fame` (living characters), `exalts`, `characters` (living), `maxed` (living with 8/8). Sorted by `items`. |
| GET | `/guild/drops?limit=50` | `{"guild", "drops": [{"userId", "ign", "itemId", "time", "source", "enchants", "enchantIds", "where", "bag"}]}`: newest drops first, at most 200. Only drops, no vault/inventory sightings. `time` is the dropping player's local time. |
| GET | `/guild/members/{id}` | `{"guild", "member": <leaderboard row>, "characters": {...}, "exalts": {...}, "collection": {"<item id>": count}}`. Characters are as the client stores them, without `samples`, `slots` and `slotEnchantIds`. `404` for players outside your guild. |

| GET | `/guild/caravan?range=week` | `{"guild", "range", "members": [...]}`: each member (`id`, `ign`, `guildRank`, `lastActive`) plus `byBag` (items found per bag color: `{"White": 2, "Orange": 1, ...}`; boosted bags count as their color, `Other` when unknown), `total` (all items found), `items` (`[{"itemId", "enchants", "bag", "time", "where"}]`, newest first, at most 120; `enchants` is -1 when the dropping client did not know it), `lastDrop` and `character` (`{"classType", "className", "skin", "tex1", "tex2", "fame"}`, the living character with the most fame gained in the range, else the one played last; missing if none is known). `range` is `week` (7 days, the default), `month` (30 days) or `all`. Drops only; every item counts, also several out of one bag. Sorted by `total`. |
| GET | `/guild/questmaps?limit=10` | `{"guild", "now", "current", "past": [...]}`: the guild's quest maps, newest first (`past` at most `limit`, up to 50). A map is `{"id", "name", "style", "variant", "dungeons": [names in order], "clears": [{"userId", "ign", "at"}], "startsAt", "endsAt", "completedAt"}`. `style` is `parchment`, `hex`, `overworld`, `oryx`, `constellation`, `neon`, `adventure`, `hunt` or `hellforge`; `variant` is its color variation 0-3. When there is no map or the last one has ended, a new one is rolled: random style, variant, 3-8 dungeons (easiest first) and 6-12 days. It never repeats the style or the color family (ice, fire, green, ...) of any of the last 3 maps. Stage *n* is cleared by the earliest run of its dungeon by any member (from their synced `dps_history`, not the guild hall, with their own boss damage) that started after stage *n-1* was cleared and before the map ended. |
| GET | `/guild/looks?dungeon=<name>&from=<ms>&to=<ms>` | `{"looks": [{"player", "classType", "skin", "tex1", "tex2", "source", "alive", "lastPlayed"}]}`: how your guildmates (not you) looked in a run. `source` `visit`: the look their own client saved with their synced visit of that dungeon overlapping the time (a minute of slack). Members without such a visit get one entry per character as it looks now (`source` `character`, with `alive` and `lastPlayed`). `400` without the three parameters. |

`guildRank` is the game's value: 0 initiate, 10 member, 20 officer, 30 leader, 40 founder.

## Quests

Every week, from Monday 00:00 UTC, each player rolls up to 3 **personal quests** from the accepted personal quests. Each guild gets 1 **guild quest**, the same for all its members. Progress is computed from what players sync, counting only what happened during that week. A finished quest gives its points once: personal quests to the player, guild quests to every member who contributed. Points count per calendar month (UTC), and the leaderboard has a `points` column for the current month.

The server checks quests whenever someone opens their quests or the leaderboard. It also checks the previous week once more, so data synced just after Monday still counts.

Quest types (`params.count` is required; `dungeon` is part of the dungeon name, ignoring case):

| `type` | Counts | Optional params |
|---|---|---|
| `drops` | UT/ST drops (not vault/inventory sightings) | `dungeon`, `minEnchants` (0–4), `bag` (color) |
| `new_items` | items whose first find is in the week | |
| `dungeon_clears` | dungeon runs from the DPS history (not the guild hall) | `dungeon` |
| `boss_damage` | your damage to bosses | `dungeon` |
| `fame` | fame gained, over all characters | |

Finds carry `at` (exact time, epoch ms) since client 0.5; older finds without it fall back to their local `time`, read as UTC.

| Method | Path | Who | Result |
|---|---|---|---|
| GET | `/quests` | any | `{"weekStart", "weekEnd", "personal": [quest], "guild", "guildQuests": [quest], "period": "2026-11", "points"}`. A quest is `{"id", "title", "type", "params", "points", "progress", "target", "completedAt"}`; guild quests also have `contributors` (`[{"ign", "amount"}]`, most first). `guild` is missing while the player's guild is unknown; personal quests still work. |
| POST | `/quests/suggestions` | any | `{"title" (optional, made from the rest), "type", "params", "scope": "personal" or "guild", "points" (optional)}` → `201` + the suggested quest, waiting for an admin. At most 10 waiting per player (`409 too_many_suggestions`). |
| GET | `/admin/quests?status=` | admin | `{"quests": [{"id", "title", "type", "params", "scope", "points", "status", "proposedBy", "createdAt", "decidedBy", "decidedAt"}]}`; `status` is `pending`, `approved` or `denied` (all if missing). New servers start with 12 built-in suggestions (`proposedBy` empty). |
| POST | `/admin/quests/{id}/approve` | admin | `{"scope": "personal" or "guild", "points": 1–500 (0 = keep)}` → `204`. The quest joins the weekly rotation; empty slots of the current week fill up on the next check. |
| POST | `/admin/quests/{id}/deny` | admin | `204`. Denies a suggestion, or takes an accepted quest out of the rotation (quests already running this week keep going). |

## Moderation (moderator or admin)

| Method | Path | Who | Result |
|---|---|---|---|
| GET | `/admin/requests` | moderator+ | `{"users": [...]}`: pending registrations, oldest first |
| POST | `/admin/requests/{id}/accept` | moderator+ | `{"user": ...}` |
| POST | `/admin/requests/{id}/deny` | moderator+ | `204`; the request is deleted |
| GET | `/admin/users` | moderator+ | `{"users": [...]}`: active accounts by name |
| DELETE | `/admin/users/{id}` | moderator+ | `204`. Only for users ranked below you; never the root admin. `403 insufficient_rank` |
| DELETE | `/admin/users/{id}/sessions` | moderator+ | `204`: signs that user out everywhere (same rank rules) |
| PATCH | `/admin/users/{id}/role` | admin | `{"role": "user"}` demotes a moderator. Only the root admin may set `moderator` or `admin` directly or change admins. `403 root_only` / `insufficient_rank` |
| GET | `/admin/moderator-tokens` | admin | `{"tokens": [{"id", "note", "createdBy", "createdAt", "expiresAt"}]}`: unused, unexpired tokens |
| POST | `/admin/moderator-tokens` | admin | Body optional: `{"note": "for Bob", "validHours": 24}` (1–168). `201` + `{"id", "token", "note", "expiresAt"}`. The token is shown only here. |
| DELETE | `/admin/moderator-tokens/{id}` | admin | `204`, or `404` if already used or revoked |

Ranks: user < moderator < admin < root admin. Nobody can act on themselves through `/admin/users`; use `/me` instead.
