# YouTube section

A separate section of Promin — its own rail item, sidebar and content — for
watching YouTube on the same TVs, ad-free, with SponsorBlock. It shares the
player and the remux pipeline with films and series and nothing else: if the
sidecar is down, the section shows "unavailable" and the rest of Promin does
not notice. Background and the tests that led here: `docs/proposals/youtube.md`.

## Pieces

| Piece | Path | Role |
|---|---|---|
| `ytx` sidecar | `ytx/` (Node 22, `youtubei.js` + `googlevideo` + `bgutils-js`) | Signs a profile into YouTube as the TV app (device code), reads the account's feeds through InnerTube's TV client, serves media tracks pulled over SABR by that same signed-in session with the TV app's PO token (`attest.js`) |
| `youtube` package | `server/internal/youtube` | Client for the sidecar + cached SponsorBlock lookup |
| handlers | `server/internal/httpapi/handlers_yt.go` | `/api/v1/yt/*` (session required) |
| remux kind `mux2` | `server/internal/remux` | Two elementary inputs (video URL + audio URL) copy-muxed to the usual HLS EVENT playlist |
| deploy | `k8s/ytx.yaml` | Deployment + ClusterIP + PVC + NetworkPolicy (promin pods only) |
| self-check | `GET /v1/check` on the sidecar, called hourly by `server/internal/synthmon` | the first linked account plays a known video past the unattested cut; `promin_synthetic_ok{check="youtube"}` → alert `ProminYouTubeBroken` |
| TV screens | `web/src/screens/yt/` | Rail item "YouTube" (hidden while `youtube: false`), sidebar + shelves, video page, search; controller modes `yt_side` / `yt_content` / `yt_actions` / `yt_related` |

Config: `PROMIN_YTX_URL` (e.g. `http://ytx:8091`). Empty → the section is off:
`/api/v1/ping` reports `youtube: false`, the TV hides the rail item, the
routes answer `503 youtube_disabled`.

## Why it is shaped like this

- **Anonymous extraction from the VPS is bot-checked; a signed-in TV client is
  not.** The sign-in is YouTube's own device-code flow with the TV app's
  client identity (read from youtube.com/tv at runtime, the way SmartTube does
  it). One account per Promin profile; the refresh token lives in the
  sidecar's store (`/data/ytx/accounts.json`, 0600, own PVC).
- **The player request must carry the player's `signatureTimestamp`**,
  otherwise the TV client answers "The page needs to be reloaded" with no
  formats.
- **Every client delivers adaptive formats only through SABR** (server-side
  ABR: a binary UMP protocol on `serverAbrStreamingUrl`). `googlevideo`'s
  `SabrStream` pulls a track; the sidecar writes the fragments to the HTTP
  response as they come. Measured on the node: first byte in 0.4 s.
- **The media stream needs the TV app's own PO token.** The SABR server
  flips `StreamProtectionStatus` to 2 and cuts every stream after ~12 MB
  (~70 s of 1080p) unless the request carries a token it accepts for *that*
  client. Reconstructed from `tv-player-ias.js` (`ytx/src/attest.js`):
  `GET youtube.com/tv` with the OAuth bearer returns the signed-in TV app's
  `ytcfg` (`LIVING_ROOM_PO_TOKEN_ID`, `LIVING_ROOM_EACR_TOKEN`, `DATASYNC_ID`,
  `tvAppInfo`); every InnerTube request must carry
  `tvAppInfo.livingRoomPoTokenId`; the BotGuard challenge comes from
  `/att/get` (with `eacrToken`), the WAA request key is the TV player's
  (`Z1elNkAKLpSR3oPOUMSN`), and the session token is bound to
  `LIVING_ROOM_PO_TOKEN_ID`. It goes into `/player`
  (`serviceIntegrityDimensions.poToken`, with a `cpn`) and into the SABR
  request. Result: status 1, full tracks at ~1 MB/s. Everything else was
  measured and refused: web key / page challenge / video-id, visitor or
  datasync bindings (status 2), the old TV build's plain URLs (403 after
  ~10 MB), anonymous web clients from the VPS ("Sign in to confirm you're not
  a bot", also through the residential proxy), chained streams, the
  player-attestation `atr` ping. The signed-in TV client is not bot-checked
  from the VPS, and history, resume and age-restricted videos come with the
  account — no cookies, nothing per user beyond the device-code sign-in.
- If playback starts dying after ~70 s again: the ytx log shows `sabr stream
  protection status=2`; the sidecar then drops its cached TV config and token
  (`attest.js invalidate`). If it persists, YouTube changed the recipe — start
  from `tv-player-ias.js` (`html5_web_po_request_key`, `livingRoomPoTokenId`,
  `HF()`), not from the web player.
- **Resume and far seeks** start the SABR pull at the requested second:
  `SabrStream` has no seek API, so `ytx/src/stream.js` restores it with a
  phantom segment of that length (the first request is built as if no format
  were initialised, so the server still sends the init segment). Both tracks
  start at the same offset; ffmpeg's output timeline restarts at 0 and the
  player adds `X-Remux-Start`, exactly the torrent `start=` contract.
- **History and "continue watching"** on the account come from the stats
  pings (`ytx/src/watch.js`): `videostats_playback` once per playback
  session (the video lands in history), `videostats_watchtime` with
  `st/et/cmt` on every report. YouTube derives the tile's percent from the
  reported watch time, so the TV reports every ~20 s. Exact seconds for the
  same TV live in `localStorage` (`promin:yt:resume`); the account's percent
  is the cross-device fallback.
- **Phone surfaces.** Mini App tab "YouTube" (continue / subscriptions /
  search, a row opens the video page on the TV through `POST /tg/send
  {open_yt}` → `open_yt` sync event); bot: `/yt <query>` or the "▶ YouTube"
  menu button → eight results with "▶ n" buttons → the usual device picker.
- Not done yet: live streams, a per-profile cap on live tracks.
