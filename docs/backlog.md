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

- **Voice vs. in-stream audio track UX.** Both live in one menu now; decide
  whether a source voice change should preserve the in-stream track choice.
- **Mini-player / PiP**: keep the `<video>` alive across routes.
- **Remux job reaper by client liveness**: heartbeat while watching, kill idle
  jobs after a TTL instead of the current eviction rules.

## Torrents

- Season picker on the torrents tab (today all packs of a series are one list).
- Free-disk gate before a full prefetch (LRU + `PROMIN_TORRENT_MAX_ACTIVE` only).

## Frontend

- Shared popup/overlay lifecycle (`openPopup(box)` + tracked close) instead of
  per-screen variants.
- One spacing grid (3.2 / 4 / 6 / 8.4 rem today).
- Backdrop on list screens for consistency.
- Catalog filters by country / language (needs discover support in the backend).

## Sources

- Series whose requested season is missing from a catalog still appear in the
  source list and fail only at resolve time. A season check at listing time
  costs one extra request per title.
- Transcode HEVC/HDR → H.264/SDR on the fly for old panels: see `streaming.md`
  for what exists; the on-the-fly path for 4K/HEVC torrents is not complete.

## Candidates reviewed 2026-09-12 (not started)

Ranked by the owner's value; the top three are 8, 13 and 1 of this list.

For the viewer:
1. **Series calendar + notifications** — air dates from TMDB for bookmarked
   and watched series, a "coming soon" shelf on Home, bot message "new
   episode out, open on TV". The one feature that brings people back.
2. **Mini-player** — keep `<video>` alive in a corner while browsing the
   title page, similar titles or seasons; one press to expand.
3. **Watch together** — play/pause/seek sync between two TVs (or TV and
   phone) over the existing sync WebSocket.
4. **Search transliteration fallback** — zero results → retry the query
   mapped through the other keyboard layout.
5. **Skip intro / next episode by timing** — a "skip intro" button from a
   repeated segment between episodes or a manual mark remembered per show.
6. **Kids profile** — own PIN, TMDB content-rating filter, hidden shelves.
7. **Smarter Home shelves** — "because you watched X", "unfinished this
   week", "new in your genres"; the data is already in the DB.

Existing behaviour:
8. **Season check when listing sources** — series without the requested
   season still appear and fail only at resolve time.
9. **One popup lifecycle** (`openPopup` + tracked close) instead of the
   per-screen variants that caused several focus bugs.
10. **Season selector on the torrents tab.**
11. **Voice input in search** — a microphone key that pops the host IME on
    demand, on top of our own keyboard.
12. **Audit the title screen and the source picker** with the same review
    workflow the search screen went through.

Reliability and operations:
13. **Verify on real devices** what shipped blind: episode strip and seek
    ladder on Tizen, spinner and torrent resume on Xiaomi/MSX, the source
    matrix on one movie and one series (diagnostics mode is already there).
14. **On-the-fly HEVC/HDR → H.264/SDR** for old panels — the 4K torrent path
    is what most often "does not play".
15. **Remux live-job ceiling per profile** — the last audit item.
16. **Synthetic user monitor** — hourly headless run: login, search, resolve
    one source, first bytes of the stream; alert to the same Telegram.
17. **Dependabot** for actions and Go modules.

See also `docs/proposals/youtube.md`.

## Ideas (not planned)

- Personal media library: keep downloaded torrents on the VPS, seed, prefetch
  the next episode.
- Watch together: synchronized play/pause/seek over the sync WebSocket.
- "Coming soon / just aired" calendar from bookmarked series and TMDB air dates.
- Telegram companion: search and bookmark from the phone, notify when an
  episode airs.
- External subtitle search (OpenSubtitles or similar), optional.
