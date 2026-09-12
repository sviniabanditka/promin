# API reference

Generated from the route table in `server/internal/httpapi/server.go`; handlers
and DTOs are referenced per section. Base URL for REST is `/api/v1`; bodies and
responses are JSON (UTF-8).

**Auth column**

| Value | Meaning |
|---|---|
| bearer | `requireAuth`: `Authorization: Bearer <token>` header only |
| media | `requireAuthMedia`: header **or** `?t=<token>` query parameter |
| admin | `promin_admin` cookie from `POST /admin/login` |
| basic | HTTP Basic Auth with `PROMIN_LOGS_PASSWORD`, or the `logs_auth` cookie the `/logs` page sets after a successful challenge |
| none | open |

**Error envelope** (`httpapi/errors.go`): `{"error": {"code": "...", "message": "..."}}`.
Common codes: `400 bad_request`, `401 unauthorized`, `403 forbidden`,
`404 not_found`, `409 …_taken` / `too_many_active_torrents`, `410 gone`,
`429 rate_limited` (+ `Retry-After`), `500 internal_error`,
`502 upstream_unavailable`, `504 metadata_timeout`. Messages are Ukrainian and
for display only; branch on `code`.

Request bodies are capped at 1 MiB (`maxJSONBody`).

## 1. Service

| Method | Path | Auth | Params | Response |
|---|---|---|---|---|
| GET | `/healthz` | none | — | `{status: "ok"}` |
| GET | `/api/v1/ping` | none | — | `{pong: true, version, h1_host, main_host}` — `h1_host` is the HTTP/1.1-only host for legacy TVs, `main_host` the default; empty when not configured |
| POST | `/api/v1/diag` | none | body `{kind, seq, host, data}` (≤ 16 KiB) | 204; logged verbatim as `msg=diag` |
| GET | `/msx/start.json` | none | — | Media Station X start object with `action: "link:<scheme>://<host>"` |
| GET | `/onboarding` | none | `?lang=uk\|ru\|en`, `#install\|#telegram\|#faq` | HTML help page: install MSX, Telegram bot + Mini App, tips/FAQ; self-translating (uk/ru/en switcher, remembered in localStorage) |
| GET | `/` (any unmatched path) | none | `?v=<hash>` on assets → immutable caching + ETag | embedded SPA (`static.go`) |

## 2. Auth (`handlers_auth.go`, see `docs/auth.md`)

| Method | Path | Auth | Params | Response |
|---|---|---|---|---|
| POST | `/api/v1/auth/pin` | none | body `{pin: "123456", device_name?, device_type?}`; `pin` must be 6 digits | `200 {token, user: {id, login, is_admin}}`; `401 invalid_credentials`; `429 rate_limited` |
| POST | `/api/v1/auth/logout` | bearer | — | 204 |
| GET | `/api/v1/auth/devices` | bearer | — | `{devices: [{token_id, device_name, device_type, created_at, last_seen, current}]}` (unix seconds) |
| DELETE | `/api/v1/auth/devices/{token_id}` | bearer | `?force=true` to revoke the current session | 204; `403 forbidden` (own session without force); `404 device_not_found` |

## 3. Admin panel (`admin.go`)

| Method | Path | Auth | Params | Response |
|---|---|---|---|---|
| GET | `/admin` | none | — | HTML shell |
| POST | `/admin/login` | none | body `{password}` | `{ok: true}` + `Set-Cookie: promin_admin` (Path `/admin`, HttpOnly, Secure, Strict, 2 h); `401 invalid_credentials`; `429 rate_limited` |
| POST | `/admin/logout` | admin cookie (optional) | — | 204, cookie cleared |
| GET | `/admin/profiles` | admin | — | `{profiles: [{id, login, is_admin, has_pin}]}` |
| POST | `/admin/profiles` | admin | body `{login, pin?}` | `201 {id}`; `409 login_taken` / `pin_taken` |
| PATCH | `/admin/profiles/{id}` | admin | body `{login?, pin?}` — `pin: ""` clears | 204 |
| DELETE | `/admin/profiles/{id}` | admin | — | 204; `403` for id 1 |

## 4. Catalog (`handlers_catalog.go`, DTOs in `catalog/dto.go`)

Common query: `lang` (UI language, e.g. `uk`, `ru`, `en`; default per service).

| Method | Path | Auth | Params | Response |
|---|---|---|---|---|
| GET | `/api/v1/catalog/home` | bearer | `lang?` | `{rows: [{id, title, items: [Title]}]}`. Authenticated home is personalized: `continue_watching` first, recommendation/themed rows once the user has ≥ 2 finished titles (they displace `popular_movies`/`popular_tv`), one rotating provider row, then editorial rows |
| GET | `/api/v1/catalog/list` | bearer | `type=movie\|tv`, `genre?` (TMDB id), `year_from?`, `year_to?`, `rating_from?`, `sort?=popularity\|rating\|year`, `page?=1`, `lang?` | `{page, total_pages, items: [Title]}` |
| GET | `/api/v1/catalog/search` | bearer | `q` (required), `page?`, `lang?`, `type?` = `movie` \| `tv` (typed TMDB search; omitted → `/search/multi`) | `{page, total_pages, items: [Title], people?: [{id, name, photo?, department?}]}` — `people` only on untyped searches |
| GET | `/api/v1/catalog/person/{id}` | bearer | `lang?` | `{id, name, photo?, department?, biography?, birthday?, deathday?, place_of_birth?, credits: [Title]}` — filmography most popular first, de-duplicated, poster-less entries dropped; `404 person_not_found` |
| GET | `/api/v1/catalog/title/{tmdb_id}` | bearer | `type=movie\|tv` (required), `season?`, `lang?` | `TitleDetail` (below); `404 title_not_found` |
| GET | `/api/v1/catalog/genres` | bearer | `type=movie\|tv` (required), `lang?` | `{genres: [{id, name}]}` |
| GET | `/api/v1/catalog/backdrops` | bearer | `limit?=40` (1–120), `lang?` | `{backdrops: [{url, title, year?}]}` — random backdrops for the screensaver |
| GET | `/api/v1/weather` | bearer | `lang?=uk` | `{available: false}` or `{available: true, forecast}`; never an error. Place: Cloudflare geo headers → `PROMIN_WEATHER_PLACE` → GeoIP on the client IP → country capital |

`Title` (card): `{tmdb_id, type, title, original_title, year?, rating,
imdb_rating?, poster?, backdrop?, overview?, genres[], runtime_minutes?,
content_rating?, keywords[]?, external_ids?: {imdb_id}, timecode, in_bookmarks,
seasons[]}`.

`TitleDetail` = `Title` + `{cast: [{id, name, character?, photo?}], similar:
[Title], recommendations: [Title], trailers: [{key, name, type, lang?,
youtube_url}]}`; `seasons: [{season, name, episodes: [{episode, name, air_date,
still?, overview?, runtime_minutes?, rating?, timecode}]}]`; `timecode` is the
caller's latest `{position_sec, duration_sec, season?, episode?}` on this title
or `null`.

`502 upstream_unavailable` when TMDB is down and nothing is cached.

## 5. Online sources (`handlers_sources.go`, DTOs in `sources/types.go`, see `docs/sources.md`)

| Method | Path | Auth | Params | Response |
|---|---|---|---|---|
| GET | `/api/v1/sources/online` | bearer | `tmdb_id` (required), `type=movie\|tv` (required), `title?`, `original_title?`, `year?`, `imdb_id?` | `{degraded: false, sources: [{id, name, balanser, quality_note?}]}` — providers that carry the title, no streams yet. Empty list = no catalog has it |
| GET | `/api/v1/sources/online/resolve` | bearer | `tmdb_id`, `type`, `balanser` (required; a provider id from the listing — the parameter is spelled `balanser`), `season?`, `episode?`, `voice?` (an id from `voices`), `title?`, `original_title?`, `year?`, `imdb_id?`, `demuxed_hls=false` (route HLS through `/remux`) | `ResolveResponse` (below); `404 balancer_not_found` when `balanser` is not a registered provider |

`ResolveResponse`:

```
{
  "streams":   [{"url": "/relay?u=…", "quality": "1080p" | "auto", "label": "…"}],
  "subtitles": [{"url": "/relay?u=…", "label": "…", "lang": "uk"}],
  "voices":    [{"id": "…", "name": "…"}],
  "voice":     "…",            // id of the dub the streams carry, if known
  "audio_names": ["…"],        // labels for HLS audio renditions, manifest order (optional)
  "type":      "hls" | "mp4",
  "unresolved": "not_found" | "resolve_failed"   // only when streams is empty
}
```

Stream and subtitle URLs are on Promin's origin and need the media token (`?t=`)
when loaded by a media element; the client stamps it (`mediaUrl()`).

## 6. Torrents (`handlers_torrents.go`, see `docs/torrents.md`)

| Method | Path | Auth | Params | Response |
|---|---|---|---|---|
| GET | `/api/v1/sources/torrents` | bearer | `title` and/or `original_title` (one required, else empty list), `type?=movie` , `tmdb_id?`, `year?`, `season?`, `episode?` | `{degraded, torrents: [{id, title, tracker, size, size_human, seeders, peers, quality, voices[]?}]}` sorted by seeders; `id` is the magnet id for `/torrents/add` |
| GET / POST | `/api/v1/torrents/add` | bearer | `id=<magnet_id>` **or** `magnet=<magnet: URI or base64url>` | `{infohash, files: [{index, name, size, is_video}]}`; `504 metadata_timeout`; `409 too_many_active_torrents` |
| GET | `/api/v1/torrents/active` | bearer | — | `{torrents: [{infohash, name, size, downloaded, progress, readers, added_at}]}` |
| GET | `/api/v1/torrents/audio` | bearer | `infohash`, `file` (index) | `{tracks: [{index, lang?, title?}]}` (ffprobe) ; `404 file_not_found` |
| DELETE | `/api/v1/torrents/{infohash}` | bearer | — | 204; `404 not_found` |

## 7. Media (`relay.go`, `remux.go`, `handlers_torrents.go`, `img.go`)

| Method | Path | Auth | Params | Response |
|---|---|---|---|---|
| GET | `/relay` | media | `u` = base64url(upstream URL), `Range` header forwarded | HLS manifests (`.m3u8` / `mpegurl`): rewritten so every URI points back to `/relay?u=…&t=…`. Subtitles (`.srt`/`.vtt`): served as `text/vtt` (SRT converted). Anything else: byte-for-byte passthrough with `Content-Type`, `Content-Length`, `Content-Range`, `Accept-Ranges`, `Cache-Control`, `ETag`, `Last-Modified`. `400` for a non-http(s) or loopback/link-local/private upstream; `502 upstream_unavailable` |
| GET | `/stream/{infohash}/{fileIdx}` | media | `mkv=false`, `transcode=1`, `hdr=1`, `audio=N` (default 0), `start=N` seconds | progressive file with `Range` support, **or** `302` to `/remux/<job>/playlist.m3u8?hls=1&t=…` when a remux/transcode is needed; `404 not_found` / `file_not_found` |
| GET | `/remux` | media | `u` = base64url(source URL), `kind?=copy_hls\|copy_mkv\|transcode_hevc` (default guessed from extension: `.mkv` → `copy_mkv`, else `copy_hls`), `audio?=N`, `start?=N` | `{job_id, playlist_url: "/remux/<job>/playlist.m3u8"}`; jobs dedupe on kind+source+audio |
| GET | `/remux/{job}/{file}` | media | `file` = `playlist.m3u8`, `master.m3u8`, `stream-*.m3u8` or `seg-*.ts`; `hls=1` = answer a not-ready playlist as an empty live m3u8 (200) instead of 202 JSON | Playlist: `200` m3u8 with `?t=` stamped on every child; not ready: `202 {state, progress, queue_position?}` (or empty m3u8 with `hls=1`); `master.m3u8` not ready: `503 not_ready` + `Retry-After: 1`; failed job: `502 upstream_unavailable`; `404 job_not_found`. Headers on `playlist.m3u8`: `X-Remux-Duration`, `X-Remux-Start` (when muxed from an offset), `X-Remux-Audio` (JSON track list when > 1). Segments: `video/mp2t`, immutable cache, `Range` |
| GET | `/img/{size}/{file}` | none | `size` = `w<digits>` or `original`; `file` = TMDB image file name | image proxied from TMDB and cached permanently on disk under `<DataDir>/img/`; a TMDB 404 is remembered for 1 h; `400` on a bad size |

## 8. Library and sync (`handlers_sync.go`, DTOs in `sync/dto.go`)

`media_type` is `movie` or `tv` everywhere (`400` otherwise). Timestamps are
unix seconds.

### Bookmarks

| Method | Path | Auth | Params | Response |
|---|---|---|---|---|
| GET | `/api/v1/bookmarks` | bearer | — | `{bookmarks: [{tmdb_id, media_type, added_at}]}` |
| POST | `/api/v1/bookmarks` | bearer | body `{tmdb_id, media_type}` | `201` (created) or `200` (already present) `{tmdb_id, media_type, added_at}` |
| DELETE | `/api/v1/bookmarks/{tmdb_id}` | bearer | `?media_type=` | 204 |

### Playlists

| Method | Path | Auth | Params | Response |
|---|---|---|---|---|
| GET | `/api/v1/playlists` | bearer | — | `{playlists: [{id, name, items_count, updated_at}]}` |
| POST | `/api/v1/playlists` | bearer | body `{name}` | `201 {id, name, items_count, updated_at}` |
| PATCH | `/api/v1/playlists/{id}` | bearer | body `{name}` | `200` playlist |
| DELETE | `/api/v1/playlists/{id}` | bearer | — | 204 |
| GET | `/api/v1/playlists/{id}/items` | bearer | — | `{items: [{id, tmdb_id, media_type, position}]}` |
| POST | `/api/v1/playlists/{id}/items` | bearer | body `{tmdb_id, media_type}` | `201 {id, tmdb_id, media_type, position}` |
| DELETE | `/api/v1/playlists/{id}/items/{item_id}` | bearer | — | 204 |

### History

| Method | Path | Auth | Params | Response |
|---|---|---|---|---|
| GET | `/api/v1/history` | bearer | `limit?=50`, `offset?=0` | `{items: [{tmdb_id, media_type, season, episode, watched_at}]}` (`season`/`episode` nullable) |
| POST | `/api/v1/history` | bearer | body `{tmdb_id, media_type, season?, episode?}` | `201` history item |

### Timecodes

| Method | Path | Auth | Params | Response |
|---|---|---|---|---|
| GET | `/api/v1/timecodes/continue` | bearer | `limit?=20` | `{items: [{tmdb_id, media_type, season?, episode?, position_sec, duration_sec, updated_at}]}` |
| GET | `/api/v1/timecodes/{tmdb_id}` | bearer | `media_type`, `season?`, `episode?` | `{media_type, position_sec, duration_sec, updated_at}`; `404 not_found` |
| POST | `/api/v1/timecodes` | bearer | body `{tmdb_id, media_type, season, episode, position_sec, duration_sec, updated_at}` (`updated_at` > 0 required) | `{accepted, position_sec, updated_at}` — last-write-wins on `updated_at`; when `accepted` is false the server's values are returned |

### Settings

| Method | Path | Auth | Params | Response |
|---|---|---|---|---|
| GET | `/api/v1/settings` | bearer | — | `{settings: {key: value, …}}` |
| PUT | `/api/v1/settings/{key}` | bearer | body `{value}` (string) | `{key, value}` |

### Watch queue (`handlers_queue.go`, docs/miniapp.md)

| Method | Path | Auth | Params | Response |
|---|---|---|---|---|
| GET | `/api/v1/queue` | bearer | — | `{items: [{id, tmdb_id, media_type, season, episode, position}]}` head first; `season`/`episode` are `null` for a movie |
| POST | `/api/v1/queue` | bearer | body `{tmdb_id, media_type, season?, episode?}` | `201` item (appended) or `200` the existing item when the same title/episode is already queued (no event) |
| DELETE | `/api/v1/queue/{id}` | bearer | — | 204; `404 not_found` |
| PUT | `/api/v1/queue/{id}/move` | bearer | body `{position}` (0-based, clamped) | 204; `404 not_found` |
| DELETE | `/api/v1/queue` | bearer | — | 204 (clear) |
| POST | `/api/v1/queue/pop` | bearer | — | `200` the head item, removed from the queue; `204` when the queue is empty |

Every change publishes `queue_updated` with the whole queue: `{items: [...]}`.

### Sync channel

| Method | Path | Auth | Params | Response |
|---|---|---|---|---|
| GET | `/api/v1/sync/bootstrap` | bearer | — | `{bookmarks[], playlists[], history[], timecodes[], settings{}, queue[], cursor}` |
| GET | `/api/v1/sync/events` | bearer | `since=<cursor>` | `{events: [{id, type, payload}], cursor}`; `410 gone` when the cursor is older than the hub's buffer → bootstrap again |
| GET | `/api/v1/ws` | media (`?t=`) | WebSocket upgrade | server → client: one `{id, type, payload}` frame per event for this user; client → server: `{"type":"ping"}` answered with `{"type":"pong"}` |

Event `type` values: `bookmark_added`, `bookmark_removed` (payload: bookmark),
`playlist_created`, `playlist_updated` (playlist, or `{id, deleted: true}`),
`playlist_item_added` (`{playlist_id, item}`), `playlist_item_removed`
(`{playlist_id, item_id}`), `history_added` (history item), `timecode_updated`
(timecode), `settings_updated` (`{key, value}`), `queue_updated` (`{items}` — the
full watch queue).

## 9. Logs (`logs.go`; registered only when `PROMIN_LOGS_PASSWORD` is set)

| Method | Path | Auth | Params | Response |
|---|---|---|---|---|
| GET | `/logs` | basic | — | HTML viewer |
| GET | `/logs/api/history` | basic | `limit?=1000` (≤ 5000), `level?`, `q?` | `{records: [...]}` from the in-memory ring buffer |
| GET | `/logs/api/stream` | basic | `level?`, `q?` | `text/event-stream` of new records, keepalive every 20 s |

## Profile data (Danger zone)

| Method | Path | Auth | Effect |
|---|---|---|---|
| DELETE | `/api/v1/me/history` | bearer | Deletes the profile's history and timecodes; sync event `data_cleared` (`scope: history`). 204. |
| DELETE | `/api/v1/me/data` | bearer | Deletes bookmarks, playlists, history, timecodes, settings and revokes all sessions of the profile; sync event `data_cleared` (`scope: all`). 204. |

## Telegram companion

| Method | Path | Auth | Effect |
|---|---|---|---|
| GET | `/api/v1/telegram/status` | bearer | `{enabled, linked, bot_username}` |
| POST | `/api/v1/telegram/link` | bearer | Issues a 6-digit link code: `{code, deep_link, expires_at}`; `503 telegram_disabled` when the bot is off |
| DELETE | `/api/v1/telegram/link` | bearer | Unlinks every chat of the profile. 204 |
| GET | `/api/v1/telegram/links` | bearer | `{links:[{chat_id, first_name, username, created_at}]}` |
| DELETE | `/api/v1/telegram/links/{chat_id}` | bearer | Unlinks one chat of the profile. 204 |

Sync event `open_title` (`{tmdb_id, media_type, device_id, title}`) is delivered to all sockets of the user; only the device whose token prefix equals `device_id` acts. See docs/telegram.md.

## Telegram Mini App (docs/miniapp.md)

| Method | Path | Auth | Effect |
|---|---|---|---|
| POST | `/api/v1/tg/auth` | none | `{init_data, platform?}` → validates Telegram initData (HMAC, 24 h), finds the linked profile, issues a session of type `telegram`: `{token, user}`. `401 tg_invalid`, `403 tg_not_linked`, `503 telegram_disabled` |
| GET | `/api/v1/tg/devices` | bearer | `{devices:[{id, name, type, current, online, state}]}` — `online` = live sync socket, `state` = last `PlayerState` (null when idle or older than 60 s) |
| POST | `/api/v1/tg/send` | bearer | `{device_id, open:{tmdb_id, media_type, season?, episode?, resume?}}` or `{device_id, remote:{action, value?, key?, str?}}` → publishes `open_title` / `remote` to that device. Actions: `nav_up nav_down nav_left nav_right nav_ok nav_back toggle_play seek seek_to prev next mute night sleep volume set_voice set_subtitle set_local`. 204; `400` unknown action; `404 device_offline` |
| POST | `/api/v1/player/state` | bearer | TV reports `{tmdb_id, media_type, title, season, episode, position_sec, duration_sec, paused, voice}` or `{closed:true}`; kept per device in memory, published as sync event `player_state` (≤1/s per device). 204 |

Sync events added: `player_state` (`PlayerState` + `device_id`, or `{device_id, closed:true}`); `open_title` gained optional `season`/`episode`; `remote` gained action `seek_to` (absolute seconds). `remote` also carries the D-pad actions `nav_*` (the TV routes them into its Controller, so they work outside the player).

| DELETE | `/api/v1/auth/devices` | bearer | Signs out every session of the profile except the caller's: `{revoked: n}` |

## External subtitles (OpenSubtitles)

| Method | Path | Auth | Effect |
|---|---|---|---|
| GET | `/api/v1/subtitles/search?imdb_id=&season=&episode=&langs=uk,ru,en` | bearer | `{enabled, results:[{file_id, lang, release, downloads, hearing_impaired, fps, uploader}]}` — most downloaded first; `enabled:false` when no API key |
| GET | `/api/v1/subtitles/{file_id}.vtt` | media token `?t=` | The subtitle as WebVTT; downloaded once per file id (anonymous quota) and cached under `/data/subs/` |

## YouTube section (`/api/v1/yt/*`)

Bearer routes proxied to the `ytx` sidecar: `GET /account`, `POST /account/login`, `DELETE /account`, `GET /browse/{page}`, `GET /search`, `GET /video/{id}`, `GET /play/{id}` (creates a `mux2` remux job), `GET /segments/{id}` (SponsorBlock). Shapes and errors in `docs/youtube.md`. `GET /api/v1/ping` carries `youtube: true|false`.
