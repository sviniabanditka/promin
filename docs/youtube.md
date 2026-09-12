# YouTube section

A separate section of Promin — its own rail item, sidebar and content — for
watching YouTube on the same TVs, ad-free, with SponsorBlock. It shares the
player and the remux pipeline with films and series and nothing else: if the
sidecar is down, the section shows "unavailable" and the rest of Promin does
not notice. Background and the tests that led here: `docs/proposals/youtube.md`.

## Pieces

| Piece | Path | Role |
|---|---|---|
| `ytx` sidecar | `ytx/` (Node 22, `youtubei.js` + `googlevideo` + `bgutils-js`) | Signs a profile into YouTube as the TV app (device code), reads the account's feeds through InnerTube's TV client, serves media tracks pulled over SABR by an anonymous web client with PO tokens |
| `youtube` package | `server/internal/youtube` | Client for the sidecar + cached SponsorBlock lookup |
| handlers | `server/internal/httpapi/handlers_yt.go` | `/api/v1/yt/*` (session required) |
| remux kind `mux2` | `server/internal/remux` | Two elementary inputs (video URL + audio URL) copy-muxed to the usual HLS EVENT playlist |
| deploy | `k8s/ytx.yaml` | Deployment + ClusterIP + PVC + NetworkPolicy (promin pods only) |
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
- **Media is fetched anonymously, as the web client, with PO tokens.** The
  SABR server flips `StreamProtectionStatus` to 2 and cuts the stream after
  ~12 MB (~70 s of 1080p) unless the request carries a PO token it accepts.
  A BotGuard token minted the way the web player does it (`bgutils-js` in
  Node + jsdom, challenge taken from the youtube.com page) is accepted by the
  WEB client (status 1, 128 MB in 40 s) but not by the signed-in TV client;
  the old TV build that still hands out plain URLs caps them at ~10 MB too.
  So `ytx/src/stream.js` keeps one anonymous web session (visitor data +
  session-bound token, rebuilt every 4 h) and mints a video-bound token per
  play. Consequences: playback is not written to the account's watch history
  and age-restricted videos do not play.
- **TV layouts are not modelled by youtubei.js**, so `ytx/src/tv.js` asks for
  raw JSON and normalises `tileRenderer` / `lockupViewModel` shelves, grids,
  playlist lists and watch-next pivots into one shape.
- **SponsorBlock** uses the hash-prefix endpoint (`/api/skipSegments/<sha256
  prefix>`), so the service never sees the exact video id; answers are cached
  6 h; failures degrade to "no segments".

## API (bearer, `/api/v1/yt`)

| Method | Path | Notes |
|---|---|---|
| GET | `/account` | `{linked, pending, user_code?, verification_url?, expires_at?, error?}` |
| POST | `/account/login` | starts the device flow; poll `/account` until `linked` |
| DELETE | `/account` | unlink (revokes the token) |
| GET | `/browse/{page}?cont=` | `home\|subscriptions\|history\|playlists\|library\|liked\|watch_later\|UC…\|VL…` → `{shelves:[{title, items, cont}], cont}` |
| GET | `/search?q=&cont=` | same shape |
| GET | `/video/{id}` | `{id, title, channel{id,name}, channel_avatar, duration_sec, views_text, published_text, playable, reason, qualities, resume_sec, related}`; `resume_sec` comes from the history tile's percent watched (0 = start over) |
| GET | `/play/{id}?quality=1080p&start=N&sb=a,b` | submits a `mux2` job → `{job_id, playlist_url, quality, segments}`; `start=N` (seconds) begins the SABR pull there and the playlist carries `X-Remux-Start`, like torrent offset jobs — the player uses it for resume and far seeks |
| POST | `/watch/{id}` `{position_sec, duration_sec}` | reports a position to the account's YouTube history (stats pings as the signed-in TV client); the TV player calls it every ~20 s and on seeks |
| GET | `/segments/{id}?cats=` | SponsorBlock spans `{segments:[{category,start,end}]}` |

Item: `{kind: video|channel|playlist, id, title, channel{id,name}, duration_sec,
duration_text, meta[], thumbnail, progress_pct, live}`.

## Operations

- Sidecar image `ghcr.io/sviniabanditka/promin-ytx`, built and imported by the
  same workflow as promin; deployed by `kubectl set image` with the commit sha.
- When YouTube changes something: bump `youtubei.js` / `googlevideo` /
  `bgutils-js` in `ytx/package.json`, run `npm run check`, redeploy. Symptoms:
  `502 youtube_upstream` on browse, `409 unplayable` or a failed `mux2` job on
  play, `sabr stream protection status=2` in the ytx log (PO token no longer
  accepted → playback stops after about a minute).
- The player treats `/remux/<job>/playlist.m3u8` as a growing source and
  polls it until 200, the same as torrent HLS; SponsorBlock spans ride in
  `PlayerContext.skipSegments` and are skipped from the 1 s stats tick.
- Live streams are reported `playable: false, reason: "live"`: SABR delivered
  no bytes for them in tests, so the TV shows "not supported yet" instead of a
  hanging player.
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
  is the cross-device fallback. No age-restricted videos (media is anonymous).
- Not done yet: live streams, a per-profile cap on live tracks, the Mini
  App / bot surfaces.
