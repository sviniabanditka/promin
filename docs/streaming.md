# Streaming

How media bytes reach the `<video>` element on a TV. Every media URL the client
plays is same-origin: the server proxies (`/relay`), re-containers (`/remux`)
or serves torrent data (`/stream`). All three routes sit behind
`requireAuthMedia` (`server/internal/httpapi/middleware.go`): a request needs
either an `Authorization: Bearer` header or `?t=<token>`, because `<video>`,
hls.js segment fetches and ffmpeg's loopback reads cannot set headers. Every
child URL the server generates (rewritten manifest lines, redirects, loopback
sources) carries that token via `withMediaToken` (`hls_token.go`) — the token is
only ever appended to our own origin, never forwarded upstream.

## 1. `/relay` — transparent proxy

`GET /relay?u=<base64url upstream URL>` (`server/internal/httpapi/relay.go`;
`u` is produced by `sources.EncodeRelayURL`, `server/internal/sources/relay.go`).

- **SSRF validation** (`validateUpstream`): scheme must be `http`/`https`; a
  literal IP that is loopback, link-local, unspecified or private is rejected.
  Hostnames are not resolved. `CheckRedirect` re-runs the check on every hop and
  stops after 10 redirects.
- **Request shaping**: the client's `Range` header is forwarded. The TV's own
  `User-Agent` is passed through when it contains `Mozilla/`; anything else
  (curl, bare media stacks) gets a fixed Chrome UA (`relayBrowserUA`) because
  some CDNs reject non-browser agents.
- **Timeouts**: no whole-request timeout (a segment or mp4 body may stream for
  the entire film); dial 10 s, TLS 10 s, response headers 15 s; up to 8 warm
  connections per host.
- **Redirects**: child URLs are resolved against the *final* URL after
  redirects, so a playlist that 30x-es to a CDN yields CDN segment paths.
- **Branch by content** (checked on the final path + `Content-Type`):
  - **Subtitles** (`.srt`/`.vtt`, `text/vtt`, `application/x-subrip`) →
    `relay_subtitle.go`: body (≤ 4 MiB) is converted to WebVTT if it is SRT
    (BOM dropped, `WEBVTT` header added, `,` → `.` in cue times, `{\an8}`-style
    tags stripped) and served as `text/vtt` with a 24 h cache.
  - **HLS manifests** (`.m3u8`, `*mpegurl*`) → `relayManifest`: body (≤ 4 MiB)
    is rewritten line by line. Every URI line and every `URI="..."` attribute
    (`#EXT-X-MEDIA`, `#EXT-X-KEY`, `#EXT-X-MAP`, `#EXT-X-I-FRAME-STREAM-INF`)
    is resolved and replaced with `/relay?u=...&t=<token>`. Served as
    `application/vnd.apple.mpegurl`, `Cache-Control: no-cache`.
  - **Everything else** (segments, mp4) → `relayPassthrough`: byte-for-byte
    copy preserving status code and `Content-Type`, `Content-Length`,
    `Content-Range`, `Accept-Ranges`, `Cache-Control`, `ETag`, `Last-Modified`.

## 2. `/remux` — ffmpeg jobs

`GET /remux?u=<base64url source>&audio=<n>&kind=<kind>&start=<sec>`
(`server/internal/httpapi/remux.go`) finds or creates a job and immediately
returns `{"job_id", "playlist_url": "/remux/<job>/playlist.m3u8"}`. The source
passes the same SSRF check as `/relay`.

**Kinds** (`server/internal/remux/job.go`, args in `remux/ffmpeg.go`):

| `kind` | ffmpeg | Use |
|---|---|---|
| `copy_hls` (default for non-`.mkv`) | `-c copy`, one video + one audio track, 6 s TS segments, `-reconnect` flags | demuxed-audio HLS → single muxed HLS for a client that cannot play separate audio groups |
| `copy_mkv` (default for `.mkv`) | video copied, audio → stereo AAC 160k, 6 s segments | MKV container → HLS for webviews that cannot demux Matroska; AC3/EAC3/DTS become playable AAC |
| `transcode_hevc` | `libx264 -preset ultrafast -crf 23 -g 48 -threads 4`, `scale=-2:'min(1080,ih)'`, `format=yuv420p` or an HDR→SDR `zscale`/`tonemap` chain, stereo AAC, 2 s segments | HEVC/AV1 (and HDR) sources for devices without a usable decoder |

All kinds write an `EXT-X-PLAYLIST-TYPE:EVENT` playlist under
`PROMIN_DATA_DIR/remux/<job>/`: it grows while ffmpeg runs (seekable from 0,
never treated as live by hls.js) and gets `#EXT-X-ENDLIST` on completion.
Exactly one audio track is muxed inline; switching tracks means a new job with
another `audio=<n>`. Multi-audio master output is not produced.

**Start offset**: `start=N` inserts `-ss N` *before* `-i` (`withInputSeek`) so
ffmpeg begins at the nearest keyframe instead of muxing from 0. Output time then
starts at 0; the playlist response carries `X-Remux-Start: <job start>` and the
player adds it back (`timeBase` in `web/src/core/player/index.ts`). The player
asks for it whenever it (re)loads a growing source more than 30 s in: resume,
an audio-track switch (`audio=<n>&start=<pos>`), a seek back before the
current job's start, or a seek more than 30 s past the muxed edge. The job
that answers may start elsewhere than `N` (see reuse below), which is why the
player always takes `timeBase` from the response, never from what it asked.

**Queue** (`remux/queue.go`): in-memory, no persistence. `submit` resolves a
request in this order:

1. exact dedup key `kind + source + audio + round(start)` → the live job (a job
   in `failed` state is dropped and retried);
2. **covering reuse** (`coveringLocked`): any non-failed job of the same
   `kind + source + audio` whose output already covers `start` — a finished job
   covers everything past its own start, a running one up to
   `start + MuxedSec() − 10 s` (`Job.MuxedSec` = EXTINF sum of its playlist).
   Switching a dub back, or seeking back into an earlier job's range, is served
   by that job with no new ffmpeg;
3. otherwise a new job. Before starting it, the **per-source cap**
   (`overCapLocked`) evicts the least recently accessed queued/running siblings
   of the same `kind + source` beyond `perSourceCap` − 1: copy kinds keep 2
   (the abandoned job survives one switch so a quick switch back reuses it),
   `transcode_hevc` keeps 1 (with `MaxTranscodes = 1` a second job would only
   queue behind the abandoned one). The source URL embeds the caller's token,
   so siblings are always this viewer's own earlier jobs — the ones its player
   has already stopped reading.

Copy jobs share a semaphore of `PROMIN_REMUX_MAX_COPY` (default 4);
transcodes have their own `PROMIN_REMUX_MAX_TRANSCODES` (default 1, prod 1 —
one HEVC decode eats 2–3 vCPU). Jobs idle (no `/remux/<job>/...` request) for
`PROMIN_REMUX_JOB_TTL` (default 30 min) are killed and their directory removed
(`destroy`: kill, wait for ffmpeg to exit, then delete); a killed job ends in
state `failed` (`context.Canceled`) so anything polling its state releases.
Leftover directories are wiped at startup. ffmpeg stderr is parsed for the source `Duration:` and the
audio stream table (`remux/hls_copy.go`); the last 6 lines are kept for failure
logs. Binaries: `PROMIN_FFMPEG_PATH`, `PROMIN_FFPROBE_PATH`; a missing ffmpeg
only logs a warning at startup — `/relay` keeps working without it. `zscale`
availability is probed once (`HasZscale`); without it HDR transcodes are plain
(washed) SDR.

**Serving** `GET /remux/<job>/<file>`:

- `playlist.m3u8` not yet written: `202 {"state","progress"[,"queue_position"]}`
  by default (the online path polls this JSON — `prepareStream`/
  `pollRemuxPlaylist`, up to 60 × 1.5 s); with `?hls=1` or for `stream-*.m3u8`
  a valid *empty live* playlist is returned instead, because an HLS engine may
  be loading it directly and would choke on JSON — for `playlist.m3u8` it
  already carries the `X-Remux-*` headers below; `master.m3u8` returns
  `503 Retry-After: 1`. A failed job returns `502 upstream_unavailable` (the
  ffmpeg tail goes to the server log only).
- Ready playlist (`setRemuxHeaders`): `X-Remux-Duration` (source total),
  `X-Remux-Start` (playlist time 0 == this source second; absent when 0),
  `X-Remux-Audio` (source track list, deduped by language, max 12), all listed
  in `Access-Control-Expose-Headers`; `appendRemuxToken` stamps `?t=` on every
  relative child URI (hls.js drops the master's query when resolving them).
- `seg-*.ts`: `http.ServeFile` (Range for free), immutable 1-year cache.
  Playlist and segment names are validated against traversal (`remux/hls_copy.go`).

## 3. `/stream` — torrents

Engine: `server/internal/torrent/manager.go` wraps anacrolix/torrent. Data under
`PROMIN_DATA_DIR/torrents/`. Limits: `PROMIN_TORRENT_MAX_ACTIVE` (default 5,
prod 3 — over the limit the oldest inactive torrent and its files are evicted),
`PROMIN_TORRENT_CACHE_LIMIT_GB` (default 80, LRU sweep in `lru.go`), idle drop
after 10 min without an open reader (data stays until LRU), metadata wait
`PROMIN_TORRENT_METADATA_TIMEOUT` (default 30 s), `PROMIN_TORRENT_PORT`.

API (`server/internal/httpapi/handlers_torrents.go`): search
`GET /api/v1/sources/torrents` (JacRed, `PROMIN_JACRED_BASE_URL`);
`POST|GET /api/v1/torrents/add?id=<magnet_id>` → `{infohash, files[]}`;
`GET /api/v1/torrents/active`; `GET /api/v1/torrents/audio?infohash=&file=`
(ffprobe track list for the player menu); `DELETE /api/v1/torrents/{infohash}`.

`GET /stream/{infohash}/{fileIdx}?t=...` — the URL the client builds in
`streamUrl()` (`web/src/core/api.ts`). Server decision order:

1. `transcode=1` (+ optional `hdr=1`) → `streamViaTranscode`: a
   `transcode_hevc` job whose source is our own loopback
   `/stream/...?t=` URL (`selfStreamURL`), so ffmpeg reads through the
   anacrolix reader (blocks until pieces arrive) rather than the raw `.part`
   file. No ffprobe on the request path — a cold torrent would stall it.
2. `.mkv` file and `mkv=false` → `streamViaRemux`: `copy_mkv` job, same source.
3. Otherwise progressive `http.ServeContent` with full Range support over
   `Manager.OpenFile` (sequential reader, 64 MB readahead, responsive mode).

Cases 1–2 call `Prefetch` (mark the whole file wanted so the muxed region
reaches the real end), pin a reader on the torrent while the job is queued or
running (`pinUntilDone`, protects it from idle drop/LRU), and `302` to
`/remux/<job>/playlist.m3u8?hls=1[&start=<job start>]&t=<token>`. `audio=N`
picks the inline track; `start=N` goes to the queue, which may answer with an
older job that already covers `N` (§2) — hence the job's real start in the
redirect URL and in `X-Remux-Start`. The player fetches `/stream` itself
(`prepareStream` → `pollRemuxPlaylist`), reads those, and hands the *final*
playlist URL to hls.js / `<video>`, so `/stream` is hit once per load and the
engine's playlist reloads go straight to `/remux/<job>/…`.

A job muxing from an offset reads the torrent through `/stream` with `Range`
requests; the anacrolix reader prioritises pieces only at the position being
read (nothing is requested after a bare seek), so a far offset downloads first
without any extra hint to the engine, while `Prefetch` keeps the rest of the
file at normal priority.

## 4. Client capabilities and routing

`web/src/core/capabilities.ts` computes once per boot and sends as query
params on `/api/v1/sources/online` and `/resolve` (`capsQuery()`):

| Param | Source | Server use |
|---|---|---|
| `platform` | `window.tizen`/UA token → `tizen`, `webos`, `androidtv`, `browser` | none (logging only) |
| `hevc` | `MediaSource.isTypeSupported('hvc1')` / `canPlayType` | none — the torrent transcode flag is decided client-side |
| `demuxed_hls` | `true`, `false` when Legacy TV mode is on | `handlers_sources.go` → `PreferMuxed`: an `.m3u8` stream URL is wrapped as `/remux?u=...&kind=copy_hls&audio=0` instead of `/relay` (`sources/parse.go wrapStream`) |
| `hls_native` | `canPlayType('application/vnd.apple.mpegurl')` | none server-side; client picks native `<video>` HLS only on modern Tizen (`preferNativeHls()`), hls.js everywhere else |
| `mkv` | `canPlayType('video/x-matroska')` | torrent path: `mkv=false` on `/stream` |
| `max_http_version` | `h2`, `h1` when Legacy TV mode is on | none — transport is handled by host choice (§5) |

Torrent transcode: `torrentMedia()` (`web/src/core/torrentPlay.ts`) sets
`transcode=1` when the release name matches `hevc|h.265|x265|av1|2160p|4k` and
`canDecodeHevc()` is false (`caps.hevc && preferNativeHls()` — hls.js/MSE cannot
decode HEVC even when `isTypeSupported` says yes); `hdr=1` when the name says
HDR/DV and not SDR. `remux.ProbeVideo` exists but is not called on any HTTP
path. There is no UA-based detection of old firmware anywhere.

hls.js (full build, `web/vendor/hls.min.js`, ~415 KB) is injected as a script
tag on first need (`web/src/core/player/hls.ts`), never bundled.

## 5. HTTP/1.1-only host: `h1.promin.club`

Pre-2022 Samsung Tizen (Chromium ~47) negotiates HTTP/2 via ALPN and then
breaks sustained media transfers over it (`PIPELINE_ERROR_NETWORK`). ALPN is
settled in the TLS handshake, before any HTTP request, so the server cannot
downgrade per device; Cloudflare's Free/Pro plans cannot switch h2 off for a
zone. Hence a second hostname with its own TLS policy (`k8s/promin.yaml`):

- `TLSOption h1only` — `alpnProtocols: ["http/1.1"]`.
- `Ingress promin-h1only` — hosts `h1.promin.club` and the alias
  `promin.sviniabanditka.com`, annotation
  `traefik.ingress.kubernetes.io/router.tls.options: promin-h1only@kubernetescrd`,
  cert `promin-h1-tls` (cert-manager HTTP-01). Traefik picks the TLSOption by
  SNI, so `promin.club` (Ingress `promin`, cert `promin-tls`) keeps h2.
- DNS: `h1.promin.club` is a **DNS-only** A record to the origin (no Cloudflare
  proxy — the edge would negotiate h2 again).
- Env `PROMIN_H1_HOST=h1.promin.club`, `PROMIN_MAIN_HOST=promin.club`; both are
  exposed by `GET /api/v1/ping` as `h1_host` / `main_host`.

Old Tizen also cannot play demuxed-audio HLS; that is fixed by `/remux`, not by
the host — see §4.

## 6. Legacy TV mode (manual switch)

Settings → "Old TV mode" (`legacy_tv_mode`, `web/src/core/settings.ts`).
Device-local: stored in this TV's `localStorage`, never synced to the account
(one old TV must not drag every device to the h1 host).

Effects when on: `setLegacyOverride(true)` → `demuxed_hls=false` (online HLS
goes through `copy_hls` remux), `max_http_version=h1`, `preferNativeHls()` is
false (hls.js), and `steerHost()` (`web/src/core/legacy.ts`) moves the app:
on boot and on toggle it fetches `/api/v1/ping` and, if the switch is on and
`location.hostname !== h1_host`, does `location.replace` to the h1 host with
`?legacy=1`; if the switch is off while on the h1 host, back to `main_host`
with `?legacy=0`. The flag rides in the URL because the two hosts are separate
origins with separate `localStorage` (`readLegacyFromURL`). An empty
`PROMIN_H1_HOST` disables steering. A device always starts from `promin.club`
and relocates itself; nothing inspects the User-Agent.
