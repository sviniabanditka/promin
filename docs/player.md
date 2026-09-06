# Player

The player is one module, `web/src/core/player/index.ts`, plus the lazy hls.js loader (`player/hls.ts`) and SVG icons (`player/icons.ts`). `openPlayer(ctx)` pushes it as a router activity; Back pops it and the title screen underneath resumes. The title screen (`web/src/screens/title.ts`) owns everything transport-specific — resolving sources, voices, episodes, torrent files — and hands the player a `PlayerContext` of media plus callbacks.

## Context

```ts
PlayerMedia   { type: 'hls' | 'mp4'; streams: Stream[]; subtitles: Subtitle[]; voices: Voice[];
                currentVoice?: string | null; audioNames?: string[] }
PlayerContext { title, subtitle, poster, media, tmdb_id, media_type, season, episode,
                resume?: { position_sec, duration_sec }, durationHint?: number,
                onProgress, onEnded, onVoice, onNext, onPrev, onEpisodes, onEpisode, loadAudioTracks }
```

Streams arrive best-first with same-origin URLs (`/relay`, `/remux`, `/stream`); `mediaUrl()` (`core/api.ts`) appends the session token as `?t=` because `<video>`/`<track>` cannot send headers.

## Engine

`startEngineWith()` picks the path per load:

| Media | Setting `player_engine` | Path |
|---|---|---|
| `mp4` | any | `<video src>` progressive, browser Range requests |
| `hls` | `native`, or `auto` on a modern Tizen with native HLS and legacy mode off | `<video src>` m3u8 |
| `hls` | `hlsjs`, or `auto` elsewhere | hls.js (`enableWorker: false`, generous manifest/level/fragment retries, `subtitleDisplay: false`, `startPosition` = resume target) with a `<video src>` fallback when MSE is missing |

`/remux?…` URLs are not playlists: `prepareStream()` fetches the job JSON, then polls `playlist_url` until it answers 200 (202 = still muxing; the body's `state`/`queue_position` is shown in the info bar as "preparing…"/"queued #n"), up to ~90 s. The 200 response's `X-Remux-Duration` becomes the authoritative total and `X-Remux-Audio` the list of source audio tracks.

Every (re)start bumps `loadSeq`; stale async chains bail. Fatal hls.js errors and native `error` events auto-recover in place up to 6 times (media error → `recoverMediaError`, network → `startLoad`, else reload at the last good position); the budget resets after 5 s of advancing playback. A stall watchdog (1 s tick) toasts after 25 s of frozen time and shows the error overlay (Retry / Change source) after 45 s. `showError()` tears the engine down so nothing keeps loading behind the overlay.

## UI layout

Everything is built once; availability is toggled with `.hide` (which also removes an element from D-pad navigation).

- **Info bar** (top, `player-info`): back chevron, poster thumbnail, title, second line "S1E3 · episode name · *remaining*", wall clock, and a values column with the resolution badge, a speed badge when not 1×, and the stats line.
- **Video layer**: buffering spinner, big centred play glyph while paused (pause wins over buffering), and the seek **OSD** in the centre ("▶▶ +2:30" plus the target time) while scrubbing.
- **Subtitle surface** (`player__subs`): the player's own cue renderer, positioned above the panel when it is visible.
- **"▲ Next episode" hint** (`player__skip`) during the last 90 s of an episode when `onNext` exists; UP while it shows (panel hidden) jumps.
- **Panel** (bottom, `player-panel`): timeline (buffer bar, progress, knob, time bubble), a line with current time and `total · −remaining · ends at HH:MM`, and a button row:
  - left, **labeled pills** (icon + caption + current value): Episodes, Audio, Subtitles, plus the "Next: 5. Name" caption;
  - centre: prev/next episode (series) or skip-to-start/end (movie), rewind, play/pause, forward;
  - right: quality pill (`1080p`, or `Auto · 720p` under ABR), speed pill (`1.5×`), and a "more" sheet (PiP when the API exists, mute, fit/fill framing, subtitle size).
- **Sheets** (`player__popup`): every menu is a bottom sheet above the panel. List layout is one column that scrolls the focused row into view; the **episode strip** layout is a horizontal row of cards with the still image, number badge, watched progress bar or ✓, name and "position / duration", slid with `translate3d` to keep the focused card centred. The sheet opens on the active item.
- **Countdown boxes** (`player__confirm`) for next-episode and end-of-movie prompts, and the **error overlay** (`player__error`).

The panel fades in on any key and auto-hides after 3.5 s unless paused, scrubbing or a sheet is open. While hidden, the per-tick timeline/stats DOM writes are skipped and repainted once on reveal. Mouse: `mousemove` reveals the panel, clicking the video toggles play, clicking the timeline seeks.

## Controller modes

| Mode | Focus | Keys |
|---|---|---|
| `player` | none (transport) | ←/→ scrub, OK play/pause, ↑ timeline (or Next when the hint shows), ↓ button row, Back hides the panel; a second Back within 2 s exits |
| `player_rewind` | timeline | ←/→ scrub, ↑ back to transport, ↓ button row, Back hides |
| `player_panel` | button row | ←/→ between buttons, ↑ timeline, ↓/Back hide |
| `player_menu` | sheet | ↑/↓ (list) or ←/→ (strip), OK select, Back close |
| `player_next` | countdown | ←/→, OK, Back = stay |
| `player_error` | overlay | ←/→ Retry / Change source, Back exits |

Remote media keys (⏯ ▶ ⏸ ⏹ ⏪ ⏩ ⏭ ⏮) are mixed into every mode. Any user action resets the auto-advance counter. All six modes are removed on destroy.

## Seek model

Time inside the player is **absolute source time**. `timeBase` is the offset between the engine's timeline and the source (0 for a normal stream); `absTime() = video.currentTime + timeBase`. Only `currentTime`, `seekable` and `buffered` are relative; every displayed or saved time adds `timeBase`.

**Scrubbing** (`rewind()`): the first ←/→ snapshots the position and pauses; each consecutive press moves a virtual target by a ladder — presses 1–5 → 10 s, 6–15 → 30 s, 16–30 → 60 s, then 300 s. The timeline, bubble ("1:02:30  +2:30") and OSD follow the target. `SEEK_APPLY_MS = 600` after the last press, `rewindApply()` seeks for real and resumes only if playback was not paused before the scrub. The target is clamped to `duration − 2` so a held key never lands on `ended`. With an unknown/infinite duration the key shows a one-time "seek unavailable" toast.

**Direct jumps**: skip-to-start/end buttons, timeline click (`clientX` → fraction), digits 0–9 → 0–90 %.

All seeks go through `seekClamped(pos)`:

- a target before `timeBase − 1` restarts the engine with `start=<pos>` (only a new mux job can play it);
- for a **growing source** (an explicit `/remux` job, or a torrent `/stream` answered with HLS) a target past `seekEnd()` — `min(duration, seekable.end)` — is stashed in `pendingSeek` and applied from `timeupdate` or the 1 s stats tick once the muxed edge reaches it; a stash past the authoritative end is dropped;
- VOD and progressive sources seek directly (hls.js / Range fetch the target).

**Duration** (`videoDuration()`): `authDuration` when known (hls.js `LEVEL_UPDATED` with a non-live playlist → `totalduration + timeBase`; native `video.duration`; `X-Remux-Duration`), else the `durationHint` seeded from the TMDB runtime for growing torrent sources, else `video.duration`. The `ended` fallback fires when `absTime ≥ authDuration − 1`, because a growing playlist never emits `ended`.

## Resume from position

`core/progress.ts` defines the thresholds for the whole client: `RESUME_MIN_S = 60`, `FINISH_RATIO = 0.9`; `isResumable(pos, dur)` = `60 < pos < 0.9·dur`. The title screen passes the cached timecode as `ctx.resume`; `armResume()` runs at mount and after every episode switch (each episode carries its own `resume` in `EpisodeMeta`).

Flow: on `loadedmetadata` a resumable position is applied silently with a toast "Resumed at mm:ss", the panel is revealed with focus on play/pause, and a position within 5 s of this stream's end is discarded. Then:

- hls.js VOD: `startPosition` is the resume target, so the first fragment loaded is the right one;
- native paths: `seekClamped()` then `verifyResumeSeek()` checks the first real ticks and re-issues the seek up to twice if `|cur − target| > 3 s`; any explicit user seek cancels the verification;
- growing sources with a target > 30 s: the stream URL gets `start=<pos>` so ffmpeg muxes from there, `timeBase = pos`, and the stash mechanism owns any remaining overshoot.

Progress is emitted every 10 s, after a seek, on pause, on episode switch, on `pagehide`/`visibilitychange`, and on destroy, via `ctx.onProgress(pos, dur)` → `sync.saveTimecode` (local cache + `POST /timecodes`, last-write-wins). A growing source reports nothing until its total is known, and a position of 0 is never written.

## Quality

`pickQualityIndex()` preselects a stream from `default_quality`: exact match on the label/quality text, else the first playable one. On `auto` the ceiling is **1080p** — 2160p streams are listed but only chosen by hand. A stream labelled HEVC on a device where `canDecodeHevc()` is false is listed dimmed as "(unsupported)" and skipped by the D-pad.

Two menu shapes:

- several `media.streams` → one row per stream; picking reloads in place at the same position;
- a single HLS master with `hls.levels.length > 1` → `Auto` + one row per distinct height (best first); picking sets `hls.currentLevel` (`-1` = auto) with no reload. The pill shows `Auto · <current>p` under ABR.

The chosen height is remembered for the session (`wantQualityNum`) and re-applied after voice/episode switches as exact height, else the nearest lower playable one.

## Audio: one merged menu

The Audio pill opens a single sheet with up to two sections:

- **In stream** — instant switches: source tracks of a remux (`X-Remux-Audio`, or torrent tracks from `ctx.loadAudioTracks` → `GET /torrents/audio`) switch by re-requesting the stream with `audio=<index>` and reloading in place; hls.js tracks (captured from `MANIFEST_PARSED`/`AUDIO_TRACKS_UPDATED`, de-duplicated by name+lang across failover groups) switch via `hls.audioTrack`; native `video.audioTracks` toggle `enabled`. Generic names like `audio_1` are replaced by the language name, and `media.audioNames` (the source's real dub names in manifest order) win when present.
- **From source** — the resolve response's `voices`; picking one calls `ctx.onVoice`, which re-resolves and reloads at the kept position (toast "Switching voice: …", a cold source can take ~25 s).

The pill's value shows the dub currently playing. The picked in-stream track (`wantAudio`) is re-selected on the next episode.

## Subtitles

Sources: sidecar files from the resolve response (`<track kind="subtitles">` appended to the video) and WebVTT renditions inside an HLS master (`hls.subtitleTracks`). Exactly one may be active; "Off" disables all. The `/relay` endpoint converts `.srt` to WebVTT on the fly (`server/internal/httpapi/relay_subtitle.go`), so SRT sidecars render like VTT.

Rendering is the player's own: every text track stays `mode = 'hidden'` (hls.js keeps `subtitleDisplay: false`), and `renderCues()` paints the active cues into `player__subs` on each tick with tags stripped. Size comes from `subtitle_size` (`player--subs-small/medium/large`, adjustable from the "more" sheet and the settings screen). The chosen subtitle (`wantSub`, label first, language second) carries over to the next episode.

## Playback speed

Speeds 0.5–2× in seven steps. The rate is a **global per-user setting** (`player_speed`, synced), applied after every load because `<video>` resets it; when the element refuses the rate (some native pipelines) a toast says so and the pill/badge show the rate actually in effect.

## Episodes, next and end

- `ctx.onEpisodes` fills the strip menu and the "Next: N. Name" caption; `ctx.onEpisode(n)` jumps to any episode. Season boundaries are crossed by the title screen.
- On `ended` with `onNext`: a "Next episode in 10 s" box with **Cancel focused**, showing the next episode's label; OK on Cancel stays, "Now" proceeds. After three unattended auto-advances the box asks "Still watching?" and does not count down.
- On `ended` without `onNext` (movie): "Finished — back to the card in 15 s" with "To card" / "Stay".
- Switching episodes saves the outgoing position first, resets audio index/recovery budget, applies remembered quality/subtitle picks, and arms the new episode's resume.

## Stats line

`updateStats()` runs every second: resolution badge from `videoWidth×videoHeight`; with hls.js attached, `Channel <bandwidthEstimate> Mbit/s • Bitrate ~<level bitrate> Mbit/s • Buffer <seconds ahead>`; without hls.js the slot shows the remux preparation state, if any.

## Torrent playback

`core/torrentPlay.ts` builds the media for a torrent file: `/stream/{infohash}/{index}?audio=0` plus `mkv=false` when the webview cannot demux Matroska and `transcode=1` (`hdr=1` for HDR names) when the release name says HEVC/AV1/2160p and `canDecodeHevc()` is false. The backend then 302-redirects to an HLS playlist, so `media.type` is `hls` and the player treats it as a growing source. Series packs map files to episodes with `parseEpisode()` (`S01E05`, `1x05`, `E05`, "05 серія") so each file has its own timecode slot and prev/next/strip work across the pack.

Resume deep into such a file adds `start=<pos>`; the server starts ffmpeg with `-ss` and answers with `X-Remux-Start`; the player's `timeBase` is the start it asked for.

## Keyboard map

| Key | Action |
|---|---|
| ← / → | scrub (ladder above) |
| ↑ / ↓ | timeline / button row (see modes) |
| OK / Enter | play-pause, or activate the focused control |
| Back (×2 within 2 s) | exit to the title screen |
| 0–9 | jump to 0–90 % |
| Space | play/pause |
| M | mute |
| ⏯ ▶ ⏸ ⏹ ⏪ ⏩ ⏭ ⏮ | matching transport action; ⏹ exits |
