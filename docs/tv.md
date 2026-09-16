# Live TV

A section of Promin for free-to-air television: the iptv-org catalogue for a
few countries, checked for liveness on our side, played in the ordinary
player. Same shape as YouTube (`docs/youtube.md`): its own rail item, a
sidebar, tiles, nothing shared with films except the player and the relay.

## Pieces

| Piece | Path | Role |
|---|---|---|
| `tv` package | `server/internal/tv` | Daily sync of iptv-org's JSON API, nightly liveness sweep, channel queries, the play decision (direct vs relay); `epg.go` — the programme guide (see below) |
| store | `server/internal/store/tv_repo.go`, migrations `0011_tv.sql`, `0012_tv_cors.sql`, `0013_tv_epg.sql` | `tv_channels` (+`alt_names`, `epg_id`), `tv_streams` (alive/fails/cors verdicts), `tv_favorites`, `tv_recent`, `tv_meta`, `tv_programs` |
| handlers | `server/internal/httpapi/handlers_tv.go` | `/api/v1/tv/*` (session required) |
| relay | `httpapi/relay.go` | `ua=` / `ref=` query params: a stream's fixed User-Agent / Referer, re-attached to every URL the manifest rewrite produces |
| TV screen | `web/src/screens/tv/index.ts` | rail item "TV" (when `/ping` has `tv: true`), sidebar (favourites, recent, all channels, countries, top categories), channel grid with lazy logos, long-press OK = favourite |
| player | `PlayerContext.live` + `PlayerContext.tv`, `web/src/core/player/tvguide.ts` | live: no timecodes / resume. `tv`: the transport becomes a TV set — ▲/▼ zap through the list, digits pick a channel number, OK opens the guide overlay (channels with now/progress on the left, the focused channel's programme on the right, OK on a programme unfolds its description), an info bar (number, name, now/next) after every switch; ◀/▶ reveal the ordinary button row |

Config: `PROMIN_TV_COUNTRIES` — iptv-org country codes to carry, default
`UA,RU,UK,US` (note: the United Kingdom is `UK` there). Empty → the section is
off: `/ping` reports `tv: false`, routes answer `503 tv_disabled`.

## Where the channels come from

[iptv-org](https://github.com/iptv-org/iptv) publishes its database as JSON at
`https://iptv-org.github.io/api/`: `channels.json` (~31 k channels),
`streams.json` (~17 k stream URLs with quality, User-Agent, Referer),
`logos.json`, `categories.json`, `countries.json`, `blocklist.json` (DMCA /
NSFW removals). The sync keeps, for the configured countries, open channels
that are not NSFW and not on the blocklist, and only their **HLS** streams
(`m3u8` — hls.js is the only engine on the TVs; raw MPEG-TS, DASH, RTMP are
dropped). 2026-09: UA 179, RU 428, US 1561 channels, ~4 200 streams.

This is free-to-air: news, regional, music, shopping, public broadcasters.
Paid packages (film channels, licensed sport) are not there and get removed
under DMCA. A paid provider's M3U/Xtream import lands in the same tables —
not built yet.

## Liveness

iptv-org checks its links daily, but between checks a fair share dies. Once a
day the server fetches the first bytes of every stream (12 in parallel, 10 s
timeout; alive = 2xx and a body starting with `#EXTM3U`). A stream goes dead
after two consecutive failures (one bad night must not hide a channel) and
returns on the first success. The grid shows channels with at least one alive
stream; `POST /tv/channels/{id}/fail` from the player (start failed) counts as
a failure too.

## Direct or relay

The play decision per stream:

- `https://`, no special headers, **and** the liveness probe saw
  `Access-Control-Allow-Origin: *` (`tv_streams.cors`) → **direct**: the TV
  fetches the upstream itself. Residential IP, so geo-blocked Ukrainian
  channels work from home even though the VPS would be refused; no relay
  bandwidth. Without CORS hls.js cannot read the playlist, so it is not an
  option however open the stream is.
- Everything else (`http://` mixed content, a fixed User-Agent / Referer, no
  CORS) → through `/relay?u=…&ua=…&ref=…`. The manifest rewrite carries the
  same `ua`/`ref` onto every segment URL, after the media token.

## Programme guide (EPG)

No feed is keyed by iptv-org ids, so `tv/epg.go` matches by name. Sources
(`epgSources`, XMLTV, gzip): iptvx.one for UA+RU (~4.3k channels, two weeks,
several display-names per channel incl. Latin aliases), epgshare01 `UK1` and
`US2` for UK/US. A source is fetched only when we carry one of its countries.

Matching: every display-name of the feed and every spelling of ours (`name`
plus iptv-org `alt_names`, the native spellings) is normalised — lower case,
Cyrillic transliterated, `[^a-z0-9+]` dropped — and looked up strictly first,
then loosely with filler words removed (`hd`, `tv`, `kanal`, `channel`,
`ukraine`, `international`, …). Strict before loose so "1+1 Ukraina" takes
the feed's "1+1 Україна", not the bare "1+1". Roughly two thirds of UA and RU
channels get a guide; the US free-to-air long tail mostly has none, and that
is shown as "no guide", not as an error.

Manual mapping (admin panel, `docs/auth.md` §5): `tv_epg_overrides` pins a
channel to `<source>:<xmltv id>` or to no guide; overrides win over matching
and the channel is skipped in every other source. `tv_epg_channels` keeps each
feed's channel list (id + display names) for the admin's search. Per-profile
`tv_countries` narrows the section to a subset of the configured countries.

Storage: `tv_programs(channel_id, start, stop, title, descr)` for −12 h…+3 d,
rebuilt wholesale every 12 h (`tv_meta.epg_at`); `tv_channels.epg_id`
remembers `<source>:<xmltv id>` for debugging a wrong match. ~60k rows.

## API (bearer, `/api/v1/tv`)

| Method | Path | Notes |
|---|---|---|
| GET | `/meta` | `{countries:[{code,name,flag,channels}], categories:[{id,name,channels}]}` — alive channels only |
| GET | `/channels?country=UA&category=news&q=&fav=1&recent=1&limit=` | `{items:[{id,name,country,categories,logo,quality,streams,favorite}]}`, favourites first, then by name |
| GET | `/channels/{id}/play` | `{url, direct, quality, streams}`; records "recent"; `404 no_stream` |
| POST | `/channels/{id}/fail` | the player could not start it |
| GET | `/channels/{id}/epg` | `{items:[{start,stop,title,desc?}]}`, unix seconds, −12 h … +36 h |
| GET | `/now` | `{items:{<id>:{now:{start,stop,title},next:{…}}}, at}` for every channel with a guide — the overlay's channel column |
| PUT / DELETE | `/favorites/{id}` | |

## Not done yet

- "Now" on the grid tiles (the overlay has it; the grid does not yet).
- Import of a provider's M3U / Xtream Codes (catchup, archive).
- Mini App / bot surfaces ("switch the TV to this channel").
- Channel numbers on the remote.
