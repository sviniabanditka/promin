// Live TV player (docs/tv.md). A television, not a film player: the picture
// fills the screen, there is no timeline. OK pauses and resumes AT THE LIVE
// EDGE, ▲/▼ zap through the list the channel was opened from, digits pick a
// channel number, ◀ opens the guide (channels + the focused channel's full
// programme, the same widgets as the TV screen), ▶ opens settings (night
// mode, sleep timer, picture, quality, audio). An info bar (number, name,
// now/next with progress, clock) shows after every action and hides itself.
// Back exits. Controller modes: 'live', 'live_guide', 'live_prog', 'live_menu'.

import Controller, { on, trigger, ControllerCalls } from '../controller';
import { Navigator } from '../nav';
import * as router from '../router';
import { t } from '../i18n';
import { ScreenInstance } from '../activity';
import { tvPlay, tvFail, getTvNow, mediaUrl, postPlayerState, startTimeshift, timeshiftUrl } from '../api';
import { ensureHls } from './hls';
import { setRemoteHandler } from './remote';
import { preferNativeHls } from '../capabilities';
import { getPlayerEngine, isNightMode, setNightMode } from '../settings';
import { toast } from '../../ui/toast';
import { el, empty, pad2 } from '../../ui/dom';
import { ChannelList, ProgramPane, GuideChannel, NowMap, loadNowNext, logoBox, loadLogo, hhmm, progress, nowSec } from '../../screens/tv/guide';

export interface LiveContext {
  channels: GuideChannel[];
  index: number;
  sectionLabel?: string;
}

const INFO_MS = 5000;
const DIGITS_MS = 1800;
const FILL_KEY = 'promin:live:fill';
const SLEEP_STEPS = [0, 30, 60, 90, 120];
const PAUSE_SVG = '<svg viewBox="0 0 24 24"><path d="M6 5h4v14H6zm8 0h4v14h-4z"/></svg>';

// The slice of hls.js this player touches (live edge, levels, audio tracks).
interface LiveHls {
  on(event: string, fn: (event: string, data: unknown) => void): void;
  loadSource(url: string): void;
  attachMedia(video: HTMLVideoElement): void;
  destroy(): void;
  liveSyncPosition?: number | null;
  levels?: { height?: number; bitrate?: number }[];
  currentLevel?: number;
  nextLevel?: number;
  audioTracks?: { name?: string; lang?: string }[];
  audioTrack?: number;
  startLoad(): void;
  recoverMediaError(): void;
}

function mountLive(root: HTMLElement, ctx: LiveContext): ScreenInstance {
  root.className += ' live';
  const channels = ctx.channels;
  let idx = ctx.index;
  let destroyed = false;
  let paused = false;
  let loading = 0; // sequence of start() calls
  let hls: LiveHls | null = null;
  let nn: NowMap = {};
  let infoTimer = 0;
  let clockTimer = 0;
  let digitsTimer = 0;
  let digits = '';
  let sleepMin = 0;
  let sleepTimer = 0;
  // ---- timeshift (docs/tv.md) ----
  // The server keeps a rolling window of the channel while somebody watches it,
  // so a pause no longer throws the broadcast away: resuming plays on from the
  // pause instead of jumping back to the live edge.
  let bufferWindow = 0; // seconds the server says we may seek back into; 0 = off
  let bufferTimer = 0;
  let pausedAt = 0; // Date.now() at the pause
  let timeshiftOn = false; // playing out of the buffer, not the live stream
  let seekBehind = 0; // seconds behind the live edge to land on once seekable
  let seekTries = 0;
  const TIMESHIFT_MIN_S = 5; // a blink of a pause just resumes live
  let netRetries = 0;
  let mediaRecovered = false;
  let stateTimer = 0;

  // ---- DOM ----
  const video = document.createElement('video');
  video.className = 'live__video';
  video.autoplay = true;
  video.setAttribute('playsinline', '');
  root.appendChild(video);
  try {
    if (window.localStorage.getItem(FILL_KEY) === '1') root.classList.add('live--fill');
  } catch (e) {
    /* ignore */
  }
  const spinner = el('div', 'live__spinner');
  root.appendChild(spinner);
  const pausedEl = el('div', 'live__paused hide');
  pausedEl.innerHTML = PAUSE_SVG;
  root.appendChild(pausedEl);
  const pausedHint = el('div', 'live__paused-hint hide', t('live.paused_hint'));
  root.appendChild(pausedHint);

  const info = el('div', 'live-info hide');
  root.appendChild(info);
  const infoLogo = el('div', 'live-info__logo');
  info.appendChild(infoLogo);
  const infoMain = el('div', 'live-info__main');
  info.appendChild(infoMain);
  const infoTop = el('div', 'live-info__top');
  infoMain.appendChild(infoTop);
  const infoNum = el('span', 'live-info__num');
  infoTop.appendChild(infoNum);
  const infoName = el('span', 'live-info__name');
  infoTop.appendChild(infoName);
  const infoBadge = el('span', 'live-info__badge', 'LIVE');
  infoTop.appendChild(infoBadge);
  const infoNow = el('div', 'live-info__now');
  infoMain.appendChild(infoNow);
  const infoBar = el('div', 'live-info__bar');
  const infoFill = el('div', 'live-info__fill');
  infoBar.appendChild(infoFill);
  infoMain.appendChild(infoBar);
  const infoNext = el('div', 'live-info__next');
  infoMain.appendChild(infoNext);
  const infoSide = el('div', 'live-info__side');
  info.appendChild(infoSide);
  const infoClock = el('div', 'live-info__clock');
  infoSide.appendChild(infoClock);
  infoSide.appendChild(el('div', 'live-info__hint', t('live.hint')));

  const miniToast = el('div', 'live-toast hide');
  root.appendChild(miniToast);
  let miniTimer = 0;
  function flash(text: string): void {
    miniToast.textContent = text;
    miniToast.classList.remove('hide');
    if (miniTimer) clearTimeout(miniTimer);
    miniTimer = window.setTimeout(function () {
      miniToast.classList.add('hide');
    }, 1800);
  }

  // ---- info bar ----
  function current(): GuideChannel {
    return channels[idx];
  }

  function paintInfo(typed?: string): void {
    const ch = current();
    if (!ch) return;
    const now = nowSec();
    infoNum.textContent = typed != null ? typed + '_' : String(idx + 1);
    infoName.textContent = ch.name + (ch.quality ? ' · ' + ch.quality : '');
    const behind = behindSec();
    infoBadge.textContent = paused ? t('live.paused').toUpperCase() : behind > 0 ? '-' + fmtBehind(behind) : 'LIVE';
    infoBadge.classList.toggle('is-paused', paused);
    infoBadge.classList.toggle('is-behind', !paused && behind > 0);
    const v = nn[ch.id];
    infoNow.textContent = v && v.now ? hhmm(v.now.start) + '–' + hhmm(v.now.stop) + '  ' + v.now.title : '';
    infoFill.style.width = Math.round(progress(v && v.now, now) * 100) + '%';
    infoBar.classList.toggle('hide', !(v && v.now));
    infoNext.textContent = v && v.next ? t('tv.next') + ': ' + hhmm(v.next.start) + '  ' + v.next.title : '';
    const d = new Date();
    infoClock.textContent = pad2(d.getHours()) + ':' + pad2(d.getMinutes());
  }

  function showInfo(typed?: string): void {
    const ch = current();
    if (!ch) return;
    empty(infoLogo);
    const box = logoBox(ch, 'live-info__logo-box');
    loadLogo(box, ch.name);
    infoLogo.appendChild(box);
    paintInfo(typed);
    info.classList.remove('hide');
    if (infoTimer) clearTimeout(infoTimer);
    if (!paused) {
      infoTimer = window.setTimeout(function () {
        infoTimer = 0;
        info.classList.add('hide');
      }, INFO_MS);
    }
  }

  function hideInfo(): void {
    if (infoTimer) clearTimeout(infoTimer);
    infoTimer = 0;
    info.classList.add('hide');
  }

  function refreshNow(done?: () => void): void {
    loadNowNext(getTvNow).then(function (items) {
      if (destroyed) return;
      nn = items;
      if (!info.classList.contains('hide')) paintInfo();
      if (done) done();
    });
  }

  // ---- engine ----
  function teardown(): void {
    if (hls) {
      try {
        hls.destroy();
      } catch (e) {
        /* ignore */
      }
      hls = null;
    }
    try {
      video.pause();
      video.removeAttribute('src');
      video.load();
    } catch (e) {
      /* ignore */
    }
  }

  function failed(): void {
    spinner.classList.add('hide');
    const ch = current();
    if (ch) tvFail(ch.id)['catch'](function () {});
    toast({ kind: 'error', title: t('tv.no_stream'), text: t('live.no_stream') });
    showInfo();
  }

  function start(i: number): void {
    const ch = channels[i];
    if (!ch) return;
    idx = i;
    paused = false;
    // A new channel: out of the old channel's window, into this one's.
    timeshiftOn = false;
    seekBehind = 0;
    pausedAt = 0;
    bufferWindow = 0;
    keepBuffer();
    pausedEl.classList.add('hide');
    pausedHint.classList.add('hide');
    netRetries = 0;
    mediaRecovered = false;
    const my = ++loading;
    teardown();
    spinner.classList.remove('hide');
    showInfo();
    reportState(true);
    tvPlay(ch.id).then(
      function (p) {
        if (destroyed || my !== loading) return;
        const url = p.direct ? p.url : mediaUrl(p.url);
        attach(url, my);
      },
      function () {
        if (destroyed || my !== loading) return;
        failed();
      }
    );
  }

  // Ask the server to keep this channel's window alive. Failure is not an
  // error the viewer needs to hear about: timeshift simply stays off.
  function keepBuffer(): void {
    const ch = channels[idx];
    if (!ch) return;
    startTimeshift(ch.id).then(
      function (r) {
        if (!destroyed) bufferWindow = r && r.window_sec ? r.window_sec : 0;
      },
      function () {
        if (!destroyed) bufferWindow = 0;
      }
    );
  }

  // Play the channel out of its rolling window, `behind` seconds behind live.
  function goTimeshift(behind: number): void {
    const ch = channels[idx];
    if (!ch || bufferWindow <= 0) {
      resumeLiveEdge();
      return;
    }
    const my = ++loading;
    teardown();
    timeshiftOn = true;
    seekBehind = Math.min(behind, bufferWindow - 30);
    seekTries = 0;
    spinner.classList.remove('hide');
    attach(mediaUrl(timeshiftUrl(ch.id)), my);
    applySeekBehind();
    showInfo();
  }

  // The window's playlist arrives a moment after the engine starts; land on the
  // wanted spot as soon as there is something seekable, then give up quietly.
  function applySeekBehind(): void {
    if (destroyed || !timeshiftOn || seekBehind <= 0) return;
    let done = false;
    try {
      const sk = video.seekable;
      if (sk && sk.length) {
        const end = sk.end(sk.length - 1);
        const startOf = sk.start(0);
        let target = end - seekBehind;
        if (target < startOf) target = startOf;
        if (isFinite(target) && target >= 0 && end - startOf > 1) {
          video.currentTime = target;
          done = true;
        }
      }
    } catch (e) {
      /* engines differ on when a live playlist becomes seekable */
    }
    if (done) {
      seekBehind = 0;
      tryPlay();
      return;
    }
    if (++seekTries > 40) return; // ~10 s: play from wherever the engine landed
    window.setTimeout(applySeekBehind, 250);
  }

  // Seconds behind the live edge right now (0 when riding the edge).
  function behindSec(): number {
    if (!timeshiftOn) return 0;
    try {
      const sk = video.seekable;
      if (!sk || !sk.length) return 0;
      const d = sk.end(sk.length - 1) - video.currentTime;
      return d > 2 ? Math.round(d) : 0;
    } catch (e) {
      return 0;
    }
  }

  // How far back the programme that is on now started, when the window still
  // reaches it (docs/tv.md). 0 = nothing to offer.
  function programStartBehind(): number {
    const ch = channels[idx];
    if (!ch || bufferWindow <= 0) return 0;
    const cur = nn[ch.id] && nn[ch.id].now;
    if (!cur) return 0;
    const behind = nowSec() - cur.start;
    if (behind < 60) return 0; // it just started; the edge is the beginning
    if (behind > bufferWindow - 60) return 0; // outside the window
    return behind - behindSec() > 0 ? behind : 0;
  }

  function fmtBehind(sec: number): string {
    const m = Math.floor(sec / 60);
    const s2 = Math.floor(sec % 60);
    return m + ':' + pad2(s2);
  }

  function attach(url: string, my: number): void {
    const engine = getPlayerEngine();
    const nativeHls = engine === 'native' ? true : engine === 'hlsjs' ? false : preferNativeHls();
    if (nativeHls) {
      video.src = url;
      tryPlay();
      return;
    }
    ensureHls().then(function (Hls) {
      if (destroyed || my !== loading) return;
      if (!Hls) {
        video.src = url; // no MSE: let the browser try
        tryPlay();
        return;
      }
      const inst = new Hls({
        enableWorker: false,
        liveSyncDurationCount: 3,
        liveMaxLatencyDurationCount: 10,
        manifestLoadingMaxRetry: 4,
        manifestLoadingRetryDelay: 1000,
        levelLoadingMaxRetry: 4,
        levelLoadingRetryDelay: 1000,
        fragLoadingMaxRetry: 6,
        fragLoadingRetryDelay: 1000,
      }) as unknown as LiveHls;
      hls = inst;
      inst.on('hlsError', function (_e: string, data: unknown) {
        if (destroyed || hls !== inst) return;
        const d = data as { fatal?: boolean; type?: string };
        if (!d || !d.fatal) return;
        if (d.type === 'networkError' && netRetries < 3) {
          netRetries++;
          window.setTimeout(function () {
            if (hls === inst) inst.startLoad();
          }, 1000 * netRetries);
          return;
        }
        if (d.type === 'mediaError' && !mediaRecovered) {
          mediaRecovered = true;
          inst.recoverMediaError();
          return;
        }
        failed();
      });
      inst.loadSource(url);
      inst.attachMedia(video);
      tryPlay();
    });
  }

  function tryPlay(): void {
    try {
      const p = video.play();
      if (p && typeof (p as Promise<void>)['catch'] === 'function') (p as Promise<void>)['catch'](function () {});
    } catch (e) {
      /* autoplay policy: the user pressed a key to get here, so this is rare */
    }
  }

  // ---- pause at the live edge ----
  function togglePause(): void {
    if (!paused) {
      video.pause();
      paused = true;
      pausedAt = Date.now();
      pausedEl.classList.remove('hide');
      pausedHint.classList.remove('hide');
      showInfo();
      reportState(true);
      return;
    }
    resumeLive();
  }

  function resumeLive(): void {
    paused = false;
    pausedEl.classList.add('hide');
    pausedHint.classList.add('hide');
    // The broadcast kept running while we stood still: if the server has been
    // buffering it, carry on from the pause instead of skipping what was missed.
    const away = pausedAt ? (Date.now() - pausedAt) / 1000 : 0;
    pausedAt = 0;
    if (bufferWindow > 0 && away >= TIMESHIFT_MIN_S) {
      goTimeshift(away + behindSec());
      return;
    }
    resumeLiveEdge();
  }

  // Back to the live edge of whatever is playing (the stream itself, or the
  // window when the viewer is inside it).
  function resumeLiveEdge(): void {
    // Back to NOW, not to where we paused: a live channel has no "continue".
    try {
      let edge = 0;
      if (hls && hls.liveSyncPosition) edge = hls.liveSyncPosition;
      else if (video.seekable && video.seekable.length) edge = video.seekable.end(video.seekable.length - 1) - 3;
      if (edge > 0 && isFinite(edge) && edge > video.currentTime) video.currentTime = edge;
    } catch (e) {
      /* some engines refuse seeks on live streams; playing on is still right */
    }
    tryPlay();
    showInfo();
    reportState(true);
  }

  // ---- zapping / digits ----
  function zap(dir: 1 | -1): void {
    if (!channels.length) return;
    start((idx + dir + channels.length) % channels.length);
  }

  function commitDigits(): void {
    digitsTimer = 0;
    const n = parseInt(digits, 10);
    digits = '';
    if (n >= 1 && n <= channels.length) {
      if (n - 1 !== idx) start(n - 1);
      else showInfo();
    } else {
      hideInfo();
    }
  }

  function digit(n: number): void {
    if (digits.length >= 4) digits = '';
    digits += String(n);
    showInfo(digits);
    if (digitsTimer) clearTimeout(digitsTimer);
    digitsTimer = window.setTimeout(commitDigits, DIGITS_MS);
  }

  function onExtraKey(e: KeyboardEvent): void {
    if (Controller.enabled().name !== 'live') return;
    const code = e.keyCode || (e as unknown as { which: number }).which;
    if (code >= 48 && code <= 57) {
      e.preventDefault();
      digit(code - 48);
    } else if (code === 32) {
      e.preventDefault();
      togglePause();
    } else if (code === 77) {
      e.preventDefault();
      video.muted = !video.muted;
      flash(video.muted ? '🔇' : '🔊');
    }
  }
  window.addEventListener('keydown', onExtraKey);

  // ---- guide overlay ----
  const guide = el('div', 'live-guide hide');
  root.appendChild(guide);
  const guideList = el('div', 'live-guide__list');
  guide.appendChild(guideList);
  const list = new ChannelList({
    onFocus: function (i) {
      prog.show(channels[i] || null);
    },
    onEnter: function (i) {
      closeGuide();
      if (i !== idx) start(i);
    },
  });
  guideList.appendChild(list.render());
  const guideProg = el('div', 'live-guide__prog');
  guide.appendChild(guideProg);
  const prog = new ProgramPane({
    onEnter: function (ch) {
      closeGuide();
      const i = list.indexOfId(ch.id);
      if (i >= 0 && i !== idx) start(i);
    },
  });
  guideProg.appendChild(prog.el);
  list.setChannels(channels);
  let guideOpen = false;

  function openGuide(): void {
    if (guideOpen) return;
    guideOpen = true;
    hideInfo();
    const ch = current();
    list.setCurrent(ch ? ch.id : '');
    list.setNow(nn);
    refreshNow(function () {
      list.setNow(nn);
    });
    list.lastFocused = false;
    prog.show(ch || null, true);
    guide.classList.remove('hide');
    Controller.toggle('live_guide');
    const row = list.row(idx);
    if (row) list.scroll.immediate(row, true);
  }

  function closeGuide(): void {
    if (!guideOpen) return;
    guideOpen = false;
    guide.classList.add('hide');
    Controller.toggle('live');
  }

  Controller.add('live_guide', {
    toggle: function () {
      Controller.collectionSet(list.render());
      Controller.collectionFocus(list.focusTarget(), list.render());
    },
    up: function () {
      Controller.moveOr('up');
    },
    down: function () {
      Controller.moveOr('down');
    },
    right: function () {
      if (prog.hasRows()) Controller.toggle('live_prog');
    },
    left: function () {
      closeGuide();
    },
    back: function () {
      closeGuide();
    },
  });
  Controller.add('live_prog', {
    toggle: function () {
      Controller.collectionSet(prog.scroll.render());
      Controller.collectionFocus(prog.focusTarget(), prog.scroll.render());
    },
    up: function () {
      Controller.moveOr('up');
    },
    down: function () {
      Controller.moveOr('down');
    },
    left: function () {
      Controller.toggle('live_guide');
    },
    back: function () {
      Controller.toggle('live_guide');
    },
  });

  // ---- settings menu ----
  let menuBox: HTMLElement | null = null;
  let menuLast: HTMLElement | false = false;

  function sleepLabel(): string {
    return sleepMin ? t('player.sleep_min', { n: sleepMin }) : t('live.sleep_off');
  }

  function setSleep(min: number): void {
    sleepMin = min;
    if (sleepTimer) clearTimeout(sleepTimer);
    sleepTimer = 0;
    if (min > 0) {
      sleepTimer = window.setTimeout(function () {
        sleepTimer = 0;
        sleepMin = 0;
        if (!paused) togglePause();
        toast({ kind: 'info', icon: '🌙', title: t('player.sleep_done'), text: t('live.sleep_done'), duration: 8000 });
      }, min * 60000);
    }
  }

  function qualityLabel(): string {
    if (!hls || !hls.levels || hls.levels.length < 2) return '';
    const cur = hls.currentLevel != null ? hls.currentLevel : -1;
    const auto = hls.nextLevel === -1 || hls.nextLevel == null;
    const lvl = cur >= 0 && hls.levels[cur] ? hls.levels[cur] : null;
    const h = lvl && lvl.height ? lvl.height + 'p' : '';
    return auto ? 'AUTO' + (h ? ' · ' + h : '') : h || '?';
  }

  function audioLabel(): string {
    if (!hls || !hls.audioTracks || hls.audioTracks.length < 2) return '';
    const tr = hls.audioTracks[hls.audioTrack || 0];
    return tr ? tr.name || tr.lang || String((hls.audioTrack || 0) + 1) : '';
  }

  interface MenuItem {
    label: string;
    sub: string;
    onEnter: () => void;
  }

  function menuItems(): MenuItem[] {
    const items: MenuItem[] = [
      {
        label: t('settings.night'),
        sub: t(isNightMode() ? 'toggle.on' : 'toggle.off'),
        onEnter: function () {
          setNightMode(!isNightMode());
        },
      },
      {
        label: t('player.sleep'),
        sub: sleepLabel(),
        onEnter: function () {
          const i = SLEEP_STEPS.indexOf(sleepMin);
          setSleep(SLEEP_STEPS[(i + 1) % SLEEP_STEPS.length]);
        },
      },
      {
        label: t('live.aspect'),
        sub: t(root.classList.contains('live--fill') ? 'player.aspect_fill' : 'player.aspect_fit'),
        onEnter: function () {
          const fill = !root.classList.contains('live--fill');
          root.classList.toggle('live--fill', fill);
          try {
            window.localStorage.setItem(FILL_KEY, fill ? '1' : '0');
          } catch (e) {
            /* ignore */
          }
        },
      },
    ];
    if (timeshiftOn) {
      items.push({
        label: t('live.go_live'),
        sub: '-' + fmtBehind(behindSec()),
        onEnter: function () {
          start(idx); // straight back to the broadcast
        },
      });
    }
    const fromStart = programStartBehind();
    if (fromStart > 0) {
      items.push({
        label: t('live.from_start'),
        sub: '-' + fmtBehind(fromStart),
        onEnter: function () {
          goTimeshift(fromStart);
        },
      });
    }
    if (qualityLabel()) {
      items.push({
        label: t('live.quality'),
        sub: qualityLabel(),
        onEnter: function () {
          if (!hls || !hls.levels) return;
          // AUTO → lowest … highest → AUTO
          const n = hls.levels.length;
          const cur = hls.nextLevel == null || hls.nextLevel === -1 ? -1 : hls.nextLevel;
          hls.nextLevel = cur + 1 >= n ? -1 : cur + 1;
        },
      });
    }
    if (audioLabel()) {
      items.push({
        label: t('live.audio'),
        sub: audioLabel(),
        onEnter: function () {
          if (!hls || !hls.audioTracks) return;
          hls.audioTrack = ((hls.audioTrack || 0) + 1) % hls.audioTracks.length;
        },
      });
    }
    return items;
  }

  function renderMenu(): void {
    if (!menuBox) return;
    empty(menuBox);
    menuBox.appendChild(el('div', 'live-menu__title', t('live.settings')));
    const items = menuItems();
    const focusedIdx = menuLast ? parseInt(menuLast.getAttribute('data-i') || '0', 10) : 0;
    menuLast = false;
    for (let i = 0; i < items.length; i++) {
      (function (it: MenuItem, i: number) {
        const row = el('div', 'live-menu__item selector');
        row.setAttribute('data-i', String(i));
        row.appendChild(el('span', undefined, it.label));
        row.appendChild(el('span', 'live-menu__sub', it.sub));
        on(row, 'hover:focus', function () {
          menuLast = row;
        });
        on(row, 'hover:enter', function () {
          it.onEnter();
          renderMenu(); // labels changed
          Controller.toggle('live_menu');
        });
        if (i === focusedIdx) menuLast = row;
        if (menuBox) menuBox.appendChild(row);
      })(items[i], i);
    }
  }

  function openMenu(): void {
    if (menuBox) return;
    hideInfo();
    menuBox = el('div', 'live-menu');
    root.appendChild(menuBox);
    menuLast = false;
    renderMenu();
    Controller.toggle('live_menu');
  }

  function closeMenu(): void {
    if (!menuBox) return;
    root.removeChild(menuBox);
    menuBox = null;
    Controller.toggle('live');
  }

  Controller.add('live_menu', {
    toggle: function () {
      if (!menuBox) return;
      Controller.collectionSet(menuBox);
      Controller.collectionFocus(menuLast || false, menuBox);
    },
    up: function () {
      Controller.moveOr('up');
    },
    down: function () {
      Controller.moveOr('down');
    },
    enter: function () {
      const f = Navigator.getFocusedElement();
      if (f) trigger(f, 'hover:enter');
    },
    right: function () {
      closeMenu();
    },
    left: function () {
      closeMenu();
    },
    back: function () {
      closeMenu();
    },
  });

  // ---- transport mode ----
  const mediaKeys: ControllerCalls = {
    playpause: togglePause,
    play: function () {
      if (paused) resumeLive();
    },
    pause: function () {
      if (!paused) togglePause();
    },
    stop: exit,
    next: function () {
      zap(1);
    },
    prev: function () {
      zap(-1);
    },
  };
  const live: ControllerCalls = {
    invisible: true,
    toggle: function () {
      Controller.clear();
    },
    enter: togglePause,
    up: function () {
      zap(1);
    },
    down: function () {
      zap(-1);
    },
    left: openGuide,
    right: openMenu,
    back: exit,
  };
  for (const k in mediaKeys) if (Object.prototype.hasOwnProperty.call(mediaKeys, k)) live[k] = mediaKeys[k];
  Controller.add('live', live);

  function exit(): void {
    router.back();
  }

  // ---- Mini App remote ----
  setRemoteHandler(function (action, value) {
    switch (action) {
      case 'toggle_play':
        togglePause();
        return true;
      case 'next':
        zap(1);
        return true;
      case 'prev':
        zap(-1);
        return true;
      case 'mute':
        video.muted = !video.muted;
        return true;
      case 'volume':
        video.volume = Math.max(0, Math.min(1, value / 100));
        return true;
      case 'sleep':
        setSleep(value > 0 ? value : 0);
        return true;
    }
    return false;
  });

  // ---- state for the Mini App remote ----
  function reportState(force?: boolean): void {
    const ch = current();
    if (!ch) return;
    if (!force && stateTimer) return;
    postPlayerState({ title: ch.name, media_type: 'live', position_sec: 0, duration_sec: 0, paused: paused })['catch'](function () {});
  }
  stateTimer = window.setInterval(function () {
    reportState(true);
  }, 10000);

  // ---- video events ----
  function onPlaying(): void {
    spinner.classList.add('hide');
  }
  function onWaiting(): void {
    if (!paused) spinner.classList.remove('hide');
  }
  function onVideoError(): void {
    if (hls) return; // hls.js reports its own errors
    failed();
  }
  video.addEventListener('playing', onPlaying);
  video.addEventListener('canplay', onPlaying);
  video.addEventListener('waiting', onWaiting);
  video.addEventListener('stalled', onWaiting);
  video.addEventListener('error', onVideoError);

  // Clock + progress in the bar keep moving while it is visible (paused).
  clockTimer = window.setInterval(function () {
    if (!info.classList.contains('hide')) paintInfo(digits || undefined);
  }, 30000);

  // Keep the channel's rolling window alive while this player is up; the server
  // drops a buffer nobody has asked about for two minutes.
  bufferTimer = window.setInterval(keepBuffer, 30000);

  // ---- boot ----
  Controller.toggle('live');
  refreshNow();
  start(idx);

  return {
    destroy: function () {
      destroyed = true;
      loading++;
      if (infoTimer) clearTimeout(infoTimer);
      if (clockTimer) clearInterval(clockTimer);
      if (digitsTimer) clearTimeout(digitsTimer);
      if (sleepTimer) clearTimeout(sleepTimer);
      if (miniTimer) clearTimeout(miniTimer);
      if (stateTimer) clearInterval(stateTimer);
      if (bufferTimer) clearInterval(bufferTimer);
      window.removeEventListener('keydown', onExtraKey);
      video.removeEventListener('playing', onPlaying);
      video.removeEventListener('canplay', onPlaying);
      video.removeEventListener('waiting', onWaiting);
      video.removeEventListener('stalled', onWaiting);
      video.removeEventListener('error', onVideoError);
      setRemoteHandler(null);
      postPlayerState({ closed: true })['catch'](function () {});
      teardown();
      list.destroy();
      prog.destroy();
      const modes = ['live', 'live_guide', 'live_prog', 'live_menu'];
      for (let i = 0; i < modes.length; i++) Controller.remove(modes[i]);
      empty(root);
    },
    pause: function () {
      /* nothing is pushed on top of the live player */
    },
    resume: function () {
      Controller.toggle('live');
    },
  };
}

export function openLivePlayer(ctx: LiveContext): void {
  router.push(function (container: HTMLElement) {
    return mountLive(container, ctx);
  });
}
