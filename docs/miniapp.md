# Telegram Mini App ("Promin Remote")

A phone-first companion that runs inside Telegram (Mini App) and drives the TV.
It is a separate front end (`web/miniapp/`, Preact, ES2020 — no TV-webview
constraints) served by the Promin binary at `/tg/`, talking to the same API as
every other client plus a small `/api/v1/tg/*` surface. The chat bot keeps all
of its features; the Mini App only adds convenience.

## Auth

`POST /api/v1/tg/auth` body `{ "init_data": "<Telegram.WebApp.initData>" }`.
The server validates the `hash` with HMAC-SHA256 keyed by
`SHA256("WebAppData", bot_token)` per Telegram's spec, rejects data older than
24 h, reads `user.id`, looks the chat up in `telegram_links` and issues a
regular session (device type `telegram`, device name from `user.first_name` +
platform). Response `{ "token": "...", "user": { "id", "login" } }`. Errors:
`401 tg_invalid` (bad hash / expired), `403 tg_not_linked` (chat is not linked —
the app then shows "open Settings → Telegram bot on the TV").

From then on the Mini App uses the token like any client: `Authorization:
Bearer` for REST, `?t=` for the WebSocket `/api/v1/ws` and media routes.

## Devices and player state

`GET /api/v1/tg/devices` → `{ "devices": [ { "id", "name", "online": true,
"state": PlayerState | null } ] }` — every session of the profile; `online` =
has a live sync socket (hub); `state` = last reported player state of that
device (below), `null` when nothing is playing.

`PlayerState` (reported by the TV, kept in memory per device):

```json
{
  "tmdb_id": 1399, "media_type": "tv", "title": "Гра престолів",
  "season": 2, "episode": 5,
  "position_sec": 2530.4, "duration_sec": 3720.0, "paused": false,
  "source": "collaps", "voice": "LostFilm",
  "updated_at": 1788800000
}
```

The TV posts it with `POST /api/v1/player/state` (bearer; device id = the
session's token id) every 5 s while the player is open, immediately on
play/pause/seek/episode change, and `{"closed": true}` when the player closes
(server drops the state). The hub publishes the same object as sync event
`player_state` with `device_id` added, so the Mini App updates live over the
WebSocket. Writes are throttled server-side to one publish per second per device.

## Sending to a TV

`POST /api/v1/tg/send` body one of:

```json
{ "device_id": "abcDEF123456", "open": { "tmdb_id": 550, "media_type": "movie", "resume": true } }
{ "device_id": "...", "open": { "tmdb_id": 1399, "media_type": "tv", "season": 2, "episode": 6 } }
{ "device_id": "...", "remote": { "action": "toggle_play" } }
{ "device_id": "...", "remote": { "action": "seek", "value": -30 } }
{ "device_id": "...", "remote": { "action": "seek_to", "value": 1234.5 } }
{ "device_id": "...", "remote": { "action": "next" } }
{ "device_id": "...", "remote": { "action": "mute" } }
{ "device_id": "...", "remote": { "action": "night" } }
{ "device_id": "...", "remote": { "action": "sleep", "value": 30 } }
```

`open` publishes the existing `open_title` event (fields `tmdb_id`,
`media_type`, `device_id`, `title`, `resume`, plus optional `season`,
`episode` — the TV opens the title and, when season/episode are given, starts
that episode). `remote` publishes the existing `remote` event; `seek_to`
(absolute seconds) is new. `404 device_offline` when the device has no socket.

## TV settings from the phone

Synced profile settings are plain `PUT /api/v1/settings/{key}` writes with the
TV's value formats — `lang` (uk|ru|en), `default_quality` (auto|2160|1080|720|480),
`player_engine` (auto|hlsjs|native), `subtitle_size` (small|medium|large),
`screensaver_min` (0|3|5|10), `night_mode` (true|false), `night_dim` (50..90
step 5), `player_speed`; the TV applies them live through `settings_updated`.

Device-local settings (`legacy_tv_mode`, `reduce_motion`, `debug_mode`) never
leave the TV, so the TV reports them with `POST /api/v1/device/settings`
(`{"legacy_tv_mode":"true", ...}`) on login and on every change; the hub keeps
them per device, `GET /api/v1/tg/devices` returns them as `settings`, and the
sync event `device_settings {device_id, settings}` announces changes. The Mini
App flips one with `POST /api/v1/tg/send {device_id, remote:{action:"set_local",
key, str:"true"|"false"}}`; the TV applies it and shows a toast.

Danger zone actions are the same endpoints the TV uses: `DELETE /api/v1/me/history`
and `DELETE /api/v1/me/data` (the latter revokes every session — the Mini App
re-authenticates through initData afterwards, the TVs ask for the PIN).

## Bot menu button

At start the bot calls `setChatMenuButton` with `web_app.url =
https://<PROMIN_MAIN_HOST>/tg/` (when `PROMIN_MAIN_HOST` is set), so the Mini
App is one tap away in the chat.

## Serving

Build output goes to `server/webdist/tg/` (`index.html`, `app.js`, `app.css`,
embedded with the rest of `webdist`); the binary serves `/tg/` and `/tg/*`
from there (SPA fallback to `index.html`). `index.html` loads
`https://telegram.org/js/telegram-web-app.js` before the bundle.

## Screens (Preact, hash router)

- **Home** — target-TV chip listing only devices that are online now (hidden with one device), "Now playing" card with
  a live progress bar and ⏯, "Continue" row (`GET /api/v1/sync/bootstrap` /
  timecodes), search field, rows from `GET /api/v1/catalog/home`.
- **Search** — `GET /api/v1/catalog/search?q=` with posters.
- **Title** — `GET /api/v1/catalog/title/{tmdb_id}?type=` (+ `season=` for
  episodes): poster, overview, rating, ★ bookmark, seasons → episode list, each
  "▶ On TV" (send `open` with season/episode); "▶ Continue" when a timecode
  exists.
- **Remote** — full screen: title/episode, scrubbable progress (`seek_to`),
  ⏮ ⏪30 ⏯ ⏩30 ⏭, 🔇, 🌙 night, 😴 sleep, "next episode".
- **Library** — bookmarks (`/api/v1/bookmarks`), playlists, history — each item
  opens Title.
- **Settings** — language (`PUT /api/v1/settings/lang`), devices
  (`/api/v1/auth/devices`), unlink (`DELETE /api/v1/telegram/link`).

Theme from `Telegram.WebApp.themeParams` (CSS variables), `BackButton` for
navigation, `MainButton` for the primary action on Title, haptic feedback on
remote presses. Language: `Telegram.WebApp.initDataUnsafe.user.language_code`
until the profile settings load, then the synced `lang`.
