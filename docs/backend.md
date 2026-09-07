# Backend

The backend is one Go module (`server/`, Go 1.25, `CGO_ENABLED=0`). Entry point:
`server/cmd/promin/main.go` — it reads config, opens SQLite, wires every
service, starts background loops and serves HTTP on `PROMIN_HTTP_ADDR`.
Graceful shutdown on SIGINT/SIGTERM: `http.Server.Shutdown` with a 15 s budget,
then the torrent client is closed. No `WriteTimeout` is set on the server —
`/stream` and HLS segments are long-lived responses.

## 1. Package map (`server/internal`)

| Package | Responsibility | Key files |
|---|---|---|
| `httpapi` | All HTTP: routing (`net/http` `ServeMux` with method patterns), auth middleware, JSON envelope, media proxies, WS, admin/logs/onboarding HTML, static SPA | `server.go`, `middleware.go`, `handlers_*.go`, `relay.go`, `remux.go`, `img.go`, `static.go`, `msx.go`, `admin.go`, `logs.go` |
| `auth` | Sessions (opaque 32-byte tokens), PIN login (HMAC-SHA256 lookup), admin password (argon2id PHC), in-memory rate limiters, admin bootstrap | `service.go`, `service_pin.go`, `argon2.go`, `ratelimit.go` |
| `sync` | Bookmarks/playlists/history/timecodes/settings logic, event hub with per-user 1 h journal, bootstrap + poll fallback | `service.go`, `hub.go`, `dto.go` |
| `catalog` | TMDB client with SQLite cache (SWR, single-flight, circuit breaker, fallback hosts), OMDb ratings, card normalization, home shelves, recommendations, random backdrops, library prewarm | `tmdb_client.go`, `service.go`, `normalize.go`, `recommend.go`, `backdrops.go`, `prewarm.go`, `omdb_client.go` |
| `sources` | Facade over online providers and the JacRed indexer: title matching + match cache, `/relay` and `/remux` URL encoding, torrent result normalization | `service.go`, `native.go`, `torrents.go`, `client.go`, `relay.go`, `parse.go`, `types.go` |
| `sources/provider` | Provider contract (`Provider`, `Fetcher`, `Ctx`, typed errors) plus direct and proxied fetchers | `provider.go` |
| `sources/provider` | Provider interface, fetchers (direct / residential proxy) | `provider.go` |
| `../providers/*` | Source implementations — private git submodule, built with `-tags providers` | see its README |
| `torrent` | `anacrolix/torrent` wrapper: add magnet, list files, sequential reader with readahead, idle drop, LRU eviction, orphan sweep | `manager.go`, `lru.go` |
| `remux` | In-memory ffmpeg job queue: `copy_hls`, `copy_mkv`, `transcode_hevc`; ffprobe codec/HDR/audio probing; job TTL cleanup | `queue.go`, `job.go`, `ffmpeg.go`, `probe.go`, `transcode.go`, `hls_copy.go`, `mkv_copy.go` |
| `store` | The only package with SQL: opens SQLite, runs embedded migrations, exposes repositories, `VACUUM INTO` backups | `db.go`, `migrations.go`, `migrations/*.sql`, `*_repo.go`, `backup.go` |
| `config` | Reads every `PROMIN_*` env var into `Config` | `config.go` |
| `weather` | open-meteo forecast + geocoding, GeoIP (ipwho.is, ip-api.com fallback), all cached in the shared KV table | `weather.go` |
| `logbuf` | `slog.Handler` wrapper: ring buffer of the last 5000 records + live subscribers for `/logs` | `logbuf.go` |

Dependency direction: `httpapi` → services (`auth`, `sync`, `catalog`,
`sources`, `torrent`, `remux`, `weather`) → `store`. Services never import
`httpapi`; only `store` speaks SQL; only `catalog`, `sources`, `weather` and the
`/img`+`/relay` handlers make outbound HTTP calls.

## 2. HTTP surface

All routes are registered in `server/internal/httpapi/server.go`.

| Group | Routes | Auth |
|---|---|---|
| Health / meta | `GET /healthz`, `GET /api/v1/ping` (version, `h1_host`, `main_host`), `POST /api/v1/diag` (client diagnostics → server log, 16 KiB cap) | open |
| Auth | `POST /api/v1/auth/pin` (open); `POST /api/v1/auth/logout`, `GET /api/v1/auth/devices`, `DELETE /api/v1/auth/devices/{token_id}` | bearer |
| Catalog | `GET /api/v1/catalog/{home,list,search,genres,backdrops}`, `GET /api/v1/catalog/title/{tmdb_id}` | bearer |
| Weather | `GET /api/v1/weather` | bearer |
| Online sources | `GET /api/v1/sources/online`, `GET /api/v1/sources/online/resolve` | bearer |
| Torrents | `GET /api/v1/sources/torrents`, `GET|POST /api/v1/torrents/add`, `GET /api/v1/torrents/active`, `GET /api/v1/torrents/audio`, `DELETE /api/v1/torrents/{infohash}` | bearer |
| Sync data | `/api/v1/bookmarks`, `/api/v1/playlists[/{id}/items]`, `/api/v1/history`, `/api/v1/timecodes[/continue|/{tmdb_id}]`, `/api/v1/settings[/{key}]`, `GET /api/v1/sync/bootstrap`, `GET /api/v1/sync/events?since=` | bearer |
| WS | `GET /api/v1/ws` | bearer or `?t=` |
| Media | `GET /relay?u=`, `GET /stream/{infohash}/{fileIdx}`, `GET /remux?u=&kind=&audio=&start=`, `GET /remux/{job}/{file}` | bearer or `?t=` |
| Images | `GET /img/{size}/{file}` | open (paths only discoverable via the gated catalog) |
| MSX / pages | `GET /msx/start.json`, `GET /onboarding`, `GET /` + SPA fallback | open |
| Admin | `GET /admin`, `POST /admin/login`, `POST /admin/logout`, `GET|POST /admin/profiles`, `PATCH|DELETE /admin/profiles/{id}` | `promin_admin` cookie (admin session, 2 h) |
| Logs | `GET /logs`, `GET /logs/api/history`, `GET /logs/api/stream` (SSE) | Basic Auth `PROMIN_LOGS_PASSWORD`; unregistered when empty |

Conventions:

- Errors are `{"error": {"code", "message"}}`; messages are Ukrainian UI text.
- Request bodies are capped at 1 MiB (`withLogging`), which also recovers panics.
- `requireAuth` reads only the `Authorization` header; `requireAuthMedia` also
  reads `?t=`. Every child URL the server hands to a player (`/relay` manifest
  lines, `/remux` playlists, the loopback `/stream` URL ffmpeg reads) is passed
  through `withMediaToken` (`hls_token.go`) so it carries the token.
- `/relay` and `/remux` refuse loopback, link-local, unspecified and private IP
  literals as upstream (`validateUpstream`), follow at most 10 redirects and
  re-validate each hop.
- ffmpeg/ffprobe never read torrent files from disk; they read
  `http://127.0.0.1:<port>/stream/...` so bytes arrive in order and complete.

## 3. Configuration (`server/internal/config/config.go`)

All configuration is environment variables. Durations use Go syntax (`30s`, `24h`).

| Variable | Default | Meaning |
|---|---|---|
| `PROMIN_HTTP_ADDR` | `:8080` | Listen address |
| `PROMIN_METRICS_ADDR` | `:9100` | Separate Prometheus `/metrics` listener (`promin_*`); empty disables it. Never on the public mux |
| `PROMIN_DATA_DIR` | `/data` | Root for `img/`, `torrents/`, `remux/`, `backups/`, `pin_secret`, `veoveo.json.gz` |
| `PROMIN_DB_PATH` | `$PROMIN_DATA_DIR/promin.db` | SQLite file |
| `PROMIN_LOG_LEVEL` | `info` | slog level: `debug`, `info`, `warn`, `error` |
| `PROMIN_LOGS_PASSWORD` | empty | Enables `/logs` (Basic Auth). Empty → routes not registered |
| `PROMIN_ADMIN_PASSWORD` | empty | Bootstraps/resets user 1 (admin) password on boot. Empty → admin left as-is |
| `PROMIN_PIN_SECRET` | empty | HMAC key for PIN lookup hashes. Empty → random 32 bytes generated once at `$DATA_DIR/pin_secret`. Rotating it invalidates every PIN |
| `PROMIN_TMDB_API_KEY` | — (required) | TMDB v3 key; the binary exits at start without it. Production: secret `promin-secrets/tmdb-api-key` |
| `PROMIN_TMDB_BASE_URL` | `https://api.themoviedb.org/3` | Primary TMDB host |
| `PROMIN_TMDB_FALLBACK_URLS` | none (`-`) | Comma-separated full TMDB mirrors tried in order after the primary fails |
| `PROMIN_OMDB_KEY` | empty | OMDb key for `imdb_rating`; empty disables the feature |
| `PROMIN_JACRED_BASE_URL` | `http://jac.red` | JacRed-compatible torrent indexer |
| `PROMIN_JACRED_APIKEY` | empty | `apikey` query param for the indexer |
| `PROMIN_FFMPEG_PATH` | `ffmpeg` | ffmpeg binary |
| `PROMIN_FFPROBE_PATH` | `ffprobe` | ffprobe binary (codec/HDR/audio probing) |
| `PROMIN_REMUX_MAX_TRANSCODES` | `1` | Concurrent HEVC/AV1→H264 transcodes (own semaphore) |
| `PROMIN_REMUX_MAX_COPY` | `4` | Concurrent `copy_hls`/`copy_mkv` ffmpeg processes |
| `PROMIN_REMUX_JOB_TTL` | `30m` | Idle time before a remux job directory is deleted |
| `PROMIN_TORRENT_PORT` | `0` | BitTorrent peer port; 0 = anacrolix default (42069) |
| `PROMIN_TORRENT_MAX_ACTIVE` | `5` | Torrents held by the client at once (production sets 3); the oldest idle one is evicted to make room |
| `PROMIN_TORRENT_CACHE_LIMIT_GB` | `80` | On-disk torrent data cap; LRU evicts to 90 % of it |
| `PROMIN_TORRENT_METADATA_TIMEOUT` | `30s` | Wait for torrent info after adding a magnet |
| `PROMIN_WEATHER_PLACE` | empty | Fixed city for the forecast; empty → Cloudflare geo headers, GeoIP, then `CF-IPCountry` capital |
| `PROMIN_H1_HOST` | empty | HTTP/1.1-only host advertised in `/api/v1/ping` (production `h1.promin.club`) |
| `PROMIN_MAIN_HOST` | empty | Main host advertised in `/api/v1/ping` (production `promin.club`) |
| `PROMIN_BACKUP_DIR` | `$PROMIN_DATA_DIR/backups` | Where SQLite snapshots land |
| `PROMIN_BACKUP_INTERVAL` | `24h` | Snapshot interval; `0` disables backups |
| `PROMIN_BACKUP_KEEP` | `7` | Snapshots retained |
| `PROMIN_NATIVE_SOURCES` | `false` | Master switch for native online providers |
| _provider-specific `PROMIN_*` variables_ | see the private `server/providers` submodule README | Each source reads its own base URL / token there; not part of the public config package. |
| `PROMIN_NATIVE_PROXY_URL` | empty | HTTP proxy for providers whose catalogs block datacenter IPs (Kinotochka, Eneyida, HDVB pages); from secret `lampac-proxy`. Empty → those providers off |
| `PROMIN_TELEGRAM_BOT_TOKEN` | empty (bot off) | Telegram companion bot token; k8s secret `promin-secrets/telegram-bot-token`. See docs/telegram.md |
| `PROMIN_TELEGRAM_API_BASE_URL` | `https://api.telegram.org` | Telegram API host |
| `PROMIN_WEBDIR` | unset | Dev only (`httpapi/static.go`): serve the UI from this directory instead of the embedded bundle |

Not configurable (constants in code): PIN limiter 5 failures / 15 min per IP and
30 / 15 min globally, admin limiter 5 / 15 min per IP, admin session TTL 2 h,
shutdown timeout 15 s, `last_seen` write throttle 5 min, torrent idle drop
10 min, readahead 64 MB, 40 established peers per torrent.

## 4. Background loops

| Loop | Where | Schedule | What it does |
|---|---|---|---|
| Backups | `store/backup.go` `StartBackups` | 90 s after boot, then every `PROMIN_BACKUP_INTERVAL` | `VACUUM INTO $BACKUP_DIR/promin-<UTC ts>.db`, then prune to `PROMIN_BACKUP_KEEP`. `.github/workflows/backup.yml` ships the newest snapshot off-box |
| TMDB cache prune | `main.go` | 5 min after boot, then daily | Deletes expired `list:*` rows from `tmdb_cache`; detail rows are kept for stale-ok reads |
| Library prewarm | `main.go` + `catalog/prewarm.go` | 2 min after boot, then every 6 h | Ensures every title in any profile's bookmarks/timecodes/history has a cached detail (max 300 per pass, 750 ms apart) |
| Torrent sweep | `torrent/lru.go` `StartBackgroundWorkers` | every 5 min | Drops torrents idle > 10 min from the swarm; if tracked size > limit, deletes least-recently-accessed torrents (never ones with open readers) down to 90 % |
| Remux cleanup | `remux/queue.go` `StartCleanup` | every 1 min | Kills and removes jobs untouched for `PROMIN_REMUX_JOB_TTL`; all job dirs are wiped on startup |
| Provider id map | background task returned by `providers.Build` | on boot, weekly | Downloads/refreshes the id map that drives VeoVeo, Collaps and HDVB matching |
| ffmpeg probe | `main.go` | once at boot, async | Logs a warning if ffmpeg/ffprobe are missing; detects `zscale` for HDR tone-mapping |

## 5. Logging and diagnostics

- `log/slog` text handler to stdout, level from `PROMIN_LOG_LEVEL`. The handler
  is wrapped by `logbuf` so the same records feed the `/logs` page (history of
  the last 5000 records + SSE live stream). History is in-memory and resets on
  restart.
- `withLogging` logs one line per request after the handler returns (`method`,
  `path`, `status`, `ms`). High-frequency paths (`/img`, `/stream`, `/healthz`,
  `/api/v1/sync/events`, `/api/v1/timecodes`, remux segments) log at `debug`.
- `POST /api/v1/diag` writes client-side probes (key codes, viewport, JS errors
  from TVs without devtools) into the log under `msg=diag`.
- Failed ffmpeg jobs log the stderr tail; the client only receives a short
  fixed message.
