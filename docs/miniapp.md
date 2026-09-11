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
  "voices": [ { "id": "track:0", "name": "Русский" }, { "id": "lostfilm", "name": "LostFilm" } ],
  "voice_id": "track:0",
  "subtitles": [ { "id": "off", "label": "Вимк" }, { "id": "sub:0", "label": "English" }, { "id": "hls:0", "label": "Українська" } ],
  "subtitle_id": "off",
  "volume": 80, "muted": false,
  "updated_at": 1788800000
}
```

`voices` is the player's merged Audio menu in menu order: in-stream tracks
(`track:<n>`, remux / hls.js / native — n is the row index) first, then source
dubs (their voice id). `voice_id` is the first active row, i.e. what actually
plays. `subtitles` is the Subtitles menu: the implicit `off`, sidecar files
`sub:<n>`, in-manifest text tracks `hls:<n>`; `subtitle_id` is the active one
or `off`. `volume` is 0..100 (omitted when 0), `muted` a bool (omitted when false).

The TV posts it with `POST /api/v1/player/state` (bearer; device id = the
session's token id) every 5 s while the player is open, immediately on
play/pause/seek/volume/episode change, and `{"closed": true}` when the player
closes (server drops the state). To keep the 5 s tick small the two lists
travel only when they changed or every 30 s, flagged with `"lists": true`;
a report without the flag keeps the device's last lists on the server. The hub publishes the same object as sync event
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
{ "device_id": "...", "remote": { "action": "set_voice", "str": "track:1" } }
{ "device_id": "...", "remote": { "action": "set_subtitle", "str": "sub:0" } }
{ "device_id": "...", "remote": { "action": "volume", "value": 65 } }
{ "device_id": "...", "remote": { "action": "nav_up" } }
```

`nav_up | nav_down | nav_left | nav_right | nav_ok | nav_back` are a D-pad for
the TV UI itself: the TV feeds them into the same `Controller.move / enter /
back` entry points its remote keys reach, so they work on every screen (title
page, source and episode picker, player menus), and they wake the screensaver
like a real key. After a successful `open` the Mini App shows this D-pad in a
bottom sheet on top of whatever screen it is on: the TV is now on the title
page and the source / episode still have to be picked.

`set_voice` / `set_subtitle` take an id from the device's `PlayerState.voices` /
`.subtitles` (`"off"` disables subtitles); the TV runs the same code path as the
matching row of its Audio / Subtitles menu, shows a toast and reports state at
once. `volume` sets `<video>.volume` (0..100) and unmutes.

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

## Watch queue

A per-profile ordered list of movies and episodes the phone lines up for the
TV. Table `watch_queue` (migration `0008_queue.sql`: `id, user_id, tmdb_id,
media_type, season, episode, position, created_at`; one row per
(profile, title, season, episode) — `NULL` season/episode is the movie itself).
Endpoints in docs/api.md ("Watch queue"); the bootstrap carries `queue[]` and
every change fans out `queue_updated {items}` with the whole list, so the TV and
every phone hold the same snapshot with no merging.

**Mini App.** Library → *Queue* tab: rows with posters, `S2 E5` label, ▲▼
reorder (`PUT /queue/{id}/move`), ✕ remove, *Clear*, and *▶ Play on TV* for the
head — it sends `open` with the head's season/episode to the target TV and, once
the send succeeded, removes the item. The Title screen has *＋ To queue* for a
movie and a ＋ on every episode row (a second tap removes the item).

**TV.** `core/sync.ts` mirrors the queue (`queueHead()`, `queueLength()`,
`popQueue()`). `screens/title.ts` plugs it into the player's existing next-episode
hooks, so the 10 s "next episode" countdown, the ⏭ button and the "▲ next" skip
prompt during the credits all work without player changes:

- movies (online or torrent) get an `onNext` that exists only while the queue is
  non-empty (a getter — items added from the phone mid-film are picked up);
- a series at the true edge of the show (`crossSeason` finds no next season) or
  a torrent pack past its last file falls through to the queue instead of the
  "no source" toast; the last season's episode list gets a synthetic trailing
  entry "From queue: <title>" so the player's caption/countdown name what comes
  next (picking it also jumps to the queue).

On advance the TV calls `POST /api/v1/queue/pop`, leaves the player
(`router.back()`) and opens the head with the deep-link autoplay
(`openTitle(type, id, false, season, episode, autoplay)` — `autoplay` starts a
movie from the top; episodes use the existing `season`/`episode` deep link).

## Bot menu button

The chat menu button that opens the Mini App is configured once by the bot
owner in BotFather (`/setmenubutton` → the Mini App URL, `https://promin.club/tg/`
in production). The server never calls `setChatMenuButton`: it used to do so on
every start, which overwrote whatever the owner had set.

## Serving

Build output goes to `server/webdist/tg/` (`index.html`, `app.js`, `app.css`,
embedded with the rest of `webdist`); the binary serves `/tg/` and `/tg/*`
from there (SPA fallback to `index.html`). `index.html` loads
`https://telegram.org/js/telegram-web-app.js` before the bundle.

## Screens (Preact, hash router)

- **Home** — target-TV chip listing only devices that are online now (hidden with one device), "Now playing" card with
  a live progress bar and ⏯, "Continue" row (`GET /api/v1/sync/bootstrap` /
  timecodes), search field, rows from `GET /api/v1/catalog/home`.
- **Search** — `GET /api/v1/catalog/search?q=` with posters. Recent queries
  (last 10, `localStorage`) are shown as chips while the field is empty; a query
  is remembered when the user presses Enter or opens a result.
- **Title** — `GET /api/v1/catalog/title/{tmdb_id}?type=` (+ `season=` for
  episodes): poster, overview, rating, ★ bookmark, seasons → episode list, each
  "▶ On TV" (send `open` with season/episode); "▶ Continue" when a timecode
  exists.
- **Remote** — full screen: title/episode, scrubbable progress (`seek_to`),
  ⏮ ⏪30 ⏯ ⏩30 ⏭, a chip row 🎙 audio / 💬 subtitles / 🔊 volume (each opens a
  sheet: the list with the current row marked, or a 0..100 slider debounced
  150 ms into `volume`; chips are disabled until the TV has sent the lists),
  🔇, 🌙 night, 😴 sleep, "next episode", and the D-pad (▲ ◀ OK ▶ ▼ + Back)
  for the TV UI. With nothing playing the tab shows the D-pad and the
  "Continue" rows.
- **Library** — bookmarks (`/api/v1/bookmarks`), playlists, history — each item
  opens Title.
- **Settings** — language (`PUT /api/v1/settings/lang`, then a full reload so cached cards and rows come back in the new language; a `settings_updated lang` from another device reloads too), devices
  (`/api/v1/auth/devices`), unlink (`DELETE /api/v1/telegram/link`).

Theme from `Telegram.WebApp.themeParams` (CSS variables), `BackButton` for
navigation, `MainButton` for the primary action on Title, haptic feedback on
remote presses. Language: `Telegram.WebApp.initDataUnsafe.user.language_code`
until the profile settings load, then the synced `lang`.
