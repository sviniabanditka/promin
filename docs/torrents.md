# Torrents

Two halves: a **search** facade over a JacRed-compatible indexer
(`server/internal/sources/torrents.go`, `client.go`) and an **embedded torrent
engine** built on `github.com/anacrolix/torrent`
(`server/internal/torrent/manager.go`, `lru.go`). HTTP glue lives in
`server/internal/httpapi/handlers_torrents.go`; the client helpers are
`web/src/core/api.ts` (`getTorrents`, `addTorrent`, `streamUrl`) and
`web/src/core/torrentPlay.ts`.

## 1. Search — `GET /api/v1/sources/torrents`

Upstream is the public JacRed API: `GET {PROMIN_JACRED_BASE_URL}/api/v1.0/torrents?search=<q>&apikey=<PROMIN_JACRED_APIKEY>`
(`Client.SearchTorrents`). Defaults: base `http://jac.red`, empty key. Each
call has a 20 s HTTP timeout; the whole `Service.Torrents` runs under
`jacredTimeout` = 12 s.

Query handling:

- The query is `title`, or `original_title` when `title` is empty. The year is
  **not** appended — the indexer's fuzzy search returns nothing for
  `"<title> <year>"`.
- If the localized query yields no rows and `original_title` differs, the
  original title is searched as a second attempt.
- No `title`/`original_title` at all → the handler answers an empty list
  without calling upstream.
- **Per-query cache**: results are stored in the shared KV cache under
  `jacred:<lowercased query>` for `torrentsCacheTTL` = 30 min
  (`searchTorrentsCached`).
- **429 backoff**: the indexer rate-limits per IP (a title page fires two
  queries). On `status 429` the client waits 2 s and retries once.
- Upstream failure → `{degraded: true, torrents: []}` with HTTP 200.

Normalization (`mapTorrents`):

- Rows without a magnet are dropped.
- For `type=tv` with `season>0`, rows that declare a `seasons[]` list not
  containing the season are dropped. Rows without a season list pass.
- **Infohash dedupe**: the public index returns one row per tracker for the
  same release. Rows sharing `urn:btih:<hash>` collapse into one; tracker names
  are joined with ", " and the best seeder/peer count wins.
- Output rows: `title, tracker, size, size_human, seeders, peers, quality
  ("1080p" or ""), voices[], id`. Sorted by seeders descending (zero-seed rows
  sink, not vanish).

`id` is the **magnet id**: `base64url(raw magnet URI)` without padding
(`EncodeMagnetID`). It is opaque only in the sense that a client cannot act on it
without going through `/api/v1/torrents/add`; it is not encrypted.

## 2. Engine — `torrent.Manager`

Config (`torrent.Config`, filled from `config.go`):

| Setting | Env | Default |
|---|---|---|
| Data dir | `PROMIN_DATA_DIR` → `<DataDir>/torrents/` | `/data` |
| Max active torrents | `PROMIN_TORRENT_MAX_ACTIVE` | 5 |
| On-disk cache limit | `PROMIN_TORRENT_CACHE_LIMIT_GB` | 80 GB |
| Metadata wait | `PROMIN_TORRENT_METADATA_TIMEOUT` | 30 s |
| Peer port | `PROMIN_TORRENT_PORT` | 0 → anacrolix default 42069 |
| Idle drop | — | 10 min |
| Readahead | — | 64 MiB |

Lifecycle:

- `AddMagnet` adds the magnet and blocks until the swarm delivers metadata
  (`ErrMetadataTimeout` → 504). If `MaxActive` is reached it evicts the oldest
  torrent with no open reader; if every torrent is being streamed →
  `ErrTooManyActive` (409). A torrent that is already added is reused.
- `ListFiles` returns `[{index, name, size, is_video}]`; `is_video` is an
  extension allowlist (`videoExts`). `LargestVideoFile` picks the default.
- `OpenFile` returns a `ReadSeekCloser` over the anacrolix reader with
  sequential download + readahead; each open reader pins the torrent.
- `Prefetch` marks the whole file wanted so a remux/transcode job muxes the
  full runtime.
- Background sweep (`StartBackgroundWorkers`): torrents with no reader for
  `IdleTimeout` are dropped from the client (network stops; files stay); when
  the tracked total exceeds the cache limit, least-recently-accessed torrents
  without an open reader are deleted from disk until usage falls under the
  hysteresis target.
- Bookkeeping lives in SQLite table `torrent_cache_meta` (`infohash, name,
  size, last_access`; `store.TorrentCacheRepo`). Data files are stored flat by
  torrent name. At boot `sweepOrphanFiles` deletes files with no row.

## 3. Endpoints

| Route | Auth | Purpose |
|---|---|---|
| `GET /api/v1/sources/torrents` | Bearer | search (section 1) |
| `GET\|POST /api/v1/torrents/add?id=<magnet_id>` (or `?magnet=<uri or b64>`) | Bearer | add + wait for metadata → `{infohash, files[]}`; HTTP budget 35 s |
| `GET /api/v1/torrents/active` | Bearer | `{torrents: [{infohash, name, size, downloaded, progress, readers, added_at}]}` |
| `GET /api/v1/torrents/audio?infohash=&file=` | Bearer | ffprobe the file through loopback `/stream` → `{tracks: [{index, lang, title}]}` |
| `DELETE /api/v1/torrents/{infohash}` | Bearer | drop torrent + files + cache row; 204 |
| `GET /stream/{infohash}/{fileIdx}` | Bearer or `?t=` | play (section 4) |

Full parameter tables are in `docs/api.md`.

## 4. Playback — `GET /stream/{infohash}/{fileIdx}`

The client builds the URL in `streamUrl()` from device capabilities
(`web/src/core/capabilities.ts`) and stamps the media token `?t=`.

| Query | Effect |
|---|---|
| (none) | progressive: `http.ServeContent` over the torrent reader, full `Range` support, `Content-Type` by extension (`.mkv` → `video/x-matroska`) |
| `mkv=false` | for an `.mkv` file: submit a `copy_mkv` remux job (container → HLS, no re-encode) and `302` to its playlist |
| `transcode=1` (+ `hdr=1`) | submit an HEVC/AV1 → H264 transcode (`hdr=1` adds tone-mapping) and `302` to its playlist. The decision is client-side, from the release name (`torrentPlay.ts`: `hevc|h265|x265|av1|2160p|4k`), so no ffprobe runs on a cold torrent |
| `audio=N` | which source audio track the remux/transcode muxes (`-map 0:a:N`, default 0). Switching tracks re-requests `/stream` with another index; the client does not rely on HLS alternate-audio renditions |
| `start=N` | seconds; the ffmpeg job starts at that offset (`-ss`) so a resume deep into the file plays immediately. Ignored on the progressive path (Range covers it) |

Remux and transcode jobs read the file through Promin's own loopback
`/stream/...?t=<caller's token>` (`selfStreamURL`), so ffmpeg gets valid bytes
through the anacrolix reader instead of a sparse on-disk `.part`. While the job
is queued or running, `pinUntilDone` holds a reader open so the torrent cannot be
idle-dropped or evicted. The redirect target is
`/remux/<job>/playlist.m3u8?hls=1&t=<token>`; `?hls=1` makes a not-ready
playlist come back as a valid empty live m3u8 instead of a 202 JSON body.

A playlist muxed from an offset exposes `X-Remux-Start: N` and
`X-Remux-Duration`; the player treats playlist time 0 as source time N.

### Packs and episodes

A series pack keeps every episode in one torrent. `torrentPlay.parseEpisode`
extracts season/episode from a file name (`S01E05`, `1x05`, `E05`, `05 серія`)
so each file gets its own timecode slot (`POST /api/v1/timecodes` with
`season`/`episode`); without it every file shared one position. When the pack
has more than one video file the player offers a file list for next/previous
episode.

### Resume

Positions are synced timecodes (see `docs/auth.md` §5). On resume the player
either seeks (progressive) or re-requests the HLS stream with `start=<pos>`
(remux/transcode), then treats the new playlist as starting at that position.

### What is remembered per title

The **client** keeps, in `localStorage` key `promin:lastsrc:<type>:<tmdb_id>`,
what was last played for that title: `{balanser: "torrent", torrent:
<magnet_id>, file: <index>, title}` for a torrent, or `{balanser, voice}` for an
online source (`web/src/screens/title.ts`). "Continue" on a title last played
from a torrent re-adds that magnet via `/api/v1/torrents/add`, picks the same
file index and starts playback at the saved timecode; if the torrent or file is
gone it falls back to the torrent list.

The **server** stores no per-title torrent choice. It keeps only the engine's
cache rows (`torrent_cache_meta`) and the downloaded data, both subject to the
LRU sweep above.
