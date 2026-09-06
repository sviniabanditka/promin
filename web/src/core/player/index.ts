// HTML5 <video> player overlay (docs/frontend.md, docs/streaming.md).
//
// UI/UX is a minimalist port of Lampa's player panel (yumata/lampa-source
// src/interaction/player/panel.js + video.js): a thin glass timeline with a
// buffer bar, a filled progress bar and a position pointer, current/total
// time, a compact quality/audio/subtitle button row, a big centered
// play glyph on pause and a soft spinner while buffering. The whole panel
// fades in on any key and auto-hides after ~3.5s of inactivity.
//
// Seek mirrors Lampa's rewind() exactly (video.js:1392): the first left/right
// snapshots currentTime, each subsequent press accelerates the step
// (force *= 1.03) and moves a *virtual* rewind position along the timeline
// (video is paused, the pointer + a floating time bubble track the target);
// the real video.currentTime is only assigned ~1s after the last press
// (rewindEnd), then playback resumes. This keeps Range-heavy progressive
// sources from being hammered and gives the familiar "scrub" feel on a remote.
//
// Engine choice (docs/frontend.md pickEngine) is unchanged from ./hls.ts:
//   - mp4 → progressive <video src>; hls native (Tizen/Safari) → m3u8 into
//     src; hls elsewhere → lazy hls.js. Every URL is already same-origin.
//
// ES5 target (swc-transpiled): const/let/arrow/class/template-literals are OK,
// but NO async/await, for-of, spread, Array.find/includes, or Object.assign at
// runtime — core-js is not bundled.

import { Navigator } from '../nav';
import Controller, { on, trigger, ControllerCalls } from '../controller';
import {
  ICO_VOICE,
  ICO_SUBS,
  ICO_EPISODES,
  ICO_PP_PLAY,
  ICO_PP_PAUSE,
  ICO_PREV,
  ICO_NEXT,
  ICO_TSTART,
  ICO_TEND,
  ICO_RPREV,
  ICO_RNEXT,
  ICO_BACK,
  ICO_MORE,
} from './icons';
import * as router from '../router';
import { t } from '../i18n';
import { toast } from '../../ui/toast';
import { el, empty, pad2 } from '../../ui/dom';
import { ScreenInstance } from '../activity';
import { preferNativeHls, canDecodeHevc } from '../capabilities';
import { getDefaultQuality, getPlayerEngine, getPlayerSpeed, setPlayerSpeed, getSubSize, setSubSize, SubSize } from '../settings';
import { isResumable } from '../progress';
import { report as diag } from '../diag';
import { ensureHls, HlsInstance, HlsCtor } from './hls';
import { Stream, Subtitle, Voice, mediaUrl } from '../api';

export interface PlayerMedia {
  type: 'hls' | 'mp4';
  streams: Stream[];
  subtitles: Subtitle[];
  voices: Voice[];
  // Currently selected voice id (so the voice menu can show a checkmark and
  // resolve is not repeated for the one already playing).
  currentVoice?: string | null;
  // Labels for in-stream HLS audio tracks, by rendition index (see api.ts).
  audioNames?: string[];
}

export interface PlayerContext {
  title?: string;
  // Second info-bar line: "S1E3 · Episode name" (tv) or the file name (torrent).
  subtitle?: string;
  // Poster path for the info-bar thumbnail.
  poster?: string | null;
  media: PlayerMedia;
  // Passed through to the Phase-3 timecode hooks (currently no-op).
  tmdb_id?: number | string;
  media_type?: string;
  season?: number | null;
  episode?: number | null;
  // Re-resolve for a different voice (owned by screens/sources so the player
  // stays transport-agnostic). done(null) on failure keeps current playback.
  onVoice?: (voiceId: string, done: (media: PlayerMedia | null) => void) => void;
  // Backend sync hooks (Phase 3). onProgress fires ~every 10s and on exit;
  // onEnded fires when the media finishes. screens/sources wires them to
  // core/sync timecode upserts.
  onProgress?: (position: number, duration: number) => void;
  onEnded?: () => void;
  // Saved playback position (from the sync timecode cache). When present and
  // sensible (>60s, <90%), the player asks "Continue from mm:ss / From start".
  resume?: { position_sec: number; duration_sec: number } | null;
  // Real total duration (seconds) for a stream whose HLS playlist grows as it's
  // muxed/transcoded server-side (torrent remux/transcode) — the seekbar would
  // otherwise creep up with the playlist. Seeded from the TMDB runtime since the
  // torrent path never sees the X-Remux-Duration header. Overridden if that
  // header does arrive (online path).
  durationHint?: number;
  // Autoplay the next episode (tv). The player calls this on ended; sources
  // resolves the next episode's streams and calls done(media, meta) — or
  // done(null) when there is no next episode.
  onNext?: (
    done: (media: PlayerMedia | null, meta?: EpisodeMeta) => void
  ) => void;
  // Play the previous episode (tv), symmetric to onNext. Absent → prev button
  // stays hidden (no previous episode, e.g. episode 1).
  onPrev?: (
    done: (media: PlayerMedia | null, meta?: EpisodeMeta) => void
  ) => void;
  // Episode list for the in-player "Episodes" menu (current season) and the
  // "Next: 5. Name" caption. current = the episode number playing now.
  onEpisodes?: (done: (list: PlayerEpisode[], current: number) => void) => void;
  // Jump to an arbitrary episode of the current season (from the list).
  onEpisode?: (
    episode: number,
    done: (media: PlayerMedia | null, meta?: EpisodeMeta) => void
  ) => void;
  // Lazily load the stream's selectable audio tracks (torrent path — the
  // player switches by re-requesting the stream with a different audio index,
  // since the bundled hls.js can't play HLS alternate-audio renditions). Called
  // once after open; done([]) or absent → no track menu.
  loadAudioTracks?: (done: (tracks: CtxAudioTrack[]) => void) => void;
}

export interface PlayerEpisode {
  episode: number;
  name?: string;
  still?: string | null;
  position_sec?: number;
  duration_sec?: number;
}

export interface CtxAudioTrack {
  index: number;
  lang?: string;
  title?: string;
}

// Metadata the source screen hands back with a switched episode. resume carries
// that episode's own saved timecode so a partially-watched next/prev episode
// resumes instead of always starting at 0.
export interface EpisodeMeta {
  season?: number | null;
  episode?: number | null;
  title?: string;
  subtitle?: string;
  resume?: { position_sec: number; duration_sec: number } | null;
}

// Scrub step by consecutive-press count: predictable ladder instead of
// Lampa's 10s×1.03^n (20 presses ≈ 4.5 min, 50 ≈ 19 min — nobody can tell how
// long to hold). 1–5 → 10s, 6–15 → 30s, 16–30 → 1 min, then 5 min.
function scrubStep(n: number): number {
  return n <= 5 ? 10 : n <= 15 ? 30 : n <= 30 ? 60 : 300;
}
// Delay after the last press before the seek is actually applied (rewindEnd).
// 600ms: enough to absorb key repeat, short enough not to read as lag.
const SEEK_APPLY_MS = 600;
// Panel auto-hide after inactivity.
const PANEL_HIDE_MS = 3500;

function speedLabel(rate: number): string {
  return (Math.round(rate * 100) / 100) + '×';
}

function fmtTime(sec: number): string {
  if (!isFinite(sec) || sec < 0) sec = 0;
  const s = Math.floor(sec % 60);
  const m = Math.floor((sec / 60) % 60);
  const h = Math.floor(sec / 3600);
  if (h > 0) return h + ':' + pad2(m) + ':' + pad2(s);
  return pad2(m) + ':' + pad2(s);
}

function mountPlayer(container: HTMLElement, ctx: PlayerContext): ScreenInstance {
  container.className += ' player-screen';

  let media = ctx.media;
  let hls: HlsInstance | null = null;
  // Real total duration for a remux stream, from the /remux playlist's
  // X-Remux-Duration header. A remux playlist grows as ffmpeg muxes, so
  // video.duration creeps up (5m → 15m → …); this is the true length so the
  // seekbar/time show the correct total from the start. 0 = use video.duration.
  let remuxDuration = 0;
  // Offset (seconds) between the engine's timeline and the source: a remux
  // job started with start=N (-ss) writes a playlist whose 0 is source time N.
  // Every user-facing time is ABSOLUTE (playlist time + timeBase): timeline,
  // clocks, saved progress, scrub targets. Only video.currentTime/seekable/
  // buffered are relative. absTime() and seekClamped() are the two bridges.
  let timeBase = 0;
  function absTime(): number {
    return (video.currentTime || 0) + timeBase;
  }
  // Source audio renditions of a remux stream (X-Remux-Audio). The muxed output
  // carries a single track, so alternatives can't come from the stream itself —
  // switching re-requests /remux with audio=<index> and reloads in place.
  interface RemuxAudioTrack {
    index: number;
    lang?: string;
    title?: string;
  }
  let remuxAudio: RemuxAudioTrack[] = [];
  let remuxAudioIndex = 0;
  // In-manifest WebVTT renditions captured off hls.js events (declared up here:
  // refreshButtons() reads it before the subtitle section below runs).
  interface HlsSubTrack {
    id?: number;
    name?: string;
    lang?: string;
  }
  let hlsSubs: HlsSubTrack[] = [];
  // Torrent audio tracks (from ctx.loadAudioTracks). Unlike remuxAudio (seeded
  // per-stream from the X-Remux-Audio header and reset on reload), these persist
  // for the whole session and drive the same audio=N re-mux switch.
  let ctxAudio: RemuxAudioTrack[] = [];
  let destroyed = false;
  // Generation token for engine (re)starts. Every startEngine bumps it; async
  // steps (prepareStream, remux poll, ensureHls, hls ERROR) bail when their
  // captured value is stale. Without it, two fast reloads (quality/voice/episode
  // switch) let the first async chain create a second hls.js on the same <video>
  // — leak + wrong stream. See stability audit #2.
  let loadSeq = 0;
  // Transient-stall auto-recovery for native <video> streams (torrents mostly):
  // the browser fires a network error (code 2) when a torrent read stalls on a
  // piece the swarm hasn't delivered yet. That's not fatal — reload keeping the
  // position and playback resumes once the piece lands. Only after several fast
  // failures in a row (a genuinely dead source) do we surface the error screen.
  let recoverAttempts = 0;
  let recoverTimer = 0;
  const MAX_RECOVER = 6;

  // Preselect the stream matching the user's "default quality" setting
  // (docs/frontend.md — a preselect, not a hard limit; streams are best-first so
  // 'auto' → index 0). Matches on the quality/label text ("1080p" ⊃ "1080").
  // Streams arrive best-first. A stream this device cannot decode (HEVC on an
  // engine without it) is never auto-picked; on 'auto' the ceiling is 1080p —
  // native sources now hand out 2160p files, and 4K over home Wi-Fi into a TV
  // webview is a gamble the user should opt into via the quality menu.
  function playable(s: Stream): boolean {
    const hay = ((s.quality || '') + ' ' + (s.label || '')).toUpperCase();
    return !(hay.indexOf('HEVC') !== -1 && !canDecodeHevc());
  }
  function qualityOf(s: Stream): number {
    const m = /(\d{3,4})p?/.exec((s.quality || '') + ' ' + (s.label || ''));
    return m ? parseInt(m[1], 10) : 0;
  }
  function pickQualityIndex(streams: Stream[]): number {
    const pref = getDefaultQuality();
    if (pref === 'auto') {
      let fallback = -1;
      for (let i = 0; i < streams.length; i++) {
        if (!playable(streams[i])) continue;
        if (fallback < 0) fallback = i;
        const q = qualityOf(streams[i]);
        if (q === 0 || q <= 1080) return i;
      }
      return fallback < 0 ? 0 : fallback;
    }
    for (let i = 0; i < streams.length; i++) {
      const s = streams[i];
      const hay = (s.quality || '') + ' ' + (s.label || '');
      if (hay.indexOf(pref) !== -1 && playable(s)) return i;
    }
    for (let i = 0; i < streams.length; i++) if (playable(streams[i])) return i;
    return 0;
  }

  let streamIndex = pickQualityIndex(media.streams); // index into media.streams (quality)
  diag('player:mount', {
    type: media.type,
    streams: media.streams.length,
    pick: streamIndex,
    url: (media.streams[streamIndex] || { url: '' }).url.slice(0, 40),
    resume: ctx.resume ? Math.round(ctx.resume.position_sec) + '/' + Math.round(ctx.resume.duration_sec) : null,
    season: ctx.season,
    episode: ctx.episode,
    hint: ctx.durationHint || 0,
  });

  // ---- DOM (classes/structure ported 1:1 from Lampa player/video + panel + info) ----
  const root = el('div', 'player tv');
  const video = document.createElement('video');
  video.className = 'player-video__video';
  video.setAttribute('crossorigin', 'anonymous');
  video.setAttribute('playsinline', 'true');
  (video as unknown as { autoplay: boolean }).autoplay = true;
  root.appendChild(video);

  // Video overlay layer: buffering loader + big pause glyph (Lampa player-video).
  const videoLayer = el('div', 'player-video');
  const spinner = el('div', 'player-video__loader');
  videoLayer.appendChild(spinner);
  const centerPlay = el('div', 'player-video__paused hide');
  centerPlay.innerHTML = ICO_PP_PLAY; // shown while paused → "press to resume"
  videoLayer.appendChild(centerPlay);
  // Seek OSD (centre): "▶▶ +2:30" + the target time while scrubbing.
  const osdEl = el('div', 'player__osd hide');
  const osdDelta = el('div', 'player__osd-delta');
  const osdTime = el('div', 'player__osd-time');
  osdEl.appendChild(osdDelta);
  osdEl.appendChild(osdTime);
  videoLayer.appendChild(osdEl);
  root.appendChild(videoLayer);
  // Own subtitle surface (tracks stay hidden; renderCues paints them here).
  const subsEl = el('div', 'player__subs');
  root.appendChild(subsEl);
  // "▲ Next episode" prompt during the last 90s (credits heuristic — no chapter
  // data from any source). UP while it shows jumps; OK still pauses.
  const SKIP_WINDOW_S = 90;
  const skipEl = el('div', 'player__skip hide', '\u25B2 ' + t('player.btn_next'));
  root.appendChild(skipEl);
  function paintSkip(cur: number, dur: number): void {
    const show = !!ctx.onNext && dur > 0 && isFinite(dur) && cur > 0 && dur - cur <= SKIP_WINDOW_S && !nextBox && !errorBox;
    skipEl.classList.toggle('hide', !show);
  }
  function skipVisible(): boolean {
    return !skipEl.classList.contains('hide');
  }
  root.classList.add('player--subs-' + getSubSize());

  // Top info bar (Lampa player-info) — back arrow + title + wall clock on the
  // first line, a resolution badge + live channel/bitrate/buffer stats on the
  // second.
  const info = el('div', 'player-info');
  const infoBody = el('div', 'player-info__body');
  const infoLine = el('div', 'player-info__line');
  const backBtn = el('div', 'player-info__back selector');
  backBtn.innerHTML = ICO_BACK;
  on(backBtn, 'hover:enter', function () {
    exitToSources();
  });
  // Poster thumb + two text lines (title / "S1E3 · name · 42:10 left"); the
  // clock and the live stats sit in a right-hand column.
  const posterEl = el('div', 'player-info__poster');
  if (ctx.poster) {
    const pimg = el('img') as HTMLImageElement;
    pimg.alt = '';
    pimg.src = ctx.poster;
    posterEl.appendChild(pimg);
  }
  const textCol = el('div', 'player-info__text');
  const titleEl = el('div', 'player-info__name', ctx.title || '');
  const subEl = el('div', 'player-info__sub');
  let subBase = ctx.subtitle || '';
  textCol.appendChild(titleEl);
  textCol.appendChild(subEl);
  const rightCol = el('div', 'player-info__right');
  const clockEl = el('div', 'player-info__time');
  const clockSpan = el('span', 'time--clock', '');
  clockEl.appendChild(clockSpan);
  rightCol.appendChild(clockEl);
  infoLine.appendChild(backBtn);
  infoLine.appendChild(posterEl);
  infoLine.appendChild(textCol);
  infoLine.appendChild(rightCol);
  infoBody.appendChild(infoLine);
  function paintSub(cur: number, dur: number): void {
    let text = subBase;
    if (dur > 0 && isFinite(dur) && cur >= 0) {
      const left = t('player.remaining', { t: fmtTime(Math.max(0, dur - cur)) });
      text = text ? text + ' · ' + left : left;
    }
    if (subEl.textContent !== text) subEl.textContent = text;
  }

  const infoValues = el('div', 'player-info__values');
  const valSize = el('div', 'value--size');
  const valSizeSpan = el('span', '', '');
  valSize.appendChild(valSizeSpan);
  const valStat = el('div', 'value--stat');
  const valStatSpan = el('span', '', '');
  valStat.appendChild(valStatSpan);
  const valSpeed = el('div', 'value--speed hide');
  const valSpeedSpan = el('span', '', '');
  valSpeed.appendChild(valSpeedSpan);
  infoValues.appendChild(valSize);
  infoValues.appendChild(valSpeed);
  infoValues.appendChild(valStat);
  rightCol.appendChild(infoValues);

  info.appendChild(infoBody);
  root.appendChild(info);

  // Bottom panel (Lampa player-panel).
  const panel = el('div', 'player-panel');
  const panelBody = el('div', 'player-panel__body');

  // Timeline: peding (buffer) + position (progress + knob via ::after) + time
  // bubble. A .selector so it can hold focus in the player_rewind ring (Lampa
  // gives the timeline its own controller).
  const timeline = el('div', 'player-panel__timeline selector');
  const buffer = el('div', 'player-panel__peding');
  const progress = el('div', 'player-panel__position');
  const pointer = el('div');
  progress.appendChild(pointer);
  timeline.appendChild(buffer);
  timeline.appendChild(progress);
  const bubble = el('div', 'player-panel__time hide');
  timeline.appendChild(bubble);
  panelBody.appendChild(timeline);

  // Line one: current / total time.
  const lineOne = el('div', 'player-panel__line player-panel__line-one');
  const timeCur = el('div', 'player-panel__timenow', '00:00');
  const timeDur = el('div', 'player-panel__timeend', '00:00');
  lineOne.appendChild(timeCur);
  lineOne.appendChild(timeDur);
  panelBody.appendChild(lineOne);

  // Panel focus bookkeeping — declared before the button DOM so the builders
  // below can record the last-focused control safely.
  let lastFocused: HTMLElement | false = false;

  // Round icon .button (Lampa player-panel__right). onEnter fires on OK/click.
  function makeIconButton(cls: string, icon: string, onEnter: () => void): HTMLElement {
    const b = el('div', 'player-panel__' + cls + ' button selector');
    b.innerHTML = icon;
    on(b, 'hover:focus', function () {
      lastFocused = b;
    });
    on(b, 'hover:enter', onEnter);
    return b;
  }
  // Text pill .button (quality) with a live label.
  function makeTextButton(cls: string, label: string, onEnter: () => void): HTMLElement {
    const b = el('div', 'player-panel__' + cls + ' button selector', label);
    on(b, 'hover:focus', function () {
      lastFocused = b;
    });
    on(b, 'hover:enter', onEnter);
    (b as unknown as { _setLabel: (s: string) => void })._setLabel = function (s: string) {
      b.textContent = s;
    };
    return b;
  }
  // Icon + visible caption (+ small state value): the left "what am I
  // watching" group — Episodes / Audio / Subtitles — reads without focusing.
  function makeLabeledButton(cls: string, icon: string, labelKey: string, onEnter: () => void): HTMLElement {
    const b = el('div', 'player-panel__' + cls + ' button button--labeled selector');
    b.innerHTML = icon;
    b.appendChild(el('span', 'button__label', t(labelKey)));
    const value = el('span', 'button__value', '');
    b.appendChild(value);
    on(b, 'hover:focus', function () {
      lastFocused = b;
    });
    on(b, 'hover:enter', onEnter);
    (b as unknown as { _setLabel: (s: string) => void })._setLabel = function (s: string) {
      value.textContent = s;
    };
    return b;
  }
  // Tooltip-style name shown above a focused icon button (CSS ::after).
  function nameBtn(b: HTMLElement, key: string): void {
    b.setAttribute('data-label', t(key));
  }
  function setLabel(b: HTMLElement, s: string): void {
    const fn = (b as unknown as { _setLabel?: (s: string) => void })._setLabel;
    if (fn) fn(s);
  }

  // Line two: left (episode nav) / center (transport) / right (options) —
  // the full Lampa footer button row. Every button is built once; unavailable
  // ones carry .hide (Navigator skips display:none), toggled by refreshButtons.
  const lineTwo = el('div', 'player-panel__line player-panel__line-two');

  // LEFT — "what am I watching": Episodes / Audio / Subtitles with captions,
  // plus the next-episode name.
  const left = el('div', 'player-panel__left');
  const btnEpisodes = makeLabeledButton('episodes', ICO_EPISODES, 'player.episodes', openEpisodesMenu);
  btnEpisodes.classList.add('hide');
  const btnVoice = makeLabeledButton('voice', ICO_VOICE, 'player.voice', openVoiceMenu);
  const btnSubs = makeLabeledButton('subs', ICO_SUBS, 'player.subs', openSubsMenu);
  const nextName = el('div', 'player-panel__next-episode-name hide');
  left.appendChild(btnEpisodes);
  left.appendChild(btnVoice);
  left.appendChild(btnSubs);
  left.appendChild(nextName);
  lineTwo.appendChild(left);

  // CENTER — outer pair is prev/next EPISODE for a series and skip-to-start/end
  // for a movie (same slots, icon + handler swap in refreshButtons); inner pair
  // rewinds; play/pause in the middle.
  function seriesNav(): boolean {
    return !!(ctx.onNext || ctx.onPrev);
  }
  const center = el('div', 'player-panel__center');
  const btnTStart = makeIconButton('tstart', ICO_TSTART, function () {
    if (seriesNav()) goPrev();
    else seekTo(0);
    showPanel();
  });
  const btnRPrev = makeIconButton('rprev', ICO_RPREV, function () {
    rewind(false);
  });
  const playpause = el('div', 'player-panel__playpause button selector');
  const ppPlay = el('div');
  ppPlay.innerHTML = ICO_PP_PLAY;
  const ppPause = el('div');
  ppPause.innerHTML = ICO_PP_PAUSE;
  playpause.appendChild(ppPlay);
  playpause.appendChild(ppPause);
  on(playpause, 'hover:focus', function () {
    lastFocused = playpause;
  });
  on(playpause, 'hover:enter', function () {
    togglePlay();
    showPanel();
  });
  const btnRNext = makeIconButton('rnext', ICO_RNEXT, function () {
    rewind(true);
  });
  const btnTEnd = makeIconButton('tend', ICO_TEND, function () {
    if (seriesNav()) {
      goNext();
    } else {
      const dur = videoDuration();
      if (dur > 0) seekTo(Math.max(0, dur - 1));
    }
    showPanel();
  });
  center.appendChild(btnTStart);
  center.appendChild(btnRPrev);
  center.appendChild(playpause);
  center.appendChild(btnRNext);
  center.appendChild(btnTEnd);
  lineTwo.appendChild(center);

  // RIGHT — "how": quality pill, speed pill, "more" sheet (PiP, sound,
  // framing, subtitle size).
  const controls = el('div', 'player-panel__right');
  const btnQuality = makeTextButton('quality', 'auto', openQualityMenu);
  // Speed is a text pill ("1.5×") rather than a gear icon: the rate persists
  // per user across titles (owner decision, docs/player.md), so it has to be
  // visible at a glance — not discoverable only by opening a menu.
  const btnSpeed = makeTextButton('speed', speedLabel(getPlayerSpeed()), openSettingsMenu);
  // No fullscreen button: the player already fills the viewport on every TV
  // target, and the Fullscreen API is a no-op (or absent) in the Tizen/MSX
  // webview — the control did nothing but take focus. PiP is the useful
  // sibling and stays, gated on the API actually existing.
  const canPip =
    typeof (video as unknown as { requestPictureInPicture?: unknown }).requestPictureInPicture === 'function' &&
    (document as unknown as { pictureInPictureEnabled?: boolean }).pictureInPictureEnabled !== false;
  const btnMore = makeIconButton('more', ICO_MORE, openMoreMenu);
  controls.appendChild(btnQuality);
  controls.appendChild(btnSpeed);
  controls.appendChild(btnMore);
  lineTwo.appendChild(controls);
  nameBtn(btnRPrev, 'player.btn_rewind');
  nameBtn(playpause, 'player.btn_playpause');
  nameBtn(btnRNext, 'player.btn_forward');
  nameBtn(btnQuality, 'player.quality');
  nameBtn(btnSpeed, 'player.speed');
  nameBtn(btnMore, 'player.more');
  panelBody.appendChild(lineTwo);

  panel.appendChild(panelBody);
  root.appendChild(panel);
  container.appendChild(root);

  // Overlays built on demand.
  let errorBox: HTMLElement | null = null;

  // ---- panel visibility ----
  let hideTimer = 0;
  let panelVisible = false;

  function armHide(): void {
    if (hideTimer) window.clearTimeout(hideTimer);
    hideTimer = window.setTimeout(function () {
      // Never hide while paused or mid-scrub.
      if (!video.paused && !scrubbing) autoHide();
    }, PANEL_HIDE_MS);
  }
  function showPanel(): void {
    const wasHidden = !panelVisible;
    panelVisible = true;
    root.classList.add('player--panel-visible');
    panel.classList.add('panel--visible');
    info.classList.add('info--visible');
    // Paint once on reveal: the per-tick paints are skipped while hidden, so
    // without this the timeline/stats would show the values from the last time
    // the panel was open.
    if (wasHidden) repaintPanel();
    armHide();
  }
  // Full one-shot repaint of the panel's live values (used on reveal).
  // "1:52:00 · до 23:41": wall-clock end at the current rate — the number a
  // viewer on a couch actually wants at 22:00.
  function durText(cur: number, dur: number): string {
    const total = fmtTime(dur);
    if (!(dur > 0) || !isFinite(dur)) return total;
    let text = total + ' · \u2212' + fmtTime(Math.max(0, dur - cur));
    if (!video.paused) {
      const rate = video.playbackRate || 1;
      const end = new Date(Date.now() + ((dur - cur) / rate) * 1000);
      text += ' · ' + t('player.ends_at', { time: pad2(end.getHours()) + ':' + pad2(end.getMinutes()) });
    }
    return text;
  }
  function repaintPanel(): void {
    const dur = videoDuration();
    const cur = absTime();
    timeCur.textContent = fmtTime(cur);
    timeDur.textContent = durText(cur, dur);
    paintSub(cur, dur);
    paintTimeline(cur, dur);
    onBuffered();
    updateStats();
  }
  function hidePanel(): void {
    panelVisible = false;
    root.classList.remove('player--panel-visible');
    panel.classList.remove('panel--visible');
    info.classList.remove('info--visible');
  }
  let suppressReveal = false;
  function autoHide(): void {
    hidePanel();
    // Drop button/timeline focus back to transport so left/right seek again —
    // but WITHOUT re-showing the panel (setMode→player.toggle calls showPanel,
    // which re-armed the hide timer and doubled the effective delay).
    if (currentMode === 'player_panel' || currentMode === 'player_rewind') {
      timeline.classList.remove('focus');
      suppressReveal = true;
      setMode('player');
      suppressReveal = false;
    }
  }

  // Quality pill label from the selected stream (falls back to "Quality").
  // One HLS master with several variant levels (lampac collaps/rezka): the
  // quality choice lives in hls.js, not in media.streams.
  function hlsLevelsUsable(): boolean {
    return media.streams.length <= 1 && !!hls && !!hls.levels && hls.levels.length > 1;
  }
  function hlsLevelHeight(idx: number): number {
    return hls && hls.levels && hls.levels[idx] && hls.levels[idx].height ? (hls.levels[idx].height as number) : 0;
  }
  // Pill shows the bare resolution ("1080p"); the full label ("1080p HEVC",
  // torrent file names) lives in the menu. With hls.js levels: "AUTO · 720p"
  // while ABR drives, the fixed height otherwise.
  function qualityLabel(): string {
    if (hlsLevelsUsable() && hls) {
      const h = hlsLevelHeight(hls.currentLevel);
      const auto = hls.autoLevelEnabled !== false && wantQualityNum === 0;
      if (auto) return t('player.auto') + (h ? ' · ' + h + 'p' : '');
      return h ? h + 'p' : t('player.auto');
    }
    const s = media.streams[streamIndex];
    if (!s) return t('player.quality');
    const q = qualityOf(s);
    return q ? q + 'p' : s.label || s.quality || t('player.quality');
  }
  function updateQualityButton(): void {
    btnQuality.classList.toggle('hide', media.streams.length <= 1 && !hlsLevelsUsable());
    setLabel(btnQuality, qualityLabel());
  }
  // Pick the hls.js level for the remembered height: exact, else nearest lower,
  // else -1 (auto).
  function levelForHeight(h: number): number {
    if (!hls || !hls.levels) return -1;
    let best = -1;
    let bestH = 0;
    for (let i = 0; i < hls.levels.length; i++) {
      const lh = hls.levels[i].height || 0;
      if (lh === h) return i;
      if (lh < h && lh > bestH) {
        bestH = lh;
        best = i;
      }
    }
    return best;
  }
  function applyWantLevel(): void {
    if (!hls || !hlsLevelsUsable()) return;
    try {
      hls.currentLevel = wantQualityNum > 0 ? levelForHeight(wantQualityNum) : -1;
    } catch (e) {
      /* ignore */
    }
    updateQualityButton();
  }

  // Toggle button availability after any media change (voice re-resolve / next
  // episode). Buttons stay in the DOM; only .hide changes (Lampa model).
  function refreshButtons(): void {
    updateQualityButton();
    updateSubsButton();
    btnEpisodes.classList.toggle('hide', !ctx.onEpisodes);
    // Caption value: the dub that's playing (from the source's list).
    let vname = '';
    for (let i = 0; i < (media.voices || []).length; i++) {
      if (media.voices[i].id === media.currentVoice) vname = media.voices[i].name;
    }
    setLabel(btnVoice, vname);
    const series = seriesNav();
    btnTStart.innerHTML = series ? ICO_PREV : ICO_TSTART;
    btnTEnd.innerHTML = series ? ICO_NEXT : ICO_TEND;
    nameBtn(btnTStart, series ? 'player.btn_prev' : 'player.btn_start');
    nameBtn(btnTEnd, series ? 'player.btn_next' : 'player.btn_end');
    btnTStart.classList.toggle('hide', series && !ctx.onPrev);
    btnTEnd.classList.toggle('hide', series && !ctx.onNext);
    updateAudioTracks();
  }

  // ---- episode list (tv) ----
  let episodes: PlayerEpisode[] = [];
  let curEpisode: number | null = ctx.episode != null ? ctx.episode : null;
  function loadEpisodes(): void {
    if (!ctx.onEpisodes) return;
    ctx.onEpisodes(function (list, current) {
      if (destroyed) return;
      episodes = list || [];
      if (current != null) curEpisode = current;
      refreshNextName();
    });
  }
  function nextEpisode(): PlayerEpisode | null {
    if (curEpisode == null) return null;
    for (let i = 0; i < episodes.length; i++) if (episodes[i].episode === curEpisode + 1) return episodes[i];
    return null;
  }
  function epLabel(ep: PlayerEpisode): string {
    return ep.episode + (ep.name ? '. ' + ep.name : '');
  }
  // "Next: 5. Name" beside the prev/next buttons; "Next season" on the last
  // episode when the source screen can cross the boundary (onNext present).
  function refreshNextName(): void {
    const nx = nextEpisode();
    let text = '';
    if (nx) text = t('player.next_ep', { n: epLabel(nx) });
    else if (ctx.onNext && episodes.length && curEpisode != null) text = t('player.next_season');
    nextName.textContent = text;
    nextName.classList.toggle('hide', !text);
  }
  function epSub(ep: PlayerEpisode): string {
    const d = ep.duration_sec || 0;
    const p = ep.position_sec || 0;
    if (d > 0 && p >= d * 0.9) return '\u2713 ' + fmtTime(d);
    if (p > 0 && d > 0) return fmtTime(p) + ' / ' + fmtTime(d);
    return d > 0 ? fmtTime(d) : '';
  }
  function openEpisodesMenu(): void {
    if (!ctx.onEpisode || !episodes.length) return;
    const opts: MenuOption[] = [];
    for (let i = 0; i < episodes.length; i++) {
      (function (ep: PlayerEpisode) {
        const d = ep.duration_sec || 0;
        const p = ep.position_sec || 0;
        opts.push({
          label: ep.name || t('title.episode') + ' ' + ep.episode,
          sub: epSub(ep),
          still: ep.still,
          badge: String(ep.episode),
          progress: d > 0 ? Math.min(1, p / d) : 0,
          done: d > 0 && p >= d * 0.9,
          active: ep.episode === curEpisode,
          onSelect: function () {
            if (ep.episode === curEpisode) return;
            switchEpisode(function (done) {
              (ctx.onEpisode as NonNullable<PlayerContext['onEpisode']>)(ep.episode, done);
            });
          },
        });
      })(episodes[i]);
    }
    openMenu(t('player.episodes'), opts, 'strip');
  }

  refreshButtons();
  // Default panel focus target (the centered play/pause button).
  lastFocused = playpause;

  // ---- subtitle track ----
  // Two sources: sidecar files from the resolve response (<track src>) and
  // WebVTT renditions inside an HLS master (hls.js subtitleTracks). Exactly
  // one may be showing.
  let subTrack: HTMLTrackElement | null = null;
  let currentSub: Subtitle | null = null;
  function captureHlsSubs(data: unknown): void {
    const d = data as { subtitleTracks?: HlsSubTrack[] };
    let src: HlsSubTrack[] = (d && d.subtitleTracks) || [];
    if (!src.length && hls && hls.subtitleTracks) src = hls.subtitleTracks;
    hlsSubs = src;
    updateSubsButton();
    applyWantSub();
  }
  function updateSubsButton(): void {
    const any = (media.subtitles && media.subtitles.length > 0) || hlsSubs.length > 0;
    btnSubs.classList.toggle('hide', !any);
  }
  function hlsSubOff(): void {
    if (hls && typeof hls.subtitleTrack === 'number' && hls.subtitleTrack !== -1) {
      try {
        hls.subtitleTrack = -1;
        hls.subtitleDisplay = false;
      } catch (e) {
        /* ignore */
      }
    }
  }
  function selectHlsSub(tr: HlsSubTrack, idx: number): void {
    applySubtitle(null); // drops any sidecar track + disables every TextTrack
    if (!hls) return;
    try {
      // subtitleDisplay stays false: hls.js keeps the track hidden but still
      // loads its cues, which renderCues paints.
      hls.subtitleTrack = tr.id != null ? tr.id : idx;
    } catch (e) {
      /* ignore */
    }
    setLabel(btnSubs, tr.name || tr.lang || 'sub');
  }
  // Paint the active cues of every hidden TextTrack (ours, or hls.js's). Tags
  // are stripped; line breaks survive via white-space: pre-line.
  let lastCueText = '';
  function renderCues(): void {
    let text = '';
    const tracks = video.textTracks;
    if (tracks) {
      for (let i = 0; i < tracks.length; i++) {
        const tr = tracks[i] as unknown as { mode: string; activeCues: ArrayLike<{ text?: string }> | null };
        if (tr.mode !== 'hidden' || !tr.activeCues) continue;
        for (let j = 0; j < tr.activeCues.length; j++) {
          const c = tr.activeCues[j].text || '';
          if (c) text += (text ? '\n' : '') + c.replace(/<[^>]+>/g, '');
        }
      }
    }
    if (text === lastCueText) return;
    lastCueText = text;
    empty(subsEl);
    if (text) subsEl.appendChild(el('span', '', text));
  }
  function applySubtitle(sub: Subtitle | null): void {
    currentSub = sub;
    if (subTrack && subTrack.parentNode) subTrack.parentNode.removeChild(subTrack);
    subTrack = null;
    hlsSubOff();
    lastCueText = '';
    empty(subsEl);
    const tracks = video.textTracks;
    if (tracks) {
      for (let i = 0; i < tracks.length; i++) {
        (tracks[i] as unknown as { mode: string }).mode = 'disabled';
      }
    }
    if (!sub) {
      if (btnSubs) setLabel(btnSubs, t('player.off'));
      return;
    }
    const track = document.createElement('track');
    track.kind = 'subtitles';
    track.src = mediaUrl(sub.url);
    if (sub.lang) track.srclang = sub.lang;
    track.label = sub.label || sub.lang || 'sub';
    video.appendChild(track);
    subTrack = track;
    window.setTimeout(function () {
      // hidden, not showing: cues load, the browser doesn't paint them — we do.
      if (track.track) (track.track as unknown as { mode: string }).mode = 'hidden';
    }, 0);
    if (btnSubs) setLabel(btnSubs, sub.label || sub.lang || 'on');
  }

  // ---- popup menus (quality / voice / subs) ----
  let popup: HTMLElement | null = null;

  interface MenuOption {
    label: string;
    // Optional second line (episode progress).
    sub?: string;
    // Non-selectable section title.
    header?: boolean;
    // Strip layout (episodes): thumbnail, number badge, watched progress 0..1.
    still?: string | null;
    badge?: string;
    progress?: number;
    done?: boolean;
    active: boolean;
    // Listed but not pickable (a stream this device can't decode): rendered
    // dimmed and without .selector so the D-pad skips it.
    disabled?: boolean;
    onSelect: () => void;
  }

  // Bottom sheet above the panel. layout 'list' (default): one column of rows;
  // 'strip': a horizontal row of episode cards (still, number, progress) that
  // slides to keep the focused card in view.
  function openMenu(titleText: string, options: MenuOption[], layout?: 'strip'): void {
    closeMenu(true);
    const strip = layout === 'strip';
    const returnMode = currentMode;
    const box = el('div', 'player__popup' + (strip ? ' player__popup--strip' : ''));
    box.appendChild(el('div', 'player__popup-title', titleText));
    const viewport = el('div', 'player__popup-viewport');
    const list = el('div', 'player__popup-list');
    viewport.appendChild(list);
    box.appendChild(viewport);

    let lastItem: HTMLElement | false = false;
    function slideTo(item: HTMLElement): void {
      // Centre the focused card; clamp to the strip's ends.
      const vw = viewport.clientWidth;
      const max = Math.max(0, list.scrollWidth - vw);
      let x = item.offsetLeft - (vw - item.offsetWidth) / 2;
      if (x < 0) x = 0;
      if (x > max) x = max;
      list.style.transform = 'translate3d(' + -Math.round(x) + 'px, 0, 0)';
    }
    for (let i = 0; i < options.length; i++) {
      (function (opt: MenuOption) {
        if (opt.header) {
          list.appendChild(el('div', 'player__popup-head', opt.label));
          return;
        }
        let item: HTMLElement;
        if (strip) {
          item = el('div', 'player__ep selector' + (opt.active ? ' is-active' : '') + (opt.done ? ' is-done' : ''));
          const thumb = el('div', 'player__ep-thumb');
          if (opt.still) {
            const img = el('img') as HTMLImageElement;
            img.alt = '';
            img.src = opt.still;
            thumb.appendChild(img);
          }
          if (opt.badge) thumb.appendChild(el('div', 'player__ep-num', opt.badge));
          if (opt.done) {
            thumb.appendChild(el('div', 'player__ep-done', '\u2713'));
          } else if (opt.progress && opt.progress > 0) {
            const bar = el('div', 'player__ep-progress');
            const fill = el('div');
            fill.style.width = (opt.progress * 100).toFixed(1) + '%';
            bar.appendChild(fill);
            thumb.appendChild(bar);
          }
          item.appendChild(thumb);
          item.appendChild(el('div', 'player__ep-name', opt.label));
          if (opt.sub) item.appendChild(el('div', 'player__ep-sub', opt.sub));
        } else {
          const cls = 'player__popup-item' + (opt.disabled ? ' is-disabled' : ' selector') + (opt.active ? ' is-active' : '');
          item = el('div', cls, opt.label);
          if (opt.sub) item.appendChild(el('span', 'player__popup-item__sub', opt.sub));
          if (opt.disabled) {
            list.appendChild(item);
            return;
          }
        }
        if (opt.active) lastItem = item; // open on what's playing
        on(item, 'hover:focus', function () {
          lastItem = item;
          if (strip) {
            slideTo(item);
            return;
          }
          // The sheet is overflow-y:auto but Navigator never scrolls: with 20+
          // voices (Rezka) the focused row was off-screen. Chromium 47 has the
          // boolean form only.
          try {
            item.scrollIntoView(false);
          } catch (e) {
            /* ignore */
          }
        });
        on(item, 'hover:enter', function () {
          closeMenu(false);
          opt.onSelect();
        });
        list.appendChild(item);
      })(options[i]);
    }

    root.appendChild(box);
    popup = box;
    popupReturnMode = returnMode;
    // The panel must not auto-hide under an open menu (the popup is positioned
    // against the panel and was left floating alone).
    if (hideTimer) {
      window.clearTimeout(hideTimer);
      hideTimer = 0;
    }

    const calls: ControllerCalls = {
      toggle: function () {
        Controller.collectionSet(box);
        Controller.collectionFocus(lastItem || false, box);
      },
      back: function () {
        closeMenu(false);
      },
    };
    if (strip) {
      calls.left = function () {
        Controller.moveOr('left');
      };
      calls.right = function () {
        Controller.moveOr('right');
      };
    } else {
      calls.up = function () {
        Controller.moveOr('up');
      };
      calls.down = function () {
        Controller.moveOr('down');
      };
    }
    Controller.add('player_menu', calls);
    setMode('player_menu');
  }

  // "More" sheet: the rarely-touched toggles that used to be five icons.
  function openMoreMenu(): void {
    const opts: MenuOption[] = [];
    if (canPip) opts.push({ label: t('player.btn_pip'), active: false, onSelect: togglePip });
    opts.push({
      label: t('player.btn_mute'),
      sub: t(video.muted ? 'toggle.off' : 'toggle.on'),
      active: false,
      onSelect: toggleMute,
    });
    opts.push({
      label: t('player.btn_aspect'),
      sub: t(root.classList.contains('player--fill') ? 'player.aspect_fill' : 'player.aspect_fit'),
      active: false,
      onSelect: toggleAspect,
    });
    opts.push({
      label: t('settings.subs_size'),
      sub: t('subsize.' + getSubSize()),
      active: false,
      onSelect: openSubSizeMenu,
    });
    openMenu(t('player.more'), opts);
  }
  function openSubSizeMenu(): void {
    const sizes: SubSize[] = ['small', 'medium', 'large'];
    const opts: MenuOption[] = [];
    for (let i = 0; i < sizes.length; i++) {
      (function (sz: SubSize) {
        opts.push({
          label: t('subsize.' + sz),
          active: getSubSize() === sz,
          onSelect: function () {
            root.classList.remove('player--subs-' + getSubSize());
            setSubSize(sz);
            root.classList.add('player--subs-' + sz);
          },
        });
      })(sizes[i]);
    }
    openMenu(t('settings.subs_size'), opts);
  }

  let popupReturnMode = 'player_panel';
  function closeMenu(silent: boolean): void {
    if (popup && popup.parentNode) popup.parentNode.removeChild(popup);
    popup = null;
    if (!silent) {
      setMode(popupReturnMode);
      armHide();
    }
  }

  // Session memory of the user's picks. Every episode/voice switch used to reset
  // quality/audio/subtitles to defaults, so a binge meant re-picking the same
  // dub every 40 minutes. Recorded on explicit selection only; applied after each
  // new media is loaded. wantSub: undefined = never touched, null = chose "off".
  // Height (1080) rather than the label: labels differ between sources/voices
  // ("1080p" vs "1080p HEVC"), the number doesn't. 0 = untouched/auto.
  let wantQualityNum = 0;
  let wantAudio = '';
  let wantSub: string | null | undefined;
  let audioWantApplied = false;
  function applyWants(): void {
    if (wantQualityNum > 0 && media.streams.length > 1) {
      // Exact height, else the nearest lower playable one.
      let best = -1;
      let bestQ = 0;
      for (let i = 0; i < media.streams.length; i++) {
        const st = media.streams[i];
        if (!playable(st)) continue;
        const q = qualityOf(st);
        if (q === wantQualityNum) {
          best = i;
          break;
        }
        if (q < wantQualityNum && q > bestQ) {
          bestQ = q;
          best = i;
        }
      }
      if (best >= 0) streamIndex = best;
    }
    applyWantSub();
    audioWantApplied = false;
  }
  // Re-select the subtitle the user picked on the previous episode/voice. Label
  // first ("(Russian) Forced" ≠ "(Russian) Full" share a lang), lang as the
  // fallback when the new source names its tracks differently.
  function applyWantSub(): void {
    if (!wantSub) return;
    const subs = media.subtitles || [];
    for (let i = 0; i < subs.length; i++) {
      if ((subs[i].label || '') === wantSub) {
        applySubtitle(subs[i]);
        return;
      }
    }
    for (let i = 0; i < hlsSubs.length; i++) {
      if ((hlsSubs[i].name || '') === wantSub) {
        selectHlsSub(hlsSubs[i], i);
        return;
      }
    }
    for (let i = 0; i < subs.length; i++) {
      if (subs[i].lang && subs[i].lang === wantSub) {
        applySubtitle(subs[i]);
        return;
      }
    }
    for (let i = 0; i < hlsSubs.length; i++) {
      if (hlsSubs[i].lang && hlsSubs[i].lang === wantSub) {
        selectHlsSub(hlsSubs[i], i);
        return;
      }
    }
  }
  function applyWantAudio(): void {
    if (!wantAudio || audioWantApplied || !hls) return;
    const trks = hlsAudioList();
    for (let i = 0; i < trks.length; i++) {
      const tr = trks[i];
      if ((tr.lang || tr.name || tr.label || '') === wantAudio) {
        audioWantApplied = true;
        hls.audioTrack = tr.id != null ? tr.id : i;
        return;
      }
    }
  }

  function openQualityMenu(): void {
    const opts: MenuOption[] = [];
    if (hlsLevelsUsable() && hls) {
      const inst = hls;
      const auto = inst.autoLevelEnabled !== false && wantQualityNum === 0;
      opts.push({
        label: t('player.auto'),
        active: auto,
        onSelect: function () {
          wantQualityNum = 0;
          applyWantLevel();
        },
      });
      // Levels by height, best first; duplicates (same height, other bitrate)
      // collapse to the first.
      const order: number[] = [];
      for (let i = 0; i < inst.levels.length; i++) order.push(i);
      order.sort(function (a, b) {
        return hlsLevelHeight(b) - hlsLevelHeight(a);
      });
      const seen: { [h: number]: boolean } = {};
      for (let k = 0; k < order.length; k++) {
        (function (idx: number) {
          const h = hlsLevelHeight(idx);
          if (!h || seen[h]) return;
          seen[h] = true;
          opts.push({
            label: h + 'p',
            active: !auto && inst.currentLevel === idx,
            onSelect: function () {
              wantQualityNum = h;
              applyWantLevel();
            },
          });
        })(order[k]);
      }
      openMenu(t('player.quality'), opts);
      return;
    }
    for (let i = 0; i < media.streams.length; i++) {
      (function (idx: number) {
        const s = media.streams[idx];
        const ok = playable(s);
        opts.push({
          label: (s.label || s.quality || 'stream ' + (idx + 1)) + (ok ? '' : ' (' + t('player.unsupported') + ')'),
          active: idx === streamIndex,
          disabled: !ok,
          onSelect: function () {
            if (idx === streamIndex) return;
            streamIndex = idx;
            wantQualityNum = qualityOf(s);
            updateQualityButton();
            reload(true);
          },
        });
      })(i);
    }
    openMenu(t('player.quality'), opts);
  }

  // One "Audio" menu for both kinds of choice: tracks inside the current
  // stream (instant switch) and dubs the source offers (re-resolve). Two
  // separate buttons meant two icons for what the viewer thinks of as one thing.
  function openVoiceMenu(): void {
    const inStream = audioTrackOptions();
    const fromSource: MenuOption[] = [];
    if (ctx.onVoice) {
      for (let i = 0; i < media.voices.length; i++) {
        (function (v: Voice) {
          fromSource.push({
            label: v.name,
            active: v.id === media.currentVoice,
            onSelect: function () {
              if (v.id === media.currentVoice) return;
              requestVoice(v.id, v.name);
            },
          });
        })(media.voices[i]);
      }
    }
    let opts: MenuOption[] = [];
    if (inStream.length && fromSource.length) {
      opts.push({ label: t('player.in_stream'), header: true, active: false, onSelect: function () {} });
      opts = opts.concat(inStream);
      opts.push({ label: t('player.from_source'), header: true, active: false, onSelect: function () {} });
      opts = opts.concat(fromSource);
    } else {
      opts = inStream.length ? inStream : fromSource;
    }
    if (!opts.length) return;
    openMenu(t('player.voice'), opts);
  }

  function openSubsMenu(): void {
    const opts: MenuOption[] = [];
    const hlsCur = hls && typeof hls.subtitleTrack === 'number' ? hls.subtitleTrack : -1;
    opts.push({
      label: t('player.off'),
      active: !subTrack && hlsCur === -1,
      onSelect: function () {
        wantSub = null;
        applySubtitle(null);
      },
    });
    const subs = media.subtitles || [];
    for (let i = 0; i < subs.length; i++) {
      (function (sub: Subtitle) {
        opts.push({
          label: sub.label || sub.lang || 'sub',
          active: currentSub === sub || (!!currentSub && currentSub.url === sub.url),
          onSelect: function () {
            wantSub = sub.label || sub.lang || '';
            applySubtitle(sub);
          },
        });
      })(subs[i]);
    }
    for (let i = 0; i < hlsSubs.length; i++) {
      (function (tr: HlsSubTrack, idx: number) {
        const id = tr.id != null ? tr.id : idx;
        opts.push({
          label: tr.name || tr.lang || 'sub ' + (idx + 1),
          active: hlsCur === id,
          onSelect: function () {
            wantSub = tr.name || tr.lang || '';
            selectHlsSub(tr, idx);
          },
        });
      })(hlsSubs[i], i);
    }
    openMenu(t('player.subs'), opts);
  }

  // ---- voice re-resolve ----
  function requestVoice(voiceId: string, name?: string): void {
    if (!ctx.onVoice) return;
    setBuffering(true);
    if (name) toast(t('player.switching_voice', { name: name })); // a cold source takes up to ~25s
    const keepTime = absTime();
    ctx.onVoice(voiceId, function (newMedia) {
      if (destroyed) return;
      setBuffering(false);
      if (!newMedia || !newMedia.streams || !newMedia.streams.length) {
        toast(t('sources.resolve_failed'));
        return;
      }
      media = newMedia;
      media.currentVoice = voiceId;
      applySubtitle(null); // don't carry the old voice's subtitle track over
      remuxAudioIndex = 0; // new stream: default audio track, not the old index (AUD-1)
      ctxAudio = [];
      recoverAttempts = 0; // fresh stream gets a full recovery budget (LIFE-3)
      streamIndex = pickQualityIndex(media.streams);
      applyWants(); // the user's quality/subtitle picks carry over
      refreshButtons();
      lastFocused = btnVoice;
      resumeAt = keepTime;
      reload(true);
      setMode('player_panel');
    });
  }

  // ---- seek / rewind (Lampa video.js rewind() model) ----
  // !scrubbing means "not scrubbing". Left/right accelerate a virtual
  // target that rides the timeline; the real currentTime is applied ~1s after
  // the last press (rewindApply), then playback resumes.
  // scrubbing is the state flag (a virtual position of exactly 0 is a valid
  // target — the old `rewindPosition === 0` sentinel broke scrubbing to the
  // start). rewindPosition = the virtual target while scrubbing.
  let scrubbing = false;
  let rewindPosition = 0;
  let scrubPresses = 0;
  let scrubStart = 0;
  let wasPausedBeforeScrub = false;
  let rewindTimer = 0;
  let seekWarned = false;
  // A seek target past the currently-muxed/seekable edge (torrent HLS grows as
  // it downloads). We stash it and apply it once onTimeUpdate sees the edge
  // reach it (A1 prefetch fills the region), instead of letting the browser
  // silently clamp the seek back to the edge ("+10min lands +2min").
  let pendingSeek = 0;
  // True only for a torrent /remux stream whose HLS playlist ffmpeg is still
  // growing — the ONLY case where seekEnd() genuinely trails the target and a
  // seek must be stashed. Online VOD (/relay) and progressive (/stream) are
  // fully seekable on demand (hls.js/Range fetch the target), so clamping their
  // seeks to the buffered edge was the "resume/+10min lands at ~2min" bug.
  let growingSource = false;
  // Last non-zero play position — a stall/error resumes here when the element
  // has zeroed currentTime.
  let lastGoodTime = 0;

  // Highest time the video element will actually let us seek to right now:
  // min(nominal duration, end of the seekable range). On a growing torrent HLS
  // the seekable end trails the nominal runtime.
  function seekEnd(): number {
    const dur = videoDuration();
    try {
      const s = video.seekable;
      if (s && s.length) return Math.min(dur, s.end(s.length - 1));
    } catch (e) {
      /* seekable may throw before metadata */
    }
    return dur;
  }

  // Set currentTime clamped to the seekable edge; stash any overshoot in
  // pendingSeek so onTimeUpdate re-applies it once a growing torrent HLS muxes
  // that far. Every seek (scrub/skip/resume/continue) must go through this.
  // pos is ABSOLUTE source time.
  function seekClamped(pos: number): void {
    // Any explicit seek supersedes a pending resume verification — otherwise
    // the verifier would drag a viewer who scrubbed right after "Continue"
    // back to the saved spot.
    verifySeekAt = 0;
    // Before the muxed window (the job started at timeBase): only a new job
    // from that spot can play it — restart the engine there.
    if (timeBase > 0 && pos < timeBase - 1) {
      diag('player:seek-restart', { target: Math.round(pos), base: Math.round(timeBase) });
      resumeAt = pos;
      pendingResume = false;
      reload(false);
      return;
    }
    let rel = pos - timeBase;
    if (rel < 0) rel = 0;
    // Only a growing torrent remux needs clamp-and-stash (its seekable edge
    // trails the muxed target). VOD/progressive seek directly — hls.js/Range
    // fetch an unbuffered target, so clamping to the buffered edge would strand
    // the seek at ~2min and never re-apply.
    if (growingSource) {
      const end = seekEnd();
      if (rel > end) {
        diag('player:seek-stash', { target: Math.round(pos), edge: Math.round(end + timeBase) });
        pendingSeek = rel;
        rel = end;
      } else {
        pendingSeek = 0;
      }
    } else {
      pendingSeek = 0;
    }
    try {
      video.currentTime = rel;
    } catch (e) {
      /* not ready; loadedmetadata retries */
    }
  }

  function rewind(forward: boolean): void {
    const dur = videoDuration();
    if (!dur || !isFinite(dur)) {
      // First seconds of a remux/transcode, or a live-looking manifest: say so
      // once instead of silently eating the key.
      if (!seekWarned) {
        seekWarned = true;
        toast(t('player.seek_unavailable'));
      }
      return;
    }

    if (!scrubbing) {
      scrubbing = true;
      scrubPresses = 0;
      scrubStart = absTime();
      rewindPosition = scrubStart;
      wasPausedBeforeScrub = video.paused;
    }
    scrubPresses++;
    const step = scrubStep(scrubPresses);
    rewindPosition += forward ? step : -step;
    if (rewindPosition < 0) rewindPosition = 0;
    // Stop 2s short of the end, like the skip-to-end button: landing exactly on
    // `dur` fires `ended` → autoplay-next and marks THIS episode finished — a held
    // → key must never silently switch episodes.
    if (rewindPosition > dur - 2) rewindPosition = Math.max(0, dur - 2);

    // Pause while scrubbing (Lampa pauses in rewindStart); don't flash the
    // big play glyph — it only shows for a genuine user pause.
    try {
      video.pause();
    } catch (e) {
      /* ignore */
    }
    refreshCenter();

    paintTimeline(rewindPosition, dur);
    timeCur.textContent = fmtTime(rewindPosition);
    const delta = rewindPosition - scrubStart;
    showBubble(rewindPosition, dur, delta);
    osdDelta.textContent = (forward ? '\u25B6\u25B6 ' : '\u25C0\u25C0 ') + (delta >= 0 ? '+' : '\u2212') + fmtTime(Math.abs(delta));
    osdTime.textContent = fmtTime(rewindPosition);
    osdEl.classList.remove('hide');
    root.classList.add('player--rewind');
    showPanel();

    if (rewindTimer) window.clearTimeout(rewindTimer);
    rewindTimer = window.setTimeout(rewindApply, SEEK_APPLY_MS);
  }

  function rewindApply(): void {
    const target = rewindPosition;
    const resume = !wasPausedBeforeScrub;
    scrubbing = false;
    rewindPosition = 0;
    scrubPresses = 0;
    root.classList.remove('player--rewind');
    osdEl.classList.add('hide');
    hideBubble();
    seekClamped(target); // clamp+stash only for a growing remux; VOD seeks direct
    // Scrubbing pauses; only resume if the viewer wasn't paused to begin with.
    if (resume) tryPlay();
    else refreshCenter();
    emitProgress(); // a jump followed by exit within 10s used to be lost
    armHide();
  }

  function togglePlay(): void {
    if (scrubbing) return;
    if (video.paused) {
      tryPlay();
    } else {
      video.pause();
    }
    refreshCenter();
  }

  // Immediate seek (skip-to-start / skip-to-end buttons). Unlike rewind() this
  // is a direct jump, not the accelerating virtual scrub.
  function seekTo(pos: number): void {
    const dur = videoDuration();
    if (!dur || !isFinite(dur)) return;
    if (pos < 0) pos = 0;
    if (pos > dur) pos = dur;
    seekClamped(pos); // growing remux stashes; VOD seeks direct (was clamped for all)
    paintTimeline(pos, dur);
    timeCur.textContent = fmtTime(pos);
    emitProgress();
  }

  // ---- audio tracks ----
  // Two transports carry in-stream audio tracks: hls.js (demuxed audio parsed
  // from the manifest) and native HLS on Tizen/Safari (an AudioTrackList on
  // the <video>). Mirrors Lampa video.js loaded(): prefer hls when attached,
  // else fall back to native.
  //
  // hls.js gotcha: the `hls.audioTracks` getter is driven by the async
  // AudioTrackController and can stay empty long after the manifest is parsed
  // (observed with collaps' primary+failover audio groups — the getter never
  // populated, so a getter-only button never showed). The reliable signal is
  // the `audioTracks` array delivered *with* the MANIFEST_PARSED /
  // AUDIO_TRACKS_UPDATED events, which we capture below. Switching still goes
  // through `hls.audioTrack = track.id` (Lampa does the same); each parsed
  // track's `id` is its index in that array.
  interface HlsAudioTrack {
    id?: number;
    name?: string;
    lang?: string;
    label?: string;
  }
  let capturedAudio: HlsAudioTrack[] = [];

  function captureHlsAudio(data: unknown): void {
    const d = data as { audioTracks?: HlsAudioTrack[] };
    let src: HlsAudioTrack[] = (d && d.audioTracks) || [];
    if (!src.length && hls && hls.audioTracks) src = hls.audioTracks;
    // Dedup by name+lang: a stream often repeats the same renditions across a
    // primary and a "failover" audio group; show each label once, keeping the
    // first id (the primary group).
    const seen: { [k: string]: boolean } = {};
    const out: HlsAudioTrack[] = [];
    for (let i = 0; i < src.length; i++) {
      const tr = src[i];
      const key = (tr.name || '') + '|' + (tr.lang || '') + '|' + (tr.label || '');
      if (seen[key]) continue;
      seen[key] = true;
      out.push(tr);
    }
    capturedAudio = out;
    updateAudioTracks();
    applyWantAudio(); // re-select the dub the user picked on the previous episode
  }

  // hls audio list preferring whichever source has more entries (the live
  // getter once it finally populates, else the captured/deduped manifest list).
  function hlsAudioList(): HlsAudioTrack[] {
    const live = hls && hls.audioTracks ? hls.audioTracks : [];
    return live.length > capturedAudio.length ? live : capturedAudio;
  }

  interface NativeAudioTrack {
    id?: string;
    label?: string;
    language?: string;
    kind?: string;
    enabled: boolean;
  }
  interface NativeAudioTrackList {
    length: number;
    [i: number]: NativeAudioTrack;
  }
  function nativeAudioTracks(): NativeAudioTrackList | null {
    const list = (video as unknown as { audioTracks?: NativeAudioTrackList }).audioTracks;
    return list && typeof list.length === 'number' ? list : null;
  }
  // The re-mux-driven track list (torrent ctx tracks take precedence over the
  // per-stream header list; both switch by re-muxing a different audio index).
  function switchAudioList(): RemuxAudioTrack[] {
    return ctxAudio.length ? ctxAudio : remuxAudio;
  }
  function audioTrackCount(): number {
    // A remux stream's own manifest has one track by construction; the real
    // choice lives in the source list the server reported.
    if (switchAudioList().length) return switchAudioList().length;
    if (hls) return hlsAudioList().length;
    const nat = nativeAudioTracks();
    return nat ? nat.length : 0;
  }

  function remuxTrackLabel(tr: RemuxAudioTrack, i: number): string {
    return tr.title || tr.lang || 'audio ' + (i + 1);
  }
  function audioLangName(lang: string | undefined): string {
    switch ((lang || '').toLowerCase()) {
      case 'rus':
      case 'ru':
        return 'Русский';
      case 'ukr':
      case 'uk':
        return 'Українська';
      case 'eng':
      case 'en':
        return 'English';
      default:
        return lang ? lang.toUpperCase() : '';
    }
  }
  // ffmpeg's multi-audio HLS renditions get a generic NAME ("audio_1"); prefer the
  // track's language (rus→Русский, …) so the menu is readable, not "audio_1/2/3".
  function audioTrackLabel(name: string | undefined, lang: string | undefined, i: number): string {
    const generic = !name || /^audio[_ ]?\d+$/i.test(name);
    if (generic) {
      const l = audioLangName(lang);
      if (l) return l;
    }
    return name || audioLangName(lang) || 'audio ' + (i + 1);
  }
  function updateAudioTracks(): void {
    const any = audioTrackCount() > 1 || !!(ctx.onVoice && media.voices && media.voices.length > 0);
    btnVoice.classList.toggle('hide', !any);
  }
  // In-stream audio choices as menu options (empty when there's nothing to pick).
  function audioTrackOptions(): MenuOption[] {
    const opts: MenuOption[] = [];
    // Remux: switching track means re-muxing with a different -map 0:a:<n>,
    // so we swap the audio index and reload in place (position kept). Same path
    // for torrent ctx tracks and the per-stream header list.
    const remuxList = switchAudioList();
    if (remuxList.length > 1) {
      for (let i = 0; i < remuxList.length; i++) {
        (function (idx: number) {
          const tr = remuxList[idx];
          opts.push({
            label: remuxTrackLabel(tr, idx),
            active: tr.index === remuxAudioIndex,
            onSelect: function () {
              if (tr.index === remuxAudioIndex) return;
              remuxAudioIndex = tr.index;
              reload(true);
            },
          });
        })(i);
      }
      return opts;
    }
    if (hls) {
      const trks = hlsAudioList();
      if (trks.length <= 1) return opts;
      const cur = typeof hls.audioTrack === 'number' ? hls.audioTrack : -1;
      for (let i = 0; i < trks.length; i++) {
        (function (idx: number) {
          const tr = trks[idx];
          const trackId = tr.id != null ? tr.id : idx;
          const named = media.audioNames && media.audioNames[idx];
          const name = named || audioTrackLabel(tr.name || tr.label, tr.lang, idx);
          opts.push({
            label: name,
            active: trackId === cur,
            onSelect: function () {
              wantAudio = tr.lang || tr.name || tr.label || '';
              if (hls) hls.audioTrack = trackId;
            },
          });
        })(i);
      }
      return opts;
    }
    const nat = nativeAudioTracks();
    if (!nat || nat.length <= 1) return opts;
    for (let i = 0; i < nat.length; i++) {
      (function (idx: number) {
        const tr = nat[idx];
        const name = tr.label || tr.language || 'audio ' + (idx + 1);
        opts.push({
          label: name,
          active: !!tr.enabled,
          onSelect: function () {
            const list = nativeAudioTracks();
            if (!list) return;
            for (let j = 0; j < list.length; j++) list[j].enabled = j === idx;
          },
        });
      })(i);
    }
    return opts;
  }

  // ---- picture-in-picture ----
  function togglePip(): void {
    const v = video as unknown as {
      requestPictureInPicture?: () => Promise<unknown>;
    };
    const doc = document as unknown as {
      pictureInPictureElement?: Element | null;
      exitPictureInPicture?: () => Promise<unknown>;
    };
    try {
      if (doc.pictureInPictureElement && doc.exitPictureInPicture) {
        doc.exitPictureInPicture();
      } else if (v.requestPictureInPicture) {
        v.requestPictureInPicture();
      }
    } catch (e) {
      /* ignore */
    }
    showPanel();
  }

  // ---- mute toggle (volume button) ----
  function toggleMute(): void {
    video.muted = !video.muted;
    toast(t('player.btn_mute') + ': ' + t(video.muted ? 'toggle.off' : 'toggle.on'));
    showPanel();
  }

  // ---- framing: fit (letterbox) ↔ fill (crop) ----
  function toggleAspect(): void {
    const fill = !root.classList.contains('player--fill');
    root.classList.toggle('player--fill', fill);
    toast(t(fill ? 'player.aspect_fill' : 'player.aspect_fit'));
    showPanel();
  }


  // ---- playback speed ----
  const SPEEDS = [0.5, 0.75, 1, 1.25, 1.5, 1.75, 2];
  function openSettingsMenu(): void {
    const opts: MenuOption[] = [];
    for (let i = 0; i < SPEEDS.length; i++) {
      (function (rate: number) {
        opts.push({
          label: rate === 1 ? t('player.speed_normal') : rate + '×',
          active: Math.abs(getPlayerSpeed() - rate) < 0.001,
          onSelect: function () {
            setPlayerSpeed(rate); // remembered across episodes/titles
            applyPlaybackSpeed();
          },
        });
      })(SPEEDS[i]);
    }
    openMenu(t('player.speed'), opts);
  }

  // Re-apply the remembered playback rate. The <video> resets rate to 1 on
  // every src/load, so this runs after each engine (re)start as well as from
  // the speed menu. Pill + info-bar badge follow the rate the element actually
  // took (a native HLS pipeline may silently keep 1×).
  function applyPlaybackSpeed(): void {
    const want = getPlayerSpeed();
    try {
      video.playbackRate = want;
    } catch (e) {
      /* some webviews reject rates they don't support — keep 1x */
    }
    const got = video.playbackRate || 1;
    if (Math.abs(got - want) > 0.001 && Math.abs(want - 1) > 0.001) toast(t('player.speed_unavailable'));
    setLabel(btnSpeed, speedLabel(got));
    btnSpeed.classList.toggle('is-normal', Math.abs(got - 1) < 0.001);
    valSpeedSpan.textContent = speedLabel(got);
    valSpeed.classList.toggle('hide', Math.abs(got - 1) < 0.001);
  }

  // Guards the end-of-media path so it runs once per loaded stream: both the
  // native 'ended' event and the near-duration fallback below can reach it.
  let endedFired = false;
  // Authoritative total once we learn the true length (exact segment sum from
  // the HLS level, native video.duration, or the X-Remux-Duration header) —
  // beats the TMDB runtime hint in ctx.durationHint, which is only an estimate
  // and made the bar creep / autoplay-next fire early.
  let authDuration = 0;

  // Real total: authoritative when known, else remux hint, else the element's.
  function videoDuration(): number {
    return authDuration > 0 ? authDuration : remuxDuration > 0 ? remuxDuration : video.duration || 0;
  }

  // ---- timeline paint ----
  let lastProgressW = '';
  function paintTimeline(cur: number, dur: number): void {
    const pct = dur > 0 ? (cur / dur) * 100 : 0;
    const w = pct.toFixed(3) + '%';
    if (w === lastProgressW) return;
    lastProgressW = w;
    progress.style.width = w;
  }
  function paintBuffer(pct: number): void {
    buffer.style.width = pct.toFixed(2) + '%';
  }
  // Target time plus the signed jump ("1:02:30  +2:30") so a held key reads as
  // distance, not as a moving number. Clamped off the edges (translateX(-50%)
  // pushed it past the timeline at 0%/100%).
  function showBubble(cur: number, dur: number, delta?: number): void {
    let pct = dur > 0 ? (cur / dur) * 100 : 0;
    if (pct < 3) pct = 3;
    if (pct > 97) pct = 97;
    bubble.style.left = pct.toFixed(3) + '%';
    let text = fmtTime(cur);
    if (delta && Math.abs(delta) >= 1) text += '  ' + (delta > 0 ? '+' : '\u2212') + fmtTime(Math.abs(delta));
    bubble.textContent = text;
    bubble.classList.remove('hide');
  }
  function hideBubble(): void {
    bubble.classList.add('hide');
  }

  // ---- center glyph / spinner (Lampa player--loading + player-video__paused) ----
  let buffering = false;
  function setBuffering(on_: boolean): void {
    buffering = on_;
    root.classList.toggle('player--loading', on_);
    refreshCenter();
  }
  // Paused wins over buffering: a pause during a stall used to show only the
  // spinner, so OK (= play) read as "it's hung, poke it".
  function refreshCenter(): void {
    const showPause = video.paused && !scrubbing && !errorBox;
    centerPlay.classList.toggle('hide', !showPause);
    videoLayer.classList.toggle('video--load', buffering && !showPause);
    panel.classList.toggle('panel--paused', video.paused);
  }

  // ---- engine load ----
  let resumeAt = 0;

  // Resume-from-timecode: only offer when the saved position is a meaningful
  // mid-watch spot (past the intro, before the credits). armResume runs both at
  // mount and after each episode switch (goNext/goPrev), so a partially-watched
  // next episode resumes too — not just the one the player opened on.
  let pendingResume = false;
  let resumePos = 0;
  function armResume(rc: { position_sec: number; duration_sec: number } | null | undefined): void {
    pendingResume = false;
    resumePos = 0;
    if (rc && isResumable(rc.position_sec, rc.duration_sec)) {
      pendingResume = true;
      resumePos = rc.position_sec;
    }
  }
  armResume(ctx.resume);

  function currentUrl(): string {
    const s = media.streams[streamIndex];
    return s ? mediaUrl(s.url) : '';
  }

  function teardownEngine(): void {
    capturedAudio = [];
    hlsSubs = [];
    if (hls) {
      try {
        hls.destroy();
      } catch (e) {
        /* ignore */
      }
      hls = null;
    }
    video.removeAttribute('src');
    try {
      video.load();
    } catch (e) {
      /* ignore */
    }
  }

  function reload(keepPosition: boolean): void {
    if (keepPosition) resumeAt = absTime() || resumeAt;
    // Cancel any pending scrub — otherwise a rewindApply armed just before the
    // reload fires against the fresh engine and jumps it to the stale target.
    if (rewindTimer) {
      window.clearTimeout(rewindTimer);
      rewindTimer = 0;
    }
    scrubbing = false;
    rewindPosition = 0;
    scrubPresses = 0;
    seekWarned = false;
    pendingSeek = 0;
    // Cancel a pending native-recovery reseek too — else it fires against the
    // fresh stream and jumps it to the previous position (LIFE-1).
    if (recoverTimer) {
      window.clearTimeout(recoverTimer);
      recoverTimer = 0;
    }
    root.classList.remove('player--rewind');
    osdEl.classList.add('hide');
    hideBubble();
    steadySince = 0;
    lastTickTime = -1;
    stallTicks = 0;
    stallWarned = false;
    teardownEngine();
    startEngine();
  }

  // A seek issued at loadedmetadata is not guaranteed to stick: Tizen's native
  // pipeline sometimes drops it before canplay. Remember the target and check
  // on the first real ticks; re-issue up to twice. Not for a growing remux —
  // there the stash (pendingSeek) owns the target.
  let verifySeekAt = 0;
  let verifySeekTries = 0;
  function seekToResume(): void {
    if (resumeAt > 0) {
      // The mux already starts at the resume spot (start=N): nothing to seek.
      if (Math.abs(resumeAt - timeBase) <= 1.5) {
        diag('player:resume-at-base', { base: Math.round(timeBase) });
        resumeAt = 0;
        return;
      }
      seekClamped(resumeAt); // stashes overshoot past the muxed edge in pendingSeek
      if (!growingSource) {
        verifySeekAt = resumeAt;
        verifySeekTries = 0;
      }
      resumeAt = 0;
    }
  }
  function verifyResumeSeek(cur: number): void {
    if (!verifySeekAt || cur <= 0.2) return;
    if (Math.abs(cur - verifySeekAt) <= 3) {
      diag('player:resume-ok', { cur: Math.round(cur), target: Math.round(verifySeekAt), tries: verifySeekTries });
      verifySeekAt = 0;
      return;
    }
    if (verifySeekTries >= 2) {
      diag('player:resume-failed', { cur: Math.round(cur), target: Math.round(verifySeekAt), seekable: Math.round(seekEnd()), pending: Math.round(pendingSeek) });
      verifySeekAt = 0;
      return;
    }
    verifySeekTries++;
    diag('player:resume-retry', { cur: Math.round(cur), target: Math.round(verifySeekAt), tries: verifySeekTries });
    try {
      video.currentTime = Math.max(0, verifySeekAt - timeBase);
    } catch (e) {
      /* ignore */
    }
  }

  // prepareStream turns a raw stream URL into one the engine can load
  // directly. Pass-through for /relay and progressive mp4. A /remux URL is
  // NOT a playlist — it creates/looks-up an ffmpeg job and returns JSON
  // {job_id, playlist_url}; we resolve that here and poll the playlist until
  // ffmpeg has written the first segments (HTTP 202 {progress} → 200 m3u8),
  // then hand the ready playlist to hls.js. Without this the engine feeds the
  // JSON descriptor straight into hls.js and hangs "loading" forever.
  function prepareStream(rawUrl: string, my: number): Promise<string | null> {
    // Seed from the TMDB runtime hint (torrent remux/transcode, whose growing
    // playlist has no real total); the /remux poll below overrides it from the
    // X-Remux-Duration header when that path is used.
    remuxDuration = ctx.durationHint && ctx.durationHint > 0 ? ctx.durationHint : 0;
    authDuration = 0; // re-learn the exact total for this stream (LEVEL_UPDATED / header / native)
    pendingSeek = 0; // a fresh stream invalidates any stashed overshoot
    // Growing sources: an explicit /remux job, or a torrent /stream that the
    // server answers with an HLS playlist (mkv remux / HEVC transcode — the
    // client knows from media.type). Their playlists are muxed in real time.
    const isRemux = rawUrl.indexOf('/remux?') !== -1;
    const isTorrentHls = rawUrl.indexOf('/stream/') !== -1 && media.type === 'hls';
    growingSource = isRemux || isTorrentHls;
    // Resume deep into a growing source: ask ffmpeg to start muxing THERE
    // (start=N → -ss). Without it the player could only jump to the ~2 minutes
    // already muxed and the saved spot was reached minutes later, if ever.
    timeBase = 0;
    let startAt = pendingResume ? resumePos : resumeAt;
    if (growingSource && startAt > 30) {
      startAt = Math.floor(startAt);
      rawUrl = rawUrl.replace(/([?&])start=\d+/, '').replace(/[?&]$/, '');
      rawUrl += (rawUrl.indexOf('?') === -1 ? '?' : '&') + 'start=' + startAt;
      timeBase = startAt;
      diag('player:mux-from', { start: startAt, url: rawUrl.slice(0, 40) });
    }
    if (!isRemux) {
      remuxAudio = [];
      // Torrent /stream URL carries audio=N: rewrite it to the picked track so a
      // switch re-muxes that track inline (ctxAudio drives the menu; don't reset
      // ctxAudio here — it persists for the session).
      if (/[?&]audio=\d+/.test(rawUrl)) {
        return Promise.resolve(rawUrl.replace(/([?&])audio=\d+/, '$1audio=' + remuxAudioIndex));
      }
      return Promise.resolve(rawUrl);
    }
    remuxAudio = [];
    // Ask ffmpeg for the track the user picked (audio=0 by default). A different
    // index is a different dedup key server-side, i.e. its own mux job.
    const url = rawUrl.replace(/([?&])audio=\d+/, '$1audio=' + remuxAudioIndex);
    return fetch(url)
      .then(function (r) {
        return r.json();
      })
      .then(function (job: { playlist_url?: string }) {
        const pl = job && job.playlist_url;
        if (!pl) {
          return null;
        }
        return pollRemuxPlaylist(mediaUrl(pl), 60, my);
      })
      ['catch'](function () {
        return null;
      });
  }

  // pollRemuxPlaylist GETs the ffmpeg playlist until it returns 200 (ready,
  // first segments written) rather than 202 (still buffering). ~60 tries ×
  // 1.5s ≈ 90s ceiling — a demuxed source's first mux pass can take ~30s.
  // Info-bar text while a remux/transcode job is being prepared (cleared once
  // the playlist is ready).
  let prepText = '';
  function pollRemuxPlaylist(url: string, triesLeft: number, my: number): Promise<string | null> {
    return fetch(url).then(function (r) {
      if (r.status === 200) {
        if (my !== loadSeq) return null; // superseded by a newer load — don't clobber its state (LIFE-2)
        prepText = '';
        const dh = r.headers.get('X-Remux-Duration');
        if (dh) {
          const d = parseFloat(dh);
          if (isFinite(d) && d > 0) {
            remuxDuration = d;
            authDuration = d; // server-known exact total — authoritative
          }
        }
        const ah = r.headers.get('X-Remux-Audio');
        if (ah) {
          try {
            const list = JSON.parse(ah) as RemuxAudioTrack[];
            if (list && list.length) {
              remuxAudio = list;
              updateAudioTracks();
            }
          } catch (e) {
            /* malformed header — just leave the track menu hidden */
          }
        }
        return url;
      }
      // A definitive client error (4xx) means the job is gone — give up.
      if (r.status >= 400 && r.status < 500) return null;
      if (triesLeft <= 0) return null;
      // 202 (still buffering) or a transient 5xx/0 → wait and retry. The 202
      // body says whether ffmpeg is running or still QUEUED behind other
      // transcodes — up to 90s of bare spinner read as a hang, so surface it.
      if (r.status === 202) {
        r.json().then(
          function (b: { state?: string; queue_position?: number }) {
            if (my !== loadSeq || destroyed) return;
            prepText =
              b && b.queue_position && b.queue_position > 0
                ? t('player.queued', { n: String(b.queue_position) })
                : t('player.preparing');
            showPanel();
          },
          function () {
            /* body unreadable — keep the spinner */
          }
        );
      }
      return retryPoll(url, triesLeft, my);
    }, function () {
      // Network rejection mid-warmup is transient; retry within the budget
      // instead of dumping the user to the error screen on one blip.
      if (triesLeft <= 0) return null;
      return retryPoll(url, triesLeft, my);
    });
  }

  function retryPoll(url: string, triesLeft: number, my: number): Promise<string | null> {
    return new Promise<void>(function (res) {
      window.setTimeout(res, 1500);
    }).then(function () {
      if (destroyed) return null;
      return pollRemuxPlaylist(url, triesLeft - 1, my);
    });
  }

  function startEngine(): void {
    endedFired = false;
    const my = ++loadSeq;
    const rawUrl = currentUrl();
    if (!rawUrl) {
      showError(t('sources.resolve_failed'));
      return;
    }
    setBuffering(true);
    prepareStream(rawUrl, my).then(function (url) {
      if (destroyed || my !== loadSeq) return; // superseded by a newer load
      if (!url) {
        showError(t('sources.resolve_failed'));
        return;
      }
      startEngineWith(url, my);
    });
  }

  function startEngineWith(url: string, my: number): void {
    const isHls = media.type === 'hls';
    // Engine override (docs/frontend.md player_engine). 'auto' keeps the pickEngine
    // rule (native <video> HLS on Tizen/Safari, hls.js elsewhere); 'native'
    // forces the <video src> path; 'hlsjs' forces hls.js for HLS. Progressive
    // mp4 always plays natively regardless of the setting.
    const engine = getPlayerEngine();
    let nativeHls: boolean;
    if (engine === 'native') nativeHls = true;
    else if (engine === 'hlsjs') nativeHls = false;
    // 'auto': native <video> HLS only where it's genuinely native (Tizen).
    // NOT caps.hls_native — desktop Chrome reports canPlayType('…mpegurl')
    // = "maybe" but can't actually play HLS without MSE/hls.js, so trusting
    // it sends every HLS source down the <video src> path → MEDIA_ERR (code 4,
    // "формат не поддерживается"). hls.js (with a native fallback below when
    // MSE is missing) is correct for Chrome/webOS/Android.
    else nativeHls = preferNativeHls();

    diag('player:engine', { hls: isHls, native: nativeHls, engine: engine, url: url.slice(0, 40), growing: growingSource, startAt: pendingResume ? resumePos : resumeAt });
    if (!isHls || nativeHls) {
      video.src = url;
      try {
        video.load();
      } catch (e) {
        /* ignore */
      }
      tryPlay();
      return;
    }

    ensureHls().then(function (Hls: HlsCtor | null) {
      if (destroyed || my !== loadSeq) return; // superseded — don't attach a stale engine
      if (Hls && Hls.isSupported()) {
        // startPosition 0: belt-and-braces against a remux playlist still being
        // read as live (hls.js would then begin at the live edge — the episode
        // "opening near the end"). Server-side EXT-X-PLAYLIST-TYPE:EVENT is the
        // real fix; this guarantees the start point regardless.
        // Generous retry/timeout config: a torrent transcode's HLS playlist is
        // still being produced (near-realtime) when playback starts, so manifest/
        // level/fragment loads can transiently miss — hls.js should keep retrying,
        // not go fatal. Unknown keys are ignored by older hls.js builds.
        // Resume target known up front → hls.js loads THAT fragment first
        // instead of fragment 0 + a seek (a wasted segment and a flash of the
        // opening on every "Continue"). A growing remux keeps 0 (its seekable
        // edge may not have reached the target; seekClamped stashes it).
        const startAt = !growingSource ? (pendingResume ? resumePos : resumeAt) - timeBase : 0;
        const inst = new Hls({
          // enableWorker stays FALSE: moving demux to a Blob worker is a real
          // perf win on paper but unverifiable from here — a CSP/worker-src block
          // or an old-Tizen quirk would break playback, the one thing we can't
          // risk. Revisit with a device test.
          enableWorker: false,
          startPosition: startAt > 0 ? startAt : 0, // relative to the playlist (timeBase already subtracted)
          // Higher than default: a cold-swarm multi-audio torrent may not have
          // written master.m3u8 yet on first load (503) — ride out the warmup
          // instead of exhausting retries into an error.
          manifestLoadingMaxRetry: 10,
          manifestLoadingRetryDelay: 2000,
          levelLoadingMaxRetry: 8,
          levelLoadingRetryDelay: 1000,
          fragLoadingMaxRetry: 8,
          fragLoadingRetryDelay: 1000,
          // Never auto-show a DEFAULT=YES subtitle rendition; the menu opts in.
          subtitleDisplay: false,
        });
        hls = inst;
        inst.on(Hls.Events.ERROR, function (_evt: string, data: unknown) {
          const d = data as { fatal?: boolean; type?: string; details?: string };
          if (d && d.fatal) diag('player:hls-fatal', { type: d.type, details: d.details, attempts: recoverAttempts });
          if (!d || !d.fatal || my !== loadSeq) return;
          // Auto-recover instead of dumping to the error screen: a transcode that
          // runs at ~realtime buffer-stalls on any dip, and warmup network errors
          // are transient. Recover in place (media → recoverMediaError, network →
          // startLoad) up to MAX_RECOVER before surfacing the error.
          if (recoverAttempts < MAX_RECOVER) {
            recoverAttempts++;
            setBuffering(true);
            const recover = inst as unknown as { startLoad?: () => void; recoverMediaError?: () => void };
            window.setTimeout(function () {
              if (destroyed || my !== loadSeq || hls !== inst) return;
              try {
                if (d.type === 'mediaError' && recover.recoverMediaError) recover.recoverMediaError();
                else if (recover.startLoad && recoverAttempts <= 2) recover.startLoad();
                else reload(true);
              } catch (e) {
                reload(true);
              }
            }, 1200);
            return;
          }
          showError(t('player.error_network'));
        });
        // Capture the audio-track list straight off the manifest events (the
        // reliable source — see captureHlsAudio) and reveal the button. The 1s
        // stats poll is a fallback for the live getter finally populating.
        if (Hls.Events.MANIFEST_PARSED)
          inst.on(Hls.Events.MANIFEST_PARSED, function (_e: string, data: unknown) {
            captureHlsAudio(data);
            captureHlsSubs(data);
            applyWantLevel(); // fixed height from the previous episode, or leave ABR on
          });
        if (Hls.Events.LEVEL_SWITCHED) inst.on(Hls.Events.LEVEL_SWITCHED, updateQualityButton);
        // Once ffmpeg writes ENDLIST the level is VOD and its totalduration is
        // the exact segment sum — adopt it as authoritative over the TMDB hint.
        if (Hls.Events.LEVEL_UPDATED)
          inst.on(Hls.Events.LEVEL_UPDATED, function (_e: string, data: unknown) {
            const d = data as { details?: { live?: boolean; totalduration?: number } };
            if (d && d.details && !d.details.live && d.details.totalduration && d.details.totalduration > 0) {
              authDuration = d.details.totalduration + timeBase; // playlist covers [timeBase, end]
            }
          });
        if (Hls.Events.AUDIO_TRACKS_UPDATED)
          inst.on(Hls.Events.AUDIO_TRACKS_UPDATED, function (_e: string, data: unknown) {
            captureHlsAudio(data);
          });
        if (Hls.Events.AUDIO_TRACK_SWITCHED) inst.on(Hls.Events.AUDIO_TRACK_SWITCHED, updateAudioTracks);
        if (Hls.Events.SUBTITLE_TRACKS_UPDATED)
          inst.on(Hls.Events.SUBTITLE_TRACKS_UPDATED, function (_e: string, data: unknown) {
            captureHlsSubs(data);
          });
        inst.loadSource(url);
        inst.attachMedia(video);
        tryPlay();
      } else {
        video.src = url;
        try {
          video.load();
        } catch (e) {
          /* ignore */
        }
        tryPlay();
      }
    });
  }

  function tryPlay(): void {
    const p = video.play() as unknown as { then?: (a: () => void, b: () => void) => void };
    if (p && p.then) {
      p.then(
        function () {},
        function () {
          // Autoplay blocked (desktop) — user hits OK to start.
          refreshCenter();
        }
      );
    }
  }

  // ---- error overlay ----
  function showError(message: string): void {
    setBuffering(false);
    // Stop the engine: hls.js kept retrying fragments (network + CPU + log
    // noise) behind the overlay. Retry/reload rebuild it from scratch anyway.
    teardownEngine();
    // Kill any pending native-recovery reseek — it must not fire behind the
    // error overlay (LIFE-1).
    if (recoverTimer) {
      window.clearTimeout(recoverTimer);
      recoverTimer = 0;
    }
    if (errorBox && errorBox.parentNode) errorBox.parentNode.removeChild(errorBox);

    const box = el('div', 'player__error');
    box.appendChild(el('div', 'player__error-text', message));
    const row = el('div', 'player__error-actions');

    const retry = el('div', 'button selector', t('action.retry'));
    on(retry, 'hover:enter', function () {
      if (errorBox && errorBox.parentNode) errorBox.parentNode.removeChild(errorBox);
      errorBox = null;
      recoverAttempts = 0; // give the auto-recovery budget back on a manual retry
      reload(false);
      setMode('player');
    });
    row.appendChild(retry);

    const change = el('div', 'button selector', t('player.change_source'));
    on(change, 'hover:enter', function () {
      exitToSources();
    });
    row.appendChild(change);

    box.appendChild(row);
    root.appendChild(box);
    errorBox = box;
    refreshCenter();

    Controller.add('player_error', {
      toggle: function () {
        Controller.collectionSet(box);
        Controller.collectionFocus(false, box);
      },
      left: function () {
        Controller.moveOr('left');
      },
      right: function () {
        Controller.moveOr('right');
      },
      back: function () {
        exitToSources();
      },
    });
    setMode('player_error');
  }

  // ---- exit confirm ----
  // ---- episode switch (tv: autoplay-on-ended, or prev/next buttons) ----
  // switchEpisode drives both directions: persist the current position first
  // (so the leaving episode remembers where we were — manual Next used to skip
  // this), then swap in the resolved episode and arm ITS own saved timecode.
  function switchEpisode(fn: ((done: (m: PlayerMedia | null, meta?: EpisodeMeta) => void) => void) | undefined): boolean {
    if (!fn) return false;
    emitProgress(); // save the outgoing episode's position before we leave it
    setBuffering(true);
    fn(function (newMedia, meta) {
      if (destroyed) return;
      if (!newMedia || !newMedia.streams || !newMedia.streams.length) {
        setBuffering(false);
        showPanel();
        refreshCenter();
        toast(t('online.episodes_empty')); // tell the user the next episode has no source
        return;
      }
      media = newMedia;
      applySubtitle(null); // don't carry the old episode's subtitle track over
      remuxAudioIndex = 0; // new episode: default audio track, not the old index (AUD-1)
      ctxAudio = [];
      recoverAttempts = 0; // fresh stream gets a full recovery budget (LIFE-3)
      streamIndex = pickQualityIndex(media.streams);
      applyWants(); // the user's quality/subtitle picks carry over
      resumeAt = 0;
      armResume(meta ? meta.resume : null); // resume THIS episode if partly watched
      if (meta && meta.title) titleEl.textContent = meta.title;
      if (meta && meta.subtitle != null) subBase = meta.subtitle;
      if (meta && meta.episode != null) curEpisode = meta.episode;
      refreshButtons();
      loadEpisodes(); // the season may have changed; the caption follows
      loadCtxAudio(); // a torrent pack's next file has its own track list
      lastFocused = playpause;
      reload(false);
      setMode('player');
    });
    return true;
  }
  function goNext(): boolean {
    return switchEpisode(ctx.onNext);
  }
  function goPrev(): boolean {
    return switchEpisode(ctx.onPrev);
  }

  // ---- exit ----
  function exitToSources(): void {
    flushTimecode();
    router.back();
  }

  // ---- Phase-3 timecode hooks (stubs) ----
  let progressTimer = 0;
  let clockTimer = 0;
  let statsTimer = 0;
  function emitProgress(): void {
    const pos = absTime();
    if (pos <= 0) return; // don't persist a 0 position over a real saved timecode
    // A growing remux without a known total would save the playlist's CURRENT
    // length as duration → "finished" at 11 of 12 muxed minutes, shelf entry
    // gone, resume point jumps to the next episode. Skip until we know.
    const known = authDuration > 0 ? authDuration : remuxDuration;
    if (growingSource && known <= 0) return;
    const dur = known > 0 ? known : videoDuration();
    if (ctx.onProgress) ctx.onProgress(pos, dur);
  }
  function flushTimecode(): void {
    emitProgress();
  }

  // ---- video events ----
  function onWaiting(): void {
    setBuffering(true);
    steadySince = 0;
  }
  function onPlayingLike(): void {
    setBuffering(false);
    // NOT recoverAttempts = 0 here: a source that plays 1s and dies again fires
    // canplay every cycle and would never exhaust MAX_RECOVER — the error screen
    // never came, just an eternal spinner. The budget resets in onTimeUpdate
    // after STEADY_MS of the clock actually advancing.
  }
  // Wall-clock start of the current uninterrupted stretch of advancing playback.
  let steadySince = 0;
  let lastTickTime = -1;
  const STEADY_MS = 5000;

  // Stall watchdog (1s tick from updateStats). hls.js non-fatal errors and a
  // `waiting` with no `error` gave a spinner with no timeout and no way out.
  // After STALL_WARN_S of buffering with a frozen clock: a toast; after
  // STALL_FAIL_S: the error screen, which has Retry and "other source".
  let stallTicks = 0;
  let stallWarned = false;
  let stallLastTime = -1;
  const STALL_WARN_S = 25;
  const STALL_FAIL_S = 45;
  function stallWatch(): void {
    const cur = video.currentTime || 0;
    const frozen = buffering && !video.paused && !scrubbing && cur === stallLastTime;
    stallLastTime = cur;
    if (!frozen || errorBox) {
      stallTicks = 0;
      stallWarned = false;
      return;
    }
    stallTicks++;
    if (stallTicks === STALL_WARN_S && !stallWarned) {
      stallWarned = true;
      diag('player:stall', { cur: Math.round(cur), buffered: Math.round(bufferAheadSec()) });
      toast(t('player.stalled'));
    } else if (stallTicks >= STALL_FAIL_S) {
      stallTicks = 0;
      showError(t('player.error_network'));
    }
  }
  function onBuffered(): void {
    if (!panelVisible) return; // invisible buffer bar — don't read layout
    const dur = videoDuration();
    if (dur <= 0) return;
    try {
      const b = video.buffered;
      const cur = video.currentTime || 0;
      for (let i = 0; i < b.length; i++) {
        if (b.start(i) <= cur && b.end(i) >= cur) {
          paintBuffer(Math.max(0, Math.min(100, ((b.end(i) + timeBase) / dur) * 100)));
          return;
        }
      }
    } catch (e) {
      /* buffered may throw on some webviews */
    }
  }
  // Apply a stashed forward-seek once the muxed edge (grown by the whole-file
  // prefetch) reaches the target. Called from BOTH onTimeUpdate AND the 1s
  // statsTimer: a seek clamped past the muxed edge stalls the element ('waiting'),
  // timeupdate then stops firing, so only the always-on statsTimer is left to
  // notice the edge has since grown and land the seek (RC3). Returns true when it
  // applied the seek.
  function applyPendingSeek(): boolean {
    if (scrubbing) return false;
    // Unsatisfiable stash (target past the REAL end — TMDB hint overshot the true
    // runtime): drop it once the mux edge hits the authoritative end (SEEK-3).
    if (pendingSeek && authDuration > 0 && seekEnd() + timeBase >= authDuration - 0.5 && pendingSeek > seekEnd()) {
      pendingSeek = 0;
    }
    if (pendingSeek && seekEnd() >= pendingSeek) {
      const target = pendingSeek;
      pendingSeek = 0;
      try {
        video.currentTime = target;
      } catch (e) {
        /* ignore */
      }
      return true;
    }
    return false;
  }
  function onTimeUpdate(): void {
    // During a scrub the pointer shows the virtual target, not live time.
    if (scrubbing) return;
    // Apply a stashed forward-seek if the muxed edge has grown to reach it.
    if (applyPendingSeek()) return;
    const dur = videoDuration();
    const cur = absTime();
    verifyResumeSeek(cur);
    if (cur > 0) lastGoodTime = cur;
    if (cur > lastTickTime) {
      if (!steadySince) steadySince = Date.now();
      else if (Date.now() - steadySince > STEADY_MS) recoverAttempts = 0;
    }
    lastTickTime = cur;
    renderCues();
    paintSkip(cur, dur);
    // End fallback: a remux playlist keeps growing while ffmpeg muxes, so
    // hls.js treats it as live and never fires 'ended'. We only trust the
    // authoritative total here (not the TMDB hint, which fired autoplay early).
    if (!endedFired && authDuration > 0 && cur >= authDuration - 1) {
      onEnded();
      return;
    }
    // Painting the timeline/stats while the panel is HIDDEN is pure waste: ~4Hz
    // of DOM writes + forced reflow (paintTimeline/onBuffered read layout) that
    // nobody can see — a real source of player jank on weak TV CPUs. showPanel()
    // repaints once on reveal, so nothing is stale.
    if (!panelVisible) return;
    setText(timeCur, fmtTime(cur));
    setText(timeDur, durText(cur, dur));
    paintSub(cur, dur);
    paintTimeline(cur, dur);
    onBuffered();
    updateStats();
  }

  // textContent always replaces the Text node (a layout-dirtying write) even
  // for an identical string; the total never changes and the position only
  // once a second while the tick runs at ~4Hz.
  function setText(node: HTMLElement, s: string): void {
    if (node.textContent !== s) node.textContent = s;
  }

  // ---- info-bar stats (resolution badge + channel/bitrate/buffer line) ----
  // Resolution: <video> intrinsic size. Channel throughput + level bitrate come
  // from hls.js (bandwidthEstimate / levels[currentLevel].bitrate); buffer is
  // seconds loaded ahead of the play head (video.buffered). Mirrors Lampa's
  // player/video.js hlsBitrate() line, adapted to hls.js public API.
  function bufferAheadSec(): number {
    const cur = video.currentTime || 0;
    try {
      const b = video.buffered;
      for (let i = 0; i < b.length; i++) {
        if (b.start(i) <= cur && b.end(i) >= cur) return Math.max(0, b.end(i) - cur);
      }
    } catch (e) {
      /* buffered may throw */
    }
    return 0;
  }
  function fmtBufferHuman(sec: number): string {
    if (sec >= 60) return Math.floor(sec / 60) + ' ' + t('player.min');
    return Math.round(sec) + ' ' + t('player.sec');
  }
  function updateStats(): void {
    applyPendingSeek(); // RC3: land a stashed seek even while timeupdate is frozen
    stallWatch();
    // Reveal the audio-track button once hls.js has parsed its tracks.
    updateAudioTracks();
    if (!panelVisible) return; // the stats line is invisible — skip its DOM work
    // Resolution badge.
    const w = video.videoWidth || 0;
    const h = video.videoHeight || 0;
    valSizeSpan.textContent = w && h ? w + 'x' + h : '';
    valSize.classList.toggle('hide', !(w && h));

    // Channel / bitrate / buffer — only meaningful with hls.js attached. While a
    // remux job is still preparing, the slot shows that instead.
    if (!hls) {
      if (prepText) {
        valStatSpan.textContent = prepText;
        valStat.classList.remove('hide');
      } else {
        valStat.classList.add('hide');
      }
      return;
    }
    const mbit = t('player.mbit');
    let bitrate = 0;
    if (hls.levels && typeof hls.currentLevel === 'number' && hls.levels[hls.currentLevel]) {
      bitrate = hls.levels[hls.currentLevel].bitrate || 0;
    }
    const channel = hls.bandwidthEstimate || 0;
    const dot = ' • ';
    let text = '';
    if (channel > 0) text += t('player.channel') + ' ' + (channel / 1000000).toFixed(2) + ' ' + mbit;
    if (bitrate > 0) {
      if (text) text += dot;
      text += t('player.bitrate') + ' ~' + (bitrate / 1000000).toFixed(2) + ' ' + mbit;
    }
    if (text) text += dot;
    text += t('player.buffer') + ' ' + fmtBufferHuman(bufferAheadSec());
    valStatSpan.textContent = text;
    valStat.classList.toggle('hide', !text);
  }

  // Wall clock (HH:MM) shown top-right of the info bar.
  function updateClock(): void {
    const d = new Date();
    clockSpan.textContent = pad2(d.getHours()) + ':' + pad2(d.getMinutes());
  }
  function onLoadedMeta(): void {
    diag('player:loadedmetadata', { duration: Math.round(video.duration || 0), auth: Math.round(authDuration), remux: Math.round(remuxDuration), pendingResume: pendingResume, resumePos: Math.round(resumePos), resumeAt: Math.round(resumeAt), base: Math.round(timeBase), cur: Math.round(video.currentTime || 0) });
    applyPlaybackSpeed(); // <video> resets rate on every load
    // Native HLS / progressive: the element's own duration is exact — adopt it
    // over the TMDB runtime hint (hls.js path uses LEVEL_UPDATED instead).
    if (media.type !== 'hls' && isFinite(video.duration) && video.duration > 0) {
      authDuration = video.duration;
    }
    // A spot within 5s of THIS stream's end (another cut/source is shorter than
    // the one the spot was saved on) would land on `ended` → autoplay-next.
    if (pendingResume) {
      const d = videoDuration();
      if (d > 0 && isFinite(d) && resumePos >= d - 5) pendingResume = false;
    }
    // First metadata after open: auto-resume to the saved position — no modal.
    // A toast is the only cue the seek happened (silent jump reads as a bug).
    if (pendingResume) {
      pendingResume = false;
      resumeAt = resumePos;
      toast(t('player.resumed_at', { time: fmtTime(resumePos) }));
      // Reveal the panel so "from the start" is one arrow away — but focus
      // play/pause, not that button: a reflexive OK to dismiss the toast used
      // to jump to 0:00. The panel auto-hides; doing nothing keeps playing.
      lastFocused = playpause;
      showPanel();
      setMode('player_panel');
    }
    seekToResume();
    onTimeUpdate();
  }
  function onPlayPause(): void {
    refreshCenter();
    if (!video.paused) armHide();
    else emitProgress(); // a pause is the most common moment before an exit
  }
  function onEnded(): void {
    if (endedFired) return;
    endedFired = true;
    diag('player:ended', { cur: Math.round(absTime()), dur: Math.round(videoDuration()) });
    emitProgress(); // persist the (near-complete) final position
    if (ctx.onEnded) ctx.onEnded();
    if (ctx.onNext) {
      showNextCountdown();
      return;
    }
    showEndCountdown();
  }

  // Movie finished: offer the way back to the title card (auto in 15s) instead
  // of a frozen last frame with a panel.
  function showEndCountdown(): void {
    if (nextBox) return;
    const END_S = 15;
    let left = END_S;
    const box = el('div', 'player__confirm');
    box.appendChild(el('div', 'player__confirm-text', t('player.finished')));
    const sub = el('div', 'player__confirm-sub', t('player.back_in', { n: String(left) }));
    box.appendChild(sub);
    const row = el('div', 'player__confirm-actions');
    const go = el('div', 'button selector', t('player.to_card'));
    const stayBtn = el('div', 'button selector', t('player.stay'));
    row.appendChild(go);
    row.appendChild(stayBtn);
    box.appendChild(row);
    root.appendChild(box);
    nextBox = box;
    function closeBox(): void {
      if (nextTimer) window.clearInterval(nextTimer);
      nextTimer = 0;
      if (nextBox && nextBox.parentNode) nextBox.parentNode.removeChild(nextBox);
      nextBox = null;
    }
    function stay(): void {
      closeBox();
      setMode('player');
      showPanel();
      refreshCenter();
    }
    on(go, 'hover:enter', function () {
      closeBox();
      exitToSources();
    });
    on(stayBtn, 'hover:enter', stay);
    nextTimer = window.setInterval(function () {
      left--;
      if (left <= 0) {
        closeBox();
        exitToSources();
        return;
      }
      sub.textContent = t('player.back_in', { n: String(left) });
    }, 1000);
    addMode('player_next', {
      toggle: function () {
        Controller.collectionSet(box);
        Controller.collectionFocus(go, box);
      },
      left: function () {
        Controller.moveOr('left');
      },
      right: function () {
        Controller.moveOr('right');
      },
      enter: function () {
        const f = Navigator.getFocusedElement();
        if (f) trigger(f, 'hover:enter');
      },
      back: stay,
    });
    setMode('player_next');
  }

  // Episodes auto-advanced since the viewer last touched the remote. After
  // three, the countdown stops advancing on its own ("still watching?") — a
  // binge that fell asleep shouldn't burn through a season and its timecodes.
  let autoAdvances = 0;
  const AUTO_ADVANCE_MAX = 3;

  // "Next episode in 10s" with Cancel focused: the jump used to be instant and
  // silent, so a viewer who meant to stop was already 10s into the next episode
  // (with its timecode being written). OK on Cancel = stay; the countdown
  // advances on its own otherwise.
  let nextTimer = 0;
  let nextBox: HTMLElement | null = null;
  function showNextCountdown(): void {
    if (nextBox) return;
    const NEXT_S = 10;
    let left = NEXT_S;
    const box = el('div', 'player__confirm');
    const askFirst = autoAdvances >= AUTO_ADVANCE_MAX;
    const text = el('div', 'player__confirm-text', askFirst ? t('player.still_watching') : t('player.next_in', { n: String(left) }));
    box.appendChild(text);
    const nx = nextEpisode();
    box.appendChild(el('div', 'player__confirm-sub', nx ? epLabel(nx) : ctx.onNext && episodes.length ? t('player.next_season') : ''));
    const row = el('div', 'player__confirm-actions');
    const now = el('div', 'button selector', t('player.next_now'));
    const cancel = el('div', 'button selector', t('action.cancel'));
    row.appendChild(now);
    row.appendChild(cancel);
    box.appendChild(row);
    root.appendChild(box);
    nextBox = box;

    function closeBox(): void {
      if (nextTimer) window.clearInterval(nextTimer);
      nextTimer = 0;
      if (nextBox && nextBox.parentNode) nextBox.parentNode.removeChild(nextBox);
      nextBox = null;
    }
    function proceed(): void {
      closeBox();
      autoAdvances++;
      if (!goNext()) {
        setMode('player');
        showPanel();
        refreshCenter();
      }
    }
    function stay(): void {
      closeBox();
      setMode('player');
      showPanel();
      refreshCenter();
    }
    on(now, 'hover:enter', function () {
      autoAdvances = 0; // an explicit choice, not a sleeper's auto-advance
      proceed();
    });
    on(cancel, 'hover:enter', stay);
    if (!askFirst)
      nextTimer = window.setInterval(function () {
        left--;
        if (left <= 0) {
          proceed();
          return;
        }
        text.textContent = t('player.next_in', { n: String(left) });
      }, 1000);

    addMode('player_next', {
      toggle: function () {
        Controller.collectionSet(box);
        Controller.collectionFocus(cancel, box);
      },
      left: function () {
        Controller.moveOr('left');
      },
      right: function () {
        Controller.moveOr('right');
      },
      enter: function () {
        const f = Navigator.getFocusedElement();
        if (f) trigger(f, 'hover:enter');
      },
      back: stay,
    });
    setMode('player_next');
  }
  function onVideoError(): void {
    // teardownEngine does removeAttribute('src')+load(), which fires a spurious
    // async 'error' with an empty currentSrc. Ignore it — otherwise a
    // quality/voice/episode switch triggers a phantom 800ms native recovery on
    // the fresh stream (double-load, worst on Chromium-47).
    if (!video.currentSrc) return;
    const err = video.error;
    const code = err ? err.code : 0;
    diag('player:video-error', { code: code, msg: err ? (err as unknown as { message?: string }).message : '', cur: Math.round(absTime()), attempts: recoverAttempts, src: (video.currentSrc || '').slice(0, 40) });
    // Native <video> path (no hls.js): a torrent stream stalls → code 2/3/4 that
    // is really a transient piece wait. Auto-recover by reloading at the current
    // position instead of dumping the user to the error screen (they had to
    // exit to the torrent list and restart). hls.js has its own recovery, so
    // only guard the native path.
    if (!hls && recoverAttempts < MAX_RECOVER) {
      recoverAttempts++;
      // Some webviews zero currentTime in the error state; fall back to the last
      // good play position so a transient stall resumes in place, not from 0.
      const at = video.currentTime ? absTime() : lastGoodTime || resumeAt || 0;
      setBuffering(true);
      if (recoverTimer) window.clearTimeout(recoverTimer);
      recoverTimer = window.setTimeout(function () {
        if (destroyed) return;
        resumeAt = at;
        reload(false); // teardown + fresh src, then seek back to `at`
      }, 800);
      return;
    }
    let msg = t('player.error_generic');
    if (code === 2) msg = t('player.error_network');
    else if (code === 3) msg = t('player.error_decode');
    else if (code === 4) msg = t('player.error_unsupported');
    showError(msg);
  }

  // Laptop parity. Without these the panel, once auto-hidden, could not be
  // brought back with a mouse at all, and clicking the video did nothing.
  // stopPropagation keeps pointer.ts's delegated click from ALSO dispatching
  // the active mode's enter (double toggle).
  function onMouseMoveReveal(): void {
    if (!panelVisible) showPanel();
    else armHide();
  }
  function onVideoClick(e: Event): void {
    e.stopPropagation();
    togglePlay();
    showPanel();
  }
  function onTimelineClick(e: MouseEvent): void {
    e.stopPropagation();
    const r = timeline.getBoundingClientRect();
    const dur = videoDuration();
    if (!r.width || !dur || !isFinite(dur)) return;
    const pct = Math.max(0, Math.min(1, (e.clientX - r.left) / r.width));
    seekClamped(Math.min(pct * dur, Math.max(0, dur - 2)));
    showPanel();
  }
  // Persist the position when the app is backgrounded or killed (Exit/Home on
  // Tizen fires pagehide; a tab switch fires visibilitychange). The 10s timer
  // alone lost up to 10s of progress on every hard exit.
  function onPageHide(): void {
    flushTimecode();
  }
  function onVisibility(): void {
    if (document.hidden) flushTimecode();
  }
  // Digits 0–9 jump to 0–90% (Lampa parity; remotes with a numpad), Space
  // toggles play, M mutes (laptop). Only in the transport/panel rings — never
  // over a menu/countdown/error, and never while a text field has focus.
  function onExtraKey(e: KeyboardEvent): void {
    const ae = document.activeElement;
    if (ae && (ae.tagName === 'INPUT' || ae.tagName === 'TEXTAREA')) return;
    const m = Controller.enabled().name;
    if (m !== 'player' && m !== 'player_rewind' && m !== 'player_panel') return;
    const code = e.keyCode || (e as unknown as { which: number }).which;
    if (code >= 48 && code <= 57) {
      const dur = videoDuration();
      if (!dur || !isFinite(dur)) return;
      e.preventDefault();
      seekClamped(Math.min(((code - 48) / 10) * dur, Math.max(0, dur - 2)));
      autoAdvances = 0;
      showPanel();
    } else if (code === 32) {
      e.preventDefault();
      togglePlay();
      showPanel();
    } else if (code === 77) {
      e.preventDefault();
      toggleMute();
    }
  }
  window.addEventListener('keydown', onExtraKey);
  root.addEventListener('mousemove', onMouseMoveReveal);
  video.addEventListener('click', onVideoClick);
  timeline.addEventListener('click', onTimelineClick);
  window.addEventListener('pagehide', onPageHide);
  document.addEventListener('visibilitychange', onVisibility);

  video.addEventListener('waiting', onWaiting);
  video.addEventListener('stalled', onWaiting);
  video.addEventListener('playing', onPlayingLike);
  video.addEventListener('canplay', onPlayingLike);
  video.addEventListener('play', onPlayPause);
  video.addEventListener('pause', onPlayPause);
  video.addEventListener('progress', onBuffered);
  video.addEventListener('timeupdate', onTimeUpdate);
  video.addEventListener('loadedmetadata', onLoadedMeta);
  video.addEventListener('ended', onEnded);
  video.addEventListener('error', onVideoError);

  // ---- controller modes ----
  function setMode(name: string): void {
    currentMode = name;
    Controller.toggle(name);
  }
  let currentMode = 'player';
  let lastBackAt = 0;

  // Focus rings mirror Lampa: player (invisible transport) → player_rewind
  // (timeline focused) → player_panel (button row). Up/down step between them.
  //
  // Base transport: left/right scrub, OK play/pause, up reveals + focuses the
  // timeline, down/back hides the panel or (when already hidden) asks to exit.
  // Remote media keys (⏯ ⏪ ⏩ ⏹ ⏭ ⏮ — dispatched by controller.ts as named
  // actions) behave the same in every transport mode, so they are mixed into
  // each mode here instead of being retyped three times.
  const mediaKeys: ControllerCalls = {
    playpause: function () {
      togglePlay();
      showPanel();
    },
    play: function () {
      if (video.paused) {
        tryPlay();
        refreshCenter();
      }
      showPanel();
    },
    pause: function () {
      if (!video.paused) {
        video.pause();
        refreshCenter();
      }
      showPanel();
    },
    stop: function () {
      exitToSources();
    },
    rewind: function () {
      rewind(false);
    },
    forward: function () {
      rewind(true);
    },
    next: function () {
      goNext();
    },
    prev: function () {
      goPrev();
    },
  };
  function addMode(name: string, calls: ControllerCalls): void {
    for (const k in mediaKeys) {
      if (Object.prototype.hasOwnProperty.call(mediaKeys, k) && !(k in calls)) calls[k] = mediaKeys[k];
    }
    // Every user action (anything but the mode's own toggle/gone) proves someone
    // is watching — reset the auto-advance counter.
    for (const k in calls) {
      if (!Object.prototype.hasOwnProperty.call(calls, k) || k === 'toggle' || k === 'gone' || k === 'invisible') continue;
      (function (key: string, fn: unknown) {
        if (typeof fn !== 'function') return;
        (calls as unknown as { [k: string]: () => void })[key] = function () {
          autoAdvances = 0;
          (fn as () => void)();
        };
      })(k, (calls as unknown as { [k: string]: unknown })[k]);
    }
    Controller.add(name, calls);
  }

  addMode('player', {
    invisible: true,
    toggle: function () {
      Controller.clear();
      if (!suppressReveal) showPanel();
    },
    left: function () {
      rewind(false);
    },
    right: function () {
      rewind(true);
    },
    up: function () {
      // "▲ Next episode" is offered only while the panel is hidden — with the
      // panel open UP must keep meaning "to the timeline".
      if (skipVisible() && !panelVisible) {
        skipEl.classList.add('hide');
        goNext();
        return;
      }
      showPanel();
      setMode('player_rewind');
    },
    down: function () {
      // Down is navigational, never an exit — drop into the button row (reveal
      // the panel first if hidden). Only Back exits the player.
      showPanel();
      setMode('player_panel');
    },
    enter: function () {
      togglePlay();
      showPanel();
    },
    back: function () {
      // Two Backs within 2s exit (Back→Back), replacing Back→Back→←→OK through a
      // confirm whose default was Cancel. A panel-hiding Back counts as the first.
      const nowMs = Date.now();
      if (nowMs - lastBackAt < 2000) {
        exitToSources();
        return;
      }
      lastBackAt = nowMs;
      if (panelVisible) hidePanel();
      else toast(t('player.exit_again'));
    },
  });

  // Timeline focus (Lampa player_rewind): left/right scrub along the bar, down
  // drops into the button row, up returns to hidden transport.
  addMode('player_rewind', {
    toggle: function () {
      Controller.collectionSet(panelBody);
      Controller.collectionFocus(timeline, panelBody);
      timeline.classList.add('focus');
      showPanel();
    },
    left: function () {
      rewind(false);
    },
    right: function () {
      rewind(true);
    },
    up: function () {
      timeline.classList.remove('focus');
      setMode('player');
    },
    down: function () {
      timeline.classList.remove('focus');
      setMode('player_panel');
    },
    enter: function () {
      togglePlay();
      showPanel();
    },
    gone: function () {
      timeline.classList.remove('focus');
    },
    back: function () {
      timeline.classList.remove('focus');
      hidePanel();
      setMode('player');
    },
  });

  // Panel focus: left/right move between the footer buttons.
  addMode('player_panel', {
    toggle: function () {
      Controller.collectionSet(lineTwo);
      Controller.collectionFocus(lastFocused || false, lineTwo);
      showPanel();
    },
    left: function () {
      Controller.moveOr('left');
      showPanel();
    },
    right: function () {
      Controller.moveOr('right');
      showPanel();
    },
    up: function () {
      // Back up to the timeline scrub ring.
      setMode('player_rewind');
    },
    down: function () {
      hidePanel();
      setMode('player');
    },
    enter: function () {
      const f = Navigator.getFocusedElement();
      if (f) trigger(f, 'hover:enter');
    },
    gone: function () {
      Controller.clear();
    },
    back: function () {
      hidePanel();
      setMode('player');
    },
  });

  // ---- boot ----
  progressTimer = window.setInterval(emitProgress, 10000);
  updateClock();
  clockTimer = window.setInterval(updateClock, 15000);
  statsTimer = window.setInterval(updateStats, 1000);
  startEngine();
  refreshCenter();
  setMode('player');

  // Lazily learn the torrent's audio tracks (non-blocking) so the track menu
  // can offer them; switching re-muxes the picked track inline.
  function loadCtxAudio(): void {
    if (!ctx.loadAudioTracks) return;
    ctx.loadAudioTracks(function (tracks) {
      if (destroyed || !tracks || !tracks.length) return;
      ctxAudio = tracks;
      updateAudioTracks();
    });
  }
  loadCtxAudio();
  loadEpisodes();

  return {
    resume: function () {
      setMode(currentMode === 'player_menu' ? 'player' : currentMode);
    },
    pause: function () {
      video.pause();
    },
    destroy: function () {
      flushTimecode(); // replaceRoot / eviction paths never went through exitToSources
      destroyed = true;
      window.removeEventListener('keydown', onExtraKey);
      root.removeEventListener('mousemove', onMouseMoveReveal);
      video.removeEventListener('click', onVideoClick);
      timeline.removeEventListener('click', onTimelineClick);
      window.removeEventListener('pagehide', onPageHide);
      document.removeEventListener('visibilitychange', onVisibility);
      if (hideTimer) window.clearTimeout(hideTimer);
      if (nextTimer) window.clearInterval(nextTimer);
      if (rewindTimer) window.clearTimeout(rewindTimer);
      if (recoverTimer) window.clearTimeout(recoverTimer);
      if (progressTimer) window.clearInterval(progressTimer);
      if (clockTimer) window.clearInterval(clockTimer);
      if (statsTimer) window.clearInterval(statsTimer);
      video.removeEventListener('waiting', onWaiting);
      video.removeEventListener('stalled', onWaiting);
      video.removeEventListener('playing', onPlayingLike);
      video.removeEventListener('canplay', onPlayingLike);
      video.removeEventListener('play', onPlayPause);
      video.removeEventListener('pause', onPlayPause);
      video.removeEventListener('progress', onBuffered);
      video.removeEventListener('timeupdate', onTimeUpdate);
      video.removeEventListener('loadedmetadata', onLoadedMeta);
      video.removeEventListener('ended', onEnded);
      video.removeEventListener('error', onVideoError);
      teardownEngine();
      empty(root);
      // Release the mode registrations: their closures held root/video/hls of a
      // destroyed player until the next one registered the same names.
      const modes = ['player', 'player_rewind', 'player_panel', 'player_menu', 'player_error', 'player_next'];
      for (let i = 0; i < modes.length; i++) Controller.remove(modes[i]);
    },
  };
}

// Public entry: push the player as a router activity (Back pops it and
// resumes the sources screen underneath). screens/sources.ts calls this once
// resolve returns non-empty streams.
export function openPlayer(ctx: PlayerContext): void {
  router.push(function (container: HTMLElement) {
    return mountPlayer(container, ctx);
  });
}
