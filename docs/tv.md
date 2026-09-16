# Live TV

A section of Promin for free-to-air television: the iptv-org catalogue for a
few countries, checked for liveness on our side, played in the ordinary
player. Same shape as YouTube (`docs/youtube.md`): its own rail item, a
sidebar, tiles, nothing shared with films except the player and the relay.

## Pieces

| Piece | Path | Role |
|---|---|---|
| `tv` package | `server/internal/tv` | Daily sync of iptv-org's JSON API, nightly liveness sweep, channel queries, the play decision (direct vs relay) |
| store | `server/internal/store/tv_repo.go`, migration `0011_tv.sql` | `tv_channels`, `tv_streams` (with our alive/fails verdict), `tv_favorites`, `tv_recent`, `tv_meta` |
| handlers | `server/internal/httpapi/handlers_tv.go` | `/api/v1/tv/*` (session required) |
| relay | `httpapi/relay.go` | `ua=` / `ref=` query params: a stream's fixed User-Agent / Referer, re-attached to every URL the manifest rewrite produces |
| TV screen | `web/src/screens/tv/index.ts` | rail item "TV" (when `/ping` has `tv: true`), sidebar (favourites, recent, countries, top categories), channel grid, long-press OK = favourite |
| player | `PlayerContext.live` | no timecodes / resume; prev/next zap through the current list |

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

- `https://`, no special headers → **direct**: the TV fetches the upstream
  itself. Residential IP, so geo-blocked Ukrainian channels work from home
  even though the VPS would be refused; no relay bandwidth.
- `http://` (mixed content on our https page), or a stream that needs a fixed
  User-Agent / Referer → through `/relay?u=…&ua=…&ref=…`. The manifest
  rewrite carries the same `ua`/`ref` onto every segment URL.

## API (bearer, `/api/v1/tv`)

| Method | Path | Notes |
|---|---|---|
| GET | `/meta` | `{countries:[{code,name,flag,channels}], categories:[{id,name,channels}]}` — alive channels only |
| GET | `/channels?country=UA&category=news&q=&fav=1&recent=1&limit=` | `{items:[{id,name,country,categories,logo,quality,streams,favorite}]}`, favourites first, then by name |
| GET | `/channels/{id}/play` | `{url, direct, quality, streams}`; records "recent"; `404 no_stream` |
| POST | `/channels/{id}/fail` | the player could not start it |
| PUT / DELETE | `/favorites/{id}` | |

## Not done yet

- EPG: iptv-org/epg is a grabber, not a feed — a CronJob per site for the
  configured countries, then "now / next" on tiles and in the player.
- Import of a provider's M3U / Xtream Codes (catchup, archive).
- Mini App / bot surfaces ("switch the TV to this channel").
- Channel numbers on the remote.
