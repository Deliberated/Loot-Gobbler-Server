# Loot Gobbler Server

The backend for [Loot Gobbler](https://github.com/Deliberated/Loot-Gobbler-Client). It has no web interface: the Loot Gobbler client is the frontend. The server stores accounts and each player's data, so players can see guild stats, challenges and so on.

- Single Go binary in a ~15 MB Docker image (amd64 and arm64)
- All data is in one SQLite file in the `/data` volume
- New accounts wait for approval by a moderator or admin
- Roles: user, moderator, admin. The first admin (root admin) claims the server with a token printed in the console.
- Passwords are hashed with Argon2id; tokens are stored only as hashes
- Brute-force limits on logins, registrations and tokens

## Hosting with Docker and nginx proxy manager

1. Copy [docker-compose.yml](docker-compose.yml) and [.env.example](.env.example) into one folder on your server. Rename `.env.example` to `.env` and set `ALLOWED_HOSTS` to your domain. `docker compose` reads `.env` automatically, and git ignores it.
2. Start it: `docker compose up -d`
3. In nginx proxy manager, add a proxy host:
   - **Domain:** your domain (the same one as in `ALLOWED_HOSTS`)
   - **Forward to:** `http://<server IP>:8080`, or `http://loot-gobbler:8080` if both containers share a Docker network (see the comments in the compose file)
   - **SSL:** request a Let's Encrypt certificate and turn on *Force SSL*
   - **Websockets Support:** on (needed later for live updates)
4. Check it: `https://<your domain>/api/v1/status` should show `"adminClaimed": false`.

### Claiming the server (becoming admin)

On first start, the server prints a single-use token in its log:

```
docker logs loot-gobbler
```

```
==========================================================================
  ADMIN CLAIM TOKEN (single use)

      LGA-XXXX-XXXX-XXXX-XXXX-XXXX-XXXX-XXXX-XXXX
...
```

In Loot Gobbler, register on your server and paste the token into the token field. Your account is approved right away and becomes **root admin**, which can never be removed or demoted. If you already have an account, enter the token under Settings > Server instead.

The token is printed again on every restart until an admin exists. To get a new one at any time (it replaces any older unused one, and makes another admin if a root admin already exists), run:

```
docker exec loot-gobbler lootgobbler admin-token
```

Only someone with access to the server machine can do this.

### Moderators

Admins create moderator tokens in the client (valid for 24 hours by default, at most 7 days, single use) and hand them out. A player enters the token when registering, which also skips the approval queue, or later in their settings.

| | user | moderator | admin | root admin |
|---|:-:|:-:|:-:|:-:|
| Use the server, sync own data | ✓ | ✓ | ✓ | ✓ |
| Accept/deny registrations, remove or sign out users | | ✓ | ✓ | ✓ |
| Create moderator tokens, remove/demote moderators | | | ✓ | ✓ |
| Promote/demote/remove admins | | | | ✓ |

### Updating

```
docker compose pull
docker compose up -d
```

Data in the volume is kept, and database changes are applied automatically on start. A container cannot go back to an older version once a newer one has changed the database. Back up the volume first if you want that option.

The GitHub package is private by default. Either make it public (GitHub > Packages > loot-gobbler-server > Package settings > Change visibility) or run `docker login ghcr.io` on the server with a token that has `read:packages`.

### Backups

Everything is in the `loot-gobbler-data` volume (`lootgobbler.db` plus its `-wal`/`-shm` files). To back it up, stop the container and copy the volume:

```
docker compose stop
docker run --rm -v loot-gobbler-data:/data -v "$PWD":/backup alpine tar czf /backup/loot-gobbler-backup.tgz -C /data .
docker compose start
```

## Settings

Set these in `.env`. `HOST_PORT` and `IMAGE_TAG` are used only by the compose file; the rest go to the server.

| Variable | Default | Meaning |
|---|---|---|
| `HOST_PORT` | `8080` | Port on the host that nginx proxy manager forwards to |
| `IMAGE_TAG` | `latest` | Image version: `latest`, a release like `0.1.0`, or `edge` |
| `ALLOWED_HOSTS` | *(any)* | Comma-separated host names the server answers for, e.g. `loot.example.com`. Others get `421`. Leaving it empty logs a warning. |
| `TRUSTED_PROXIES` | private + loopback ranges | IPs/CIDR ranges allowed to set `X-Forwarded-For`. Used to find the real client IP for rate limits. `none` trusts nobody. |
| `PORT` | `8080` | Listen port inside the container |
| `DATA_DIR` | `/data` in Docker, `./data` otherwise | Where the database lives |
| `SESSION_DAYS` | `30` | A login expires after this many days without use |
| `LOG_LEVEL` | `info` | `info`: logins, registrations, admin actions and every sync (download, upload, conflict). `debug`: also every request with status, duration and IP. `warn`: only problems. |

### Reading the log

`docker logs -f loot-gobbler` follows the log live. At `info`, syncing looks like this:

```
level=INFO msg="sync download" ign=Hoster what=data kinds=characters@1,collection@3,exalts@2,pets@1 size=58.2 KiB
level=INFO msg="sync upload" ign=Hoster kind=collection revision=4 size=24.8 KiB
level=INFO msg="sync conflict, client will merge and retry" ign=Hoster kind=collection clientRevision=3 serverRevision=4
```

A player's client downloads everything after logging in and uploads changed data at most once a minute while they play. A conflict means two of the player's PCs uploaded at the same time. The client merges and retries on its own, so it's not an error.

## Brute-force protection

| What | Limit |
|---|---|
| Wrong admin/moderator token | 3 per hour per IP, and 3 per hour per account |
| Failed login | 5 per 15 min per IP + IGN, and 20 per 15 min per IP |
| Registration requests | 3 per hour per IP |
| Wrong current password (password change, account deletion) | 5 per 15 min per account |

Tokens have 160 random bits, so they cannot be guessed even without these limits. Failed attempts are logged with the IP (`docker logs loot-gobbler`). The counters are kept in memory and reset when the server restarts.

## API

See [docs/API.md](docs/API.md).

## Development

Requires Go (see `go.mod` for the version).

```
go test ./...
go run ./cmd/lootgobbler          # serves on :8080 with data in ./data
docker build -t loot-gobbler-server .
```

Pushing to `main` publishes `ghcr.io/deliberated/loot-gobbler-server:edge`. Pushing a tag `vX.Y.Z` publishes `X.Y.Z`, `X.Y` and `latest`.
