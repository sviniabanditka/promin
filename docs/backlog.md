# Backlog

Open work only. Finished items are removed rather than ticked; the state of the
system itself is described in the other documents.

## Housekeeping

- `k8s/promin-h1.yaml` (nginx HTTP/1.1 front on :8444) is a fallback kept until
  the pre-2022 Samsung Tizen path via `h1.promin.club` is confirmed on a real
  set. Delete it and the CI step once confirmed.
- Verify on the real devices what was shipped blind: episode strip and seek
  ladder on Tizen, spinner centering and torrent resume on Xiaomi/MSX, the
  source matrix (every provider on one movie and one series).

## Player

- **Audio track switch on torrents re-muxes from zero.** `/stream?audio=N`
  starts a fresh ffmpeg job; a switch mid-film is not instant and costs
  disk/CPU. Options: multi-rendition HLS from one job, or keep-alive of the
  previous job.
- **Far seek on a torrent that is still downloading** waits for the download to
  reach the target. A true instant far-seek is an `-ss`-from-Range job plus a
  player time-base offset (the offset half already exists: `start=N` and
  `timeBase`).
- **Voice vs. in-stream audio track UX.** Both live in one menu now; decide
  whether a source voice change should preserve the in-stream track choice.
- **Mini-player / PiP**: keep the `<video>` alive across routes.
- **Error states**: uniform messages for source-unavailable, relay failure,
  remux failure, torrent with no peers.
- **Remux job reaper by client liveness**: heartbeat while watching, kill idle
  jobs after a TTL instead of the current eviction rules.

## Torrents

- Season picker on the torrents tab (today all packs of a series are one list).
- Free-disk gate before a full prefetch (LRU + `PROMIN_TORRENT_MAX_ACTIVE` only).

## Frontend

- The `history` table and `/api/v1/history` are never written by the TV; watch
  history everywhere is derived from `timecodes`. Either drop the table and
  endpoints or start writing "watched" events into it deliberately.

- `api.ts`: abort the underlying request on timeout (sockets stay open).
- `scroll.ts`: `transitionend` listeners can accumulate → double lazy-append.
- Catalog: pagination race on fast filter changes (`reqSeq` guard), and a
  visible indicator while the next page loads.
- Shared popup/overlay lifecycle (`openPopup(box)` + tracked close) instead of
  per-screen variants.
- `sync.ts`: WebSocket without a token does not reconnect; trailer timer must be
  cleared on destroy.
- One spacing grid (3.2 / 4 / 6 / 8.4 rem today).
- Backdrop on list screens for consistency.
- Catalog filters by country / language (needs discover support in the backend).

## Sources

- Series whose requested season is missing from a catalog still appear in the
  source list and fail only at resolve time. A season check at listing time
  costs one extra request per title.
- Transcode HEVC/HDR → H.264/SDR on the fly for old panels: see `streaming.md`
  for what exists; the on-the-fly path for 4K/HEVC torrents is not complete.

## Ideas (not planned)

- Personal media library: keep downloaded torrents on the VPS, seed, prefetch
  the next episode.
- Watch together: synchronized play/pause/seek over the sync WebSocket.
- "Coming soon / just aired" calendar from bookmarked series and TMDB air dates.
- Telegram companion: search and bookmark from the phone, notify when an
  episode airs.
- External subtitle search (OpenSubtitles or similar), optional.
