# Accounts, PIN login and the hard gate

Promin is a household service: one **admin** and N **profiles**. A profile logs
in on a TV with a 6-digit PIN; the admin manages profiles from a phone at
`/admin`. Every data and media route requires a valid session — there is no
guest mode and no toggle to disable auth.

Code map:

| Piece | Path |
|---|---|
| Sessions, token resolve, devices | `server/internal/auth/service.go`, `session.go` |
| PIN + admin login, rate limits, admin bootstrap | `server/internal/auth/service_pin.go`, `ratelimit.go`, `argon2.go` |
| Middleware `requireAuth` / `requireAuthMedia` | `server/internal/httpapi/middleware.go` |
| `/api/v1/auth/*` handlers | `server/internal/httpapi/handlers_auth.go` |
| Admin panel | `server/internal/httpapi/admin.go`, `admin_html.go` |
| Users/sessions schema | `server/internal/store/migrations/0002_auth_sync.sql`, `0005_pin_profiles.sql` |
| Client side | `web/src/core/auth.ts`, `api.ts` (401 interceptor), `screens/login.ts` |

## 1. Users

Table `users(id, login, pass_hash, created_at, pin_lookup)`.

- **Admin** is user `id = 1` (`User.IsAdmin()`). It is the only user with a
  password (`pass_hash`, argon2id PHC string). `EnsureAdmin` creates it at boot
  from `PROMIN_ADMIN_PASSWORD`, and resets the hash whenever the env value no
  longer verifies. In production the value comes from the k8s secret
  `promin-secrets`, key `admin-password`; it is never written anywhere else.
  Without the env var and without an existing user 1, `/admin` cannot be
  entered.
- **Profiles** are users `2..n`, created from the admin panel with a login
  (display name) and optionally a PIN. `pass_hash` is empty.
- `pin_lookup = hex(HMAC-SHA256(pinSecret, pin))`, `NULL` when the profile has
  no PIN (not enterable). A partial unique index makes a PIN resolve to at most
  one profile in O(1) and makes collisions a DB error (`ErrPinTaken` → 409
  `pin_taken`). The PIN is never stored or returned; the admin API exposes only
  `has_pin`.
- `pinSecret`: `PROMIN_PIN_SECRET`, else 32 random bytes generated once into
  `<DataDir>/pin_secret` (mode 0600) by `loadOrCreatePinSecret` in `main.go`.
  Rotating it invalidates every PIN.

## 2. PIN login — `POST /api/v1/auth/pin`

Open (pre-gate). Body `{pin, device_name?, device_type?}`; `pin` must be
exactly six ASCII digits or the request is a 400.

```
LoginPIN(ip, pin, deviceName):
  per-IP limiter  "pin|<ip>"   5 failures / 15 min → blocked 15 min
  global limiter  "pin"        30 failures / 15 min → blocked 15 min   (never cleared by success)
  users.GetByPinLookup(hmac(pin)) → miss: record failure, 401 invalid_credentials
  newSession(user, deviceName, "tv")
```

Response `{token, user: {id, login, is_admin}}`. `device_name` falls back to a
type guessed from the User-Agent (`tizen`, `webos`, `androidtv`, `browser`).
A blocked key answers 429 `rate_limited` with `Retry-After`. The client IP (`clientIP`) is the peer our front saw — the LAST `X-Forwarded-For` hop written by Traefik/nginx, else `RemoteAddr` — and only when that peer is a Cloudflare edge address is `CF-Connecting-IP` believed. A direct visitor (node IP, h1 host) cannot forge the header to rotate the rate-limit key. Admin login also has a global limiter (20 failures / 15 min, never cleared by success) like the PIN one.

## 3. Sessions and bearer tokens

Table `sessions(token PK, user_id, device_name, device_type, created_at,
last_seen)`; `ON DELETE CASCADE` from users.

- A token is `base64url(32 random bytes)`, no padding, opaque; all state is
  server-side. No JWT.
- `device_type` is `"tv"` for PIN sessions and `"admin"` for admin-panel
  sessions. TV sessions never expire (a living-room TV must not log itself
  out); admin sessions expire 2 h after `created_at` (`adminTTL`, checked in
  `ResolveToken`).
- `ResolveToken` writes `last_seen` at most once per 5 minutes per session.
- The client stores the token in `localStorage` (`promin:token`, `promin:user`)
  and sends `Authorization: Bearer <token>` on every API call. A 401 from any
  call clears the session and returns the UI to the PIN screen.

### Media token `?t=`

`<video>`, `<img>`, `<track>` and the WebSocket handshake cannot set headers,
so the media routes accept the same session token as a query parameter:

| Route | Middleware |
|---|---|
| `/api/v1/**` (data) | `requireAuth` — header only |
| `/relay`, `/stream/**`, `/remux`, `/remux/**`, `/api/v1/ws` | `requireAuthMedia` — header **or** `?t=` |
| `/img/**` | open — poster paths are only discoverable through the gated catalog and the images are public |

Every child URL a media handler produces must carry the token because HLS
clients resolve relative URIs without the parent's query string. All such
sites go through `withMediaToken` (`httpapi/hls_token.go`): `/relay`
manifest rewriting, `/remux` playlist child lines and `#EXT-X-MEDIA` URIs,
the `/stream` → `/remux` redirect, and the loopback `/stream` URL ffmpeg reads.
The token is appended only to Promin's own URLs, never forwarded upstream.
The client stamps it in `mediaUrl()` (`web/src/core/api.ts`).

## 4. Devices

- `GET /api/v1/auth/devices` → `{devices: [{token_id, device_name,
  device_type, created_at, last_seen, current}]}`. `token_id` is the first 12
  characters of the token — enough to pick a row, useless on its own.
- `DELETE /api/v1/auth/devices/{token_id}` revokes a session. Revoking the
  caller's own session requires `?force=true` (403 `forbidden` otherwise);
  unknown id → 404 `device_not_found`.
- `POST /api/v1/auth/logout` deletes the current session (204). The UI labels
  it "switch profile" and returns to the PIN screen.

## 5. Admin panel — `/admin`

Phone-oriented HTML shell served by `adminHandlers.page` (modern Chrome; not
the TV ES5 bundle). Its JS probes `GET /admin/profiles`: 401 → login form,
200 → roster.

- `POST /admin/login` `{password}` → `AdminLogin`: per-IP limiter
  `"admin|<ip>"` (5 / 15 min), argon2 verify against user 1, session of type
  `"admin"`, delivered as cookie `promin_admin` (`Path=/admin`, `HttpOnly`,
  `Secure`, `SameSite=Strict`, 2 h). Response `{ok: true}`.
- `POST /admin/logout` deletes the session and clears the cookie.
- `GET /admin/profiles` → `{profiles: [{id, login, is_admin, has_pin}]}`.
- `POST /admin/profiles` `{login, pin?}` → 201 `{id}`. A taken PIN rolls the
  new profile back (409 `pin_taken`); a taken login is 409 `login_taken`.
- `PATCH /admin/profiles/{id}` `{login?, pin?}` — `pin: ""` clears, six digits
  sets, absent leaves untouched. 204.
- `DELETE /admin/profiles/{id}` — 204; deleting user 1 is 403.

Every `/admin/*` JSON route runs `gate`: the cookie must resolve to a session
with `device_type == "admin"` whose user `IsAdmin()`.

## 6. The hard gate

`NewServer` (`httpapi/server.go`) wraps every route except these in
`requireAuth`/`requireAuthMedia`:

- `GET /healthz`, `GET /api/v1/ping`, `POST /api/v1/diag` (client diagnostics
  from devices that cannot get past the PIN screen; body capped at 16 KiB)
- `POST /api/v1/auth/pin`
- `/admin`, `/admin/*` (own cookie credential)
- `GET /img/**`, `GET /msx/start.json`, `GET /onboarding`, the static SPA shell
  `/`
- `/logs*` when `PROMIN_LOGS_PASSWORD` is set (Basic Auth with that password)

Everything else — catalog, sources, torrents, sync, media — answers 401
`unauthorized` without a valid session. The SPA shell itself carries no data,
so serving it open is what lets the PIN screen load.

## 7. Per-user library and sync

All library tables key on `user_id` with cascade delete, so a profile is a
complete, isolated library: bookmarks, playlists (+items), history, timecodes,
settings. Handlers are in `httpapi/handlers_sync.go`, logic in
`server/internal/sync/service.go`.

- **Bookmarks** — `GET/POST /api/v1/bookmarks`, `DELETE /api/v1/bookmarks/{tmdb_id}?media_type=`.
- **Playlists** — CRUD on `/api/v1/playlists` and `/api/v1/playlists/{id}/items`.
- **History** — `GET/POST /api/v1/history` (`tmdb_id, media_type, season?, episode?`).
- **Timecodes** — `POST /api/v1/timecodes` is last-write-wins on
  `updated_at`; the response says `accepted: false` and returns the server's
  position when the client lost. `GET /api/v1/timecodes/continue` feeds the
  "Continue watching" shelf; `GET /api/v1/catalog/title/{id}` also embeds the
  viewer's latest position as `timecode`.
- **Settings** — `GET /api/v1/settings`, `PUT /api/v1/settings/{key}
  {value}`: free-form string KV per user.

Every mutation publishes an `Event{id, type, payload}` to the in-memory hub for
that user (`sync/hub.go`): `bookmark_added`, `bookmark_removed`,
`playlist_created`, `playlist_updated`, `playlist_item_added`,
`playlist_item_removed`, `history_added`, `timecode_updated`,
`settings_updated`. Clients receive them in two ways:

- **WebSocket** `GET /api/v1/ws?t=<token>` — server pushes every event for the
  account (from any of its devices) as a JSON frame; the client sends
  `{"type":"ping"}` and gets `{"type":"pong"}`. Mutations never travel over the
  socket; they are REST calls.
- **Polling fallback** `GET /api/v1/sync/events?since=<cursor>` every 15 s
  (`web/src/core/sync.ts`); a cursor the hub no longer holds answers 410
  `gone`, and the client reloads `GET /api/v1/sync/bootstrap` (bookmarks,
  playlists, history, timecodes, settings, cursor).

The client keeps a local mirror (`promin:sync-cache`) for instant paint and
repaints screens on remote events.

### Device-local vs synced settings

`web/src/core/settings.ts` holds all preferences in `localStorage`
(`promin:settings`) and mirrors a subset to the server with
`PUT /api/v1/settings/{key}`; on boot `syncFromServer()` applies
`GET /api/v1/settings` over the local values.

| Synced across devices (server KV) | Device-local only |
|---|---|
| `default_quality`, `player_engine`, `player_speed`, `subtitle_size`, `screensaver_min`, `lang` (owned by `core/i18n`) | `legacy_tv_mode` (old-Samsung transport: `demuxed_hls=false`, HTTP/1.1 host), `reduce_motion`, `debug_mode` |

Device-local flags describe *this* TV's hardware, so another device must not
inherit them; they can also be preset through URL flags `?legacy=1` /
`?debug=1` for a device whose PIN screen cannot be operated yet.

## Danger zone

Settings → Danger zone (signed-in only), each action behind a confirm sheet:

- **Clear watch history** — `DELETE /api/v1/me/history`: history rows and resume
  positions of this profile. Bookmarks, playlists and settings stay. Other
  devices receive the sync event `data_cleared{scope:"history"}` and drop their
  local copies.
- **Delete all data** — `DELETE /api/v1/me/data`: bookmarks, playlists, history,
  resume positions and synced settings of this profile, then **every session of
  the profile is revoked**. The profile row and its PIN survive; all devices,
  including the one that pressed the button, land on the PIN screen. Event
  `data_cleared{scope:"all"}`.
