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

## Chosen 2026-09-19 (audit)

Picked by the owner after a repo audit. Ordered by value/cost; every item names
the machinery it stands on, because none of them starts from zero.

1. **Automatic skip intro.** The player already implements skip segments end to
   end (`PlayerContext.skipSegments`, the auto-skip loop at
   `web/src/core/player/index.ts:2807`) and nothing feeds them for films and
   series. Producer: an offline ffmpeg audio fingerprint over the first ~6
   minutes of two episodes of a season, the common stretch is the intro; store
   per show, compute once. Manual marking stays the fallback.

2. **Night audio (loudnorm).** Night mode dims the picture; the sound is
   untouched. `internal/remux/ffmpeg.go:194` already assembles a video filter
   chain and no audio filter at all — add `loudnorm`/compression behind a
   profile flag so explosions do not wake the house and whispers stay audible.

3. **Remembered dub.** The viewer picks the same Ukrainian voice every time.
   Store the chosen voice / audio track per profile and preselect it at resolve
   time, falling back to the nearest match.

4. **Two TVs in sync, one account.** Not "watch together with a friend" — the
   same account driving two sets in one home so they play the same frame.
   Everything needed exists: `EventRemote` is already a playback command aimed
   at one device (`internal/sync/hub.go:42`), the TV reports position, pause and
   duration every 5 s (`internal/sync/player_state.go`), and the remux queue
   reuses one job for several clients (`queue_reuse_test.go`), so the second set
   does not start a second transcode. Design: a watch group inside the account,
   one member leads; followers open the same title/source/position, then hold
   the drift with `playbackRate` 0.98/1.02 while |Δ| is 0.3–1.5 s and a hard
   seek past that. Pause/seek from any member fans out to the rest; ignore the
   echo of your own command by `device_id`.

5. **Timeshift and recording for Live TV (nDVR).** The biggest of the seven and
   the most distinctive. EPG is already stored whole per feed
   (`internal/tv/epg.go`) and the remux queue already writes HLS segments: keep
   a rolling 30–60 min window for favourite channels → "start this programme
   from the beginning", a pause that does not lose the broadcast, and "record"
   straight off the guide, with the recording appearing as an ordinary title.
   Needs the free-disk gate from *Torrents* above to land first.

6. **Live TV in the Telegram Mini App.** The Mini App has home, search,
   youtube, remote, library and settings (`web/miniapp/src/router.ts`) and no
   TV tab at all, while the backend already serves the catalogue, the EPG and
   the logo proxy (`internal/httpapi/handlers_tv.go`). Add a TV tab: channel
   list with "now / next" from the guide, favourites, and — the point of having
   it on the phone — "switch the TV to this channel" through the existing
   `EventRemote` path, plus playing the channel on the phone itself where the
   stream allows it.

## Candidates reviewed 2026-09-12 (not started)

Ranked by the owner's value; the top three are 8, 13 and 1 of this list.

For the viewer:
1. **Series calendar + notifications** — air dates from TMDB for bookmarked
   and watched series, a "coming soon" shelf on Home, bot message "new
   episode out, open on TV". The one feature that brings people back.
2. **Mini-player** — keep `<video>` alive in a corner while browsing the
   title page, similar titles or seasons; one press to expand.
3. **Watch together** — superseded by "Two TVs in sync" above (same account,
   two sets in one home), which is the shape the owner actually wants.
4. **Search transliteration fallback** — zero results → retry the query
   mapped through the other keyboard layout.
5. **Skip intro** — superseded by "Automatic skip intro" above.
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
17. **Dependabot** for actions and Go modules.

See also `docs/proposals/youtube.md`.

## Sources / subtitles

- **Search every subtitle in the library, not just the open film.** Line search
  inside the player is done (`docs/player.md`); the cross-title half needs the
  cached subtitle files indexed in SQLite FTS5 (modernc ships it) and a screen
  to ask "which film had that line".

## Live TV (docs/tv.md)
- EPG from the iptv-org/epg grabber for the configured countries ("now / next").
- Provider M3U / Xtream import with catchup.
- Mini App / bot: switch the TV to a channel; channel numbers on the remote.

## Ideas (not planned)

- Personal media library: keep downloaded torrents on the VPS, seed, prefetch
  the next episode.
- Watch together: synchronized play/pause/seek over the sync WebSocket.
- "Coming soon / just aired" calendar from bookmarked series and TMDB air dates.
- Telegram companion: search and bookmark from the phone, notify when an
  episode airs.
- External subtitle search (OpenSubtitles or similar), optional.
