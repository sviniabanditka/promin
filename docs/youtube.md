# YouTube section

A separate section of Promin — its own rail item, sidebar and content — for
watching YouTube on the same TVs, ad-free, with SponsorBlock. It shares the
player and the remux pipeline with films and series and nothing else: if the
sidecar is down, the section shows "unavailable" and the rest of Promin does
not notice. Background and the tests that led here: `docs/proposals/youtube.md`.

## Pieces

| Piece | Path | Role |
|---|---|---|
| `ytx` sidecar | `ytx/` (Node 22, `youtubei.js` + `googlevideo`) | Signs a profile into YouTube as the TV app (device code), reads the account's feeds through InnerTube's TV client, serves media tracks pulled over SABR |
| `youtube` package | `server/internal/youtube` | Client for the sidecar + cached SponsorBlock lookup |
| handlers | `server/internal/httpapi/handlers_yt.go` | `/api/v1/yt/*` (session required) |
| remux kind `mux2` | `server/internal/remux` | Two elementary inputs (video URL + audio URL) copy-muxed to the usual HLS EVENT playlist |
| deploy | `k8s/ytx.yaml` | Deployment + ClusterIP + PVC + NetworkPolicy (promin pods only) |

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
- **The TV client delivers adaptive formats only through SABR** (server-side
  ABR: a binary UMP protocol on `serverAbrStreamingUrl`). `googlevideo`'s
  `SabrStream` pulls a track; the sidecar writes the fragments to the HTTP
  response as they come. Measured on the node: first byte in 0.4 s.
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
| GET | `/video/{id}` | `{id, title, channel{id,name}, channel_avatar, duration_sec, views_text, published_text, playable, reason, qualities, related}` |
| GET | `/play/{id}?quality=1080p&sb=a,b` | submits a `mux2` job → `{job_id, playlist_url, quality, segments}`; play `playlist_url` with `?t=` like any remux |
| GET | `/segments/{id}?cats=` | SponsorBlock spans `{segments:[{category,start,end}]}` |

Item: `{kind: video|channel|playlist, id, title, channel{id,name}, duration_sec,
duration_text, meta[], thumbnail, progress_pct, live}`.

## Operations

- Sidecar image `ghcr.io/sviniabanditka/promin-ytx`, built and imported by the
  same workflow as promin; deployed by `kubectl set image` with the commit sha.
- When YouTube changes something: bump `youtubei.js` / `googlevideo` in
  `ytx/package.json`, run `npm run check`, redeploy. Symptoms: `502
  youtube_upstream` on browse, `409 unplayable` or a failed `mux2` job on play.
- Not done yet: seeking beyond the muxed range (SABR start offset), a
  per-profile cap on live tracks, the Mini App / bot surfaces.
