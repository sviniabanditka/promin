# Data model

Storage is one SQLite file (`PROMIN_DB_PATH`, default `/data/promin.db`) opened
with `modernc.org/sqlite` (no cgo). Everything else on disk is a regenerable
cache: `/data/img` (TMDB images), `/data/torrents` (torrent data),
`/data/remux` (ffmpeg job output), `/data/veoveo.json.gz`. Nightly
`VACUUM INTO` snapshots land in `/data/backups`.

## 1. Connection and migrations

`server/internal/store/db.go` opens the database with per-connection DSN pragmas:

```
journal_mode=WAL  synchronous=NORMAL  foreign_keys=1  busy_timeout=5000
```

The pool holds 4 connections: WAL lets reads run beside the single writer, and
`busy_timeout` absorbs writer-vs-writer contention (timecode writes during
playback, the sync poll from every TV, the nightly backup).

Migrations are numbered `.sql` files embedded from
`server/internal/store/migrations/` and applied in order inside a transaction,
tracked with `PRAGMA user_version` (`migrations.go`). Current chain:

| # | File | Adds |
|---|---|---|
| 1 | `0001_init.sql` | `tmdb_cache` |
| 2 | `0002_auth_sync.sql` | `users`, `sessions`, `bookmarks`, `playlists`, `playlist_items`, `history`, `timecodes`, `user_settings` |
| 3 | `0003_timecode_media_type.sql` | `timecodes.media_type` (backfilled `'movie'`) |
| 4 | `0004_torrent_cache.sql` | `torrent_cache_meta` |
| 5 | `0005_pin_profiles.sql` | `users.pin_lookup` + partial unique index |

## 2. Tables

### Accounts and devices

```sql
CREATE TABLE users (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    login      TEXT NOT NULL UNIQUE,       -- profile display name
    pass_hash  TEXT NOT NULL,              -- argon2id PHC string (only meaningful for the admin)
    created_at INTEGER NOT NULL,           -- unix seconds
    pin_lookup TEXT                        -- hex(HMAC-SHA256(pin_secret, pin)); NULL = no PIN
);
CREATE UNIQUE INDEX idx_users_pin ON users(pin_lookup) WHERE pin_lookup IS NOT NULL;

CREATE TABLE sessions (
    token       TEXT PRIMARY KEY,          -- base64url(32 random bytes), opaque
    user_id     INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    device_name TEXT NOT NULL DEFAULT '',
    device_type TEXT NOT NULL DEFAULT '',  -- 'tv' (PIN login) | 'admin' (admin panel)
    created_at  INTEGER NOT NULL,
    last_seen   INTEGER NOT NULL
);
CREATE INDEX idx_sessions_user ON sessions(user_id);
```

- A **user** is a household profile. The admin is user `id = 1` by convention
  (`User.IsAdmin()`); there is no role column. It is created or its password
  reset from `PROMIN_ADMIN_PASSWORD` on boot, cannot be deleted, and has no PIN
  by default.
- A **PIN** is 6 digits. It is never stored; `pin_lookup` is a keyed HMAC so the
  PIN resolves to exactly one profile in O(1) and a leaked database cannot be
  brute-forced without the secret. `NULL` means the profile cannot be entered.
- A **session** is a device. `GET /api/v1/auth/devices` lists a profile's
  sessions; revoking a device deletes the row. TV sessions never expire;
  `device_type='admin'` sessions expire 2 h after `created_at`. `last_seen` is
  written at most once per 5 minutes per token.

### Per-profile library (`user_id` scoped)

```sql
CREATE TABLE bookmarks (
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    tmdb_id INTEGER NOT NULL,
    media_type TEXT NOT NULL CHECK (media_type IN ('movie','tv')),
    added_at INTEGER NOT NULL,
    PRIMARY KEY (user_id, tmdb_id, media_type)
);
CREATE INDEX idx_bookmarks_user_added ON bookmarks(user_id, added_at DESC);

CREATE TABLE playlists (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);
CREATE INDEX idx_playlists_user ON playlists(user_id);

CREATE TABLE playlist_items (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    playlist_id INTEGER NOT NULL REFERENCES playlists(id) ON DELETE CASCADE,
    tmdb_id INTEGER NOT NULL,
    media_type TEXT NOT NULL CHECK (media_type IN ('movie','tv')),
    position INTEGER NOT NULL DEFAULT 0,   -- append order (max+1)
    added_at INTEGER NOT NULL,
    UNIQUE (playlist_id, tmdb_id, media_type)
);
CREATE INDEX idx_playlist_items_playlist ON playlist_items(playlist_id, position);

CREATE TABLE history (                      -- append-only log
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    tmdb_id INTEGER NOT NULL,
    media_type TEXT NOT NULL CHECK (media_type IN ('movie','tv')),
    season INTEGER,                         -- NULL for movies
    episode INTEGER,                        -- NULL for movies
    watched_at INTEGER NOT NULL
);
CREATE INDEX idx_history_user_watched ON history(user_id, watched_at DESC);
CREATE INDEX idx_history_user_title ON history(user_id, tmdb_id, media_type);

CREATE TABLE timecodes (                    -- one live row per (profile, title, season, episode)
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    tmdb_id INTEGER NOT NULL,
    season INTEGER NOT NULL DEFAULT 0,      -- 0 for movies
    episode INTEGER NOT NULL DEFAULT 0,     -- 0 for movies
    position_sec REAL NOT NULL,
    duration_sec REAL NOT NULL,
    updated_at INTEGER NOT NULL,            -- CLIENT clock, unix seconds
    media_type TEXT NOT NULL DEFAULT 'movie' CHECK (media_type IN ('movie','tv')),
    PRIMARY KEY (user_id, tmdb_id, season, episode)
);
CREATE INDEX idx_timecodes_user_updated ON timecodes(user_id, updated_at DESC);

CREATE TABLE user_settings (                -- key/value per profile
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    key TEXT NOT NULL,
    value TEXT NOT NULL,
    updated_at INTEGER NOT NULL,
    PRIMARY KEY (user_id, key)
);
```

Deleting a profile cascades through all of these.

### Caches

```sql
CREATE TABLE tmdb_cache (                   -- generic key → JSON with TTL
    key TEXT PRIMARY KEY,
    json TEXT NOT NULL,
    expires_at INTEGER NOT NULL
);
CREATE INDEX idx_tmdb_cache_expires ON tmdb_cache(expires_at);

CREATE TABLE torrent_cache_meta (           -- bookkeeping for the on-disk torrent LRU
    infohash TEXT PRIMARY KEY,
    name TEXT NOT NULL DEFAULT '',          -- on-disk name under /data/torrents (anacrolix stores by name)
    size INTEGER NOT NULL DEFAULT 0,
    last_access INTEGER NOT NULL
);
CREATE INDEX idx_torrent_cache_last_access ON torrent_cache_meta(last_access);
```

Despite its name, `tmdb_cache` is the shared KV table for every cached upstream
answer. Key prefixes and TTLs:

| Prefix | Owner | TTL |
|---|---|---|
| `list:…` (trending, discover, search, backdrops, recommendation lanes) | `catalog` | 6 h; expired rows pruned daily |
| `title:<type>:<id>:<lang>`, `title:tv:<id>:season:<n>:<lang>`, `videos:…` | `catalog` | 7 d; kept past expiry for stale-ok reads |
| `genres:<type>:<lang>` | `catalog` | 30 d |
| `omdb:<imdb_id>` | `catalog` | 7 d |
| `nsrc:match:v<N>:<provider>:<type>:<tmdb_id>` | `sources` | 7 d for a hit, 15 min for a miss; `N` is bumped when matching logic changes |
| `weather:place:…`, `weather:fc:…`, `weather:geoip:<ip>` | `weather` | 30 d / 30 min / 7 d |

Rows are read regardless of `expires_at`; callers decide whether a stale row is
acceptable (`TMDBCacheRepo.Get`).

## 3. Per-user vs per-device

| Data | Scope | Notes |
|---|---|---|
| Bookmarks, playlists, history, timecodes, settings | **profile** (`user_id`) | Every device logged into the same PIN sees the same state |
| Sessions | **device** | One row per PIN entry; carries `device_name`/`device_type` only |
| "Old TV mode", diagnostics mode, local UI caches | **device**, client-side | Kept in the browser's `localStorage`; the server never stores per-device settings |
| `tmdb_cache`, `torrent_cache_meta`, torrent files, images | **global** | Shared by all profiles; the prewarm loop covers every profile's library |

`user_settings` is per profile. Anything that must differ between two TVs of the
same profile stays on the device.

## 4. Sync semantics

The server is the source of truth; clients keep a local cache for instant paint
and reconcile against the server. `server/internal/sync/service.go` publishes an
event on every mutation; `hub.go` fans it out.

**Bootstrap.** `GET /api/v1/sync/bootstrap` returns all bookmarks, all
playlists (with item counts), the last 50 history rows, the 50 most recently
updated timecodes, all settings and a `cursor` (id of the newest event for the
profile). The client replaces its local cache with this snapshot.

**Push.** `GET /api/v1/ws?t=` subscribes the connection to the profile's hub.
Events are `{id, type, payload}` with types `bookmark_added`,
`bookmark_removed`, `playlist_created`, `playlist_updated` (also used for
delete, `{id, deleted: true}`), `playlist_item_added`, `playlist_item_removed`,
`history_added`, `timecode_updated`, `settings_updated`. A slow subscriber
(32-event buffer full) drops events rather than blocking the publisher. Client →
server traffic is only `{"type":"ping"}` keepalives; mutations always go
through REST.

**Poll fallback.** `GET /api/v1/sync/events?since=<cursor>` returns events with
`id > cursor` from the in-memory per-profile journal (1 h TTL). If the cursor
predates the journal the response is `410 Gone` and the client re-bootstraps.
Event ids are global and monotonic for the process lifetime; they reset on
restart, which also forces a bootstrap.

**Bookmarks.** `INSERT OR IGNORE` on the composite key — adding twice is a
no-op (`200` instead of `201`) and publishes nothing. Removal always publishes.

**History.** Append-only. A row is written when the client reports a watch fact;
rows are never updated or merged, and there is no retention job.

**Timecodes.** Last-write-wins on the *client* `updated_at`:

```sql
INSERT INTO timecodes (...) VALUES (...)
ON CONFLICT (user_id, tmdb_id, season, episode) DO UPDATE SET
    media_type = excluded.media_type, position_sec = excluded.position_sec,
    duration_sec = excluded.duration_sec, updated_at = excluded.updated_at
WHERE excluded.updated_at >= timecodes.updated_at
RETURNING ...;
```

Two devices on the same episode converge on the later local event, not on the
request that reached the server last. The response is
`{accepted, position_sec, updated_at}`: when `accepted=false` the client adopts
the server's values. Only an accepted write publishes `timecode_updated`, with
the full row. `media_type` defaults to `movie` when omitted.

Derived views (`store/timecodes_repo.go`): *continue watching* = newest row per
title with `position < 90 %` of duration (a series whose latest episode is
finished stays for 30 days); *finished* = titles with `position >= 90 %`, used
as recommendation seeds; the title screen attaches the newest timecode for the
title so "Continue" works even after the client cache evicted it.

**Settings.** Plain upsert per key; every write publishes
`settings_updated {key, value}`. No conflict resolution beyond "last request
wins".

## 5. Disk data lifecycle

| Data | Where | Cleanup |
|---|---|---|
| SQLite + WAL | `/data/promin.db` | Nightly `VACUUM INTO` snapshot, keep 7 |
| TMDB images | `/data/img/<size>/<file>` | Never (permanent); 404s remembered 1 h in memory |
| Torrent data | `/data/torrents/<name>` | LRU by `torrent_cache_meta.last_access` down to 90 % of `PROMIN_TORRENT_CACHE_LIMIT_GB`; active readers pin a torrent; untracked files swept on boot |
| Remux output | `/data/remux/<job>/` | Deleted after `PROMIN_REMUX_JOB_TTL` idle; all wiped on boot |
| Provider id map | `/data/veoveo.json.gz` | Re-downloaded when older than a week |
| PIN secret | `/data/pin_secret` (0600) | Never; deleting it invalidates every PIN |

## telegram_links

`telegram_links(chat_id PRIMARY KEY, user_id → users ON DELETE CASCADE, created_at)` — Telegram chats linked to a profile (docs/telegram.md). Link codes themselves are in memory only (10-minute TTL).
