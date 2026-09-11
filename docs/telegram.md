# Telegram companion

Searching with a phone keyboard beats a D-pad, so Promin ships a Telegram bot
— a separate bot from the alerting one (`@promin_club_bot`): send it a title, pick a result,
open it on a TV. Server code: `server/internal/telegram`; client: the
"Telegram bot" row in Settings and the `open_title` sync event.

## Enabling

`PROMIN_TELEGRAM_BOT_TOKEN` (k8s: `promin-secrets/telegram-bot-token`, optional).
Empty = bot off; the Settings row then says so. `PROMIN_TELEGRAM_API_BASE_URL`
overrides the API host (default `https://api.telegram.org`). The bot long-polls
`getUpdates` from the Promin process — no webhook, no inbound port.

## Linking a chat to a profile

1. Settings → Telegram bot → a 6-digit code (valid 10 minutes) and a
   `t.me/<bot>?start=<code>` deep link are shown; the sheet polls status and
   closes itself once linked.
2. The user sends `/start <code>` (or the bare code) to the bot. The chat id is
   stored in `telegram_links(chat_id, user_id)`; one profile may have several
   chats, a chat belongs to one profile.
3. `/unlink` in the bot unlinks that chat; Settings → Telegram bot → Unlink
   removes every chat of the profile. Messages from unlinked chats get a hint to
   open Settings and nothing else.

Endpoints (bearer): `GET /api/v1/telegram/status` → `{enabled, linked,
bot_username}`; `POST /api/v1/telegram/link` → `{code, deep_link, expires_at}`;
`DELETE /api/v1/telegram/link`. `503 telegram_disabled` when the bot is off.

## Menu

After linking the bot shows a persistent reply keyboard, so nothing has to be
typed: 🔍 Search · ▶ Continue / ★ Bookmarks · 🔥 What to watch / 🎛 Remote ·
⚙ Settings. Any other text is a title search. Every string comes from
`server/internal/telegram/i18n.go` (uk/ru/en, key parity enforced by a test);
the language is the profile's synced `lang` setting, so the bot and the TV
always speak the same language.

- **Search** — five results per page, per result "Open on TV" and a ★/☆
  bookmark toggle, ◀ ▶ paging edits the same message.
- **Continue** — the profile's continue-watching list (`ListContinueWatching`)
  with season/episode and position; "Open on TV" sends `open_title` with
  `resume: true`, and the TV continues from the saved position.
- **Bookmarks** — paginated list with "Open on TV" and "✕ remove".
- **What to watch** — the rows of the TV home screen (`catalog.Home`), then
  five items per page with the same buttons.
- **Remote** — a D-pad (▲ / ◀ OK ▶ / ▼ / ↩ Back) over ⏪ 30 · ⏯ · ⏩ 30 /
  ⏮ · ⏭ / 🔇 · 🌙 Night · 😴 Sleep. Each press publishes `remote {device_id,
  action, value}` (`nav_up | nav_down | nav_left | nav_right | nav_ok |
  nav_back | toggle_play | seek ±30 | prev | next | mute | night | sleep 30`).
  The D-pad drives the TV UI on any screen (the TV feeds it into the same
  Controller entry points as its physical remote); the TV's player consumes
  playback actions (`web/src/core/player/remote.ts`), night mode toggles on any
  screen, and a TV without an open player shows a toast for the rest. The same
  keyboard is attached to the "Opened on <TV>" message, so the source and
  episode can be picked right after opening a title.
- **Settings** — language (🇺🇦 🇷🇺 🇬🇧; writes the synced `lang`, the TV repaints
  live through `settings_updated`) and Unlink with a confirm step.

Listings are kept per (chat, message) in memory; after a restart an old message
answers "stale — press the menu button again". The device picked for "Open on
TV" or the remote is reused for five minutes while it stays online.

## Several phones on one profile

A profile is shared by the household, so any number of Telegram chats may be
linked to it. Ways to add a phone: on the TV, Settings → Telegram bot → "Link
another phone" shows a fresh code/QR; in the Mini App, Settings → Telegram →
"Invite another phone" issues a code and opens Telegram's share sheet with the
`t.me/<bot>?start=<code>` link — the invitee presses Start and is linked. The
bot stores `first_name`/`username` with each chat; `GET /api/v1/telegram/links`
lists them, `DELETE /api/v1/telegram/links/{chat_id}` unlinks one phone,
`DELETE /api/v1/telegram/link` unlinks all. All linked chats share the same
menu, Mini App, devices, library and the synced `lang`.

## Search and open

Any text from a linked chat is a TMDB search (`catalog.Search`, Ukrainian).
The reply lists five results per page with a button per result and ◀ ▶ paging
(the same message is edited). "Відкрити на ТВ":

- one device online → the title opens there, reply "Відкрито на <device>";
- several → a keyboard with device names;
- none → "Немає пристроїв онлайн".

"Online" means a live sync WebSocket: the hub registers `(device id, device
name)` per connection (`Hub.Subscribe`, `Hub.OnlineDevices`). The device id is
`auth.TokenID(session token)` — the first 12 characters, the same value
`GET /api/v1/auth/devices` returns as `token_id`.

The hub publishes `open_title {tmdb_id, media_type, device_id, title}` to every
socket of the user; the client acts only when `device_id` matches its own token
prefix, pushes the title page and shows an "Opened from Telegram" toast.

Limits: one search per two seconds per chat; group chats are ignored; the bot
token is never logged.

## Mini App

The chat menu button opens the Telegram Mini App at `/tg/` — a phone-first remote and browser for the same profile. The button is set by the bot owner in BotFather, not by the server. See docs/miniapp.md.
