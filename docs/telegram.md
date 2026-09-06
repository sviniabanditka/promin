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
