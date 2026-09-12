// Entry point. Polyfill first (bundled into app.js, not a separate <script>
// tag). Only fetch: Promise is native from Chrome 32 and the floor is Chromium
// 38 (webOS 3), so promise-polyfill was 4 KB of dead code.
import 'whatwg-fetch';

import Controller from './core/controller';
import { initPointer } from './core/pointer';
import * as router from './core/router';
import { initI18n, t } from './core/i18n';
import { toast } from './ui/toast';
import { mountHome } from './screens/home';
import { mountPinEntry } from './screens/pin';
import { openTitle, openRoute } from './screens/nav';
import { Direction } from './core/nav';
import { setFeatures } from './core/features';
import { dispatchRemote, RemoteAction } from './core/player/remote';
import { isLogged, clearLocal, onAuthChange } from './core/auth';
import { setDeadSessionHook, getPing } from './core/api';
import * as sync from './core/sync';
import * as screensaver from './core/screensaver';
import { initSettings, syncFromServer, setNightMode, isNightMode, setScreensaverChangedHook, applyLocalSetting, reportDeviceSettings } from './core/settings';
import { steerHost } from './core/legacy';
import { installGlobalHooks, report, viewportInfo } from './core/diag';

const BASE_WIDTH = 1280;
const BASE_ROOT_FONT_SIZE = 10; // px; 1rem == 10px at the 1280x720 baseline
const PHONE_BASE = 390; // baseline width for phones (portrait)
const PHONE_FLOOR = 0.75; // don't shrink below this scale (keeps 48px touch targets)

// Physical phone detection: touch-capable AND the smaller screen dimension is
// phone-sized. Uses screen.* (not innerWidth) so it's rotation-independent.
function isPhone(): boolean {
  const mtp = (navigator as unknown as { maxTouchPoints?: number }).maxTouchPoints || 0;
  const touch = 'ontouchstart' in window || mtp > 0;
  const minDim = Math.min(screen.width || BASE_WIDTH, screen.height || BASE_WIDTH);
  return !!touch && minDim < 540;
}

// rem scaling. TV/desktop: linear off the 1280 baseline. Phone: scale off a
// phone baseline using the STABLE min(w,h) so rotation doesn't reflow the rem,
// with a floor so the rail/targets don't collapse at device-width.
// Some TV webviews (Android TV via MSX on Xiaomi MiTV) honour the
// <meta viewport width=1280> for LAYOUT but never zoom it to fit the panel:
// the visible viewport stays device-width (960 CSS px at dpr 2) and the page is
// shown 1:1, cropped on the right and bottom. Detected as innerWidth <
// documentElement.clientWidth. Fix: hand the layout the device width too; the
// whole UI is rem/%-based and re-scales from applyViewportScale.
let viewportFixed = false;
function fixCroppedViewport(): void {
  if (viewportFixed) return;
  const de = document.documentElement;
  if (de.classList.contains('is-phone')) return;
  const inner = window.innerWidth || 0;
  const layout = de.clientWidth || 0;
  if (!inner || !layout || inner >= layout - 2) return;
  const vp = document.querySelector('meta[name=viewport]');
  if (!vp) return;
  viewportFixed = true;
  vp.setAttribute('content', 'width=device-width, initial-scale=1, maximum-scale=1, user-scalable=no');
  de.classList.add('vp-cropped');
  report('viewport-fix', { inner: inner, layout: layout });
}

function applyViewportScale(): void {
  fixCroppedViewport();
  const phone = document.documentElement.classList.contains('is-phone');
  if (phone) {
    // screen.*, not innerWidth/innerHeight: the soft keyboard shrinks the inner
    // height (landscape: to ~170px) and fired a resize that scaled the whole UI
    // to the 0.75 floor under the user's finger while typing.
    const dim = Math.min(screen.width || PHONE_BASE, screen.height || PHONE_BASE);
    const scale = Math.max(dim / PHONE_BASE, PHONE_FLOOR);
    document.documentElement.style.fontSize = BASE_ROOT_FONT_SIZE * scale + 'px';
    return;
  }
  // LAYOUT viewport width, not window.innerWidth: the Android TV WebView (MSX,
  // Xiaomi MiTV) lays the page out at the <meta viewport> 1280 but reports
  // innerWidth = 960 (visual viewport at dpr 2). Scaling off 960 gave a 7.5px
  // root font on a 1280-wide layout — everything 25% small and misplaced
  // (diagnosed via the diag log: inner 960x540, doc 1280x720).
  const de = document.documentElement;
  const width = de.clientWidth || window.innerWidth || BASE_WIDTH;
  const height = de.clientHeight || window.innerHeight || 0;
  de.style.fontSize = BASE_ROOT_FONT_SIZE * (width / BASE_WIDTH) + 'px';
  // A webview that reports a viewport TALLER than the 16:9 panel would push
  // bottom-anchored layers (player panel, toasts, hero) below the visible
  // edge. Flag it; CSS confines the app to a 72rem stage (= 16:9 height at
  // 128rem width) and anchors fixed layers to that stage.
  // Only a TV webview gets the 72rem stage: there the extra height is off the
  // panel (Xiaomi/MSX overshoot). A desktop browser window at 16:10 is simply
  // tall and everything visible — pinning it to 16:9 left a dead band below
  // the player. Lampa does the same: layout fills the window, scale off width.
  const ideal = width * (9 / 16);
  const tall = !isDesktopBrowser() && (height || ideal) > ideal * 1.03;
  if (tall) de.classList.add('vp-tall');
  else de.classList.remove('vp-tall');
}

// A regular computer browser (laptop/desktop). TV webviews (Tizen, webOS,
// Android TV/MSX) and phones never match. Layout-only heuristic — nothing
// about playback routing depends on it (that is the manual "старый ТВ" switch).
function isDesktopBrowser(): boolean {
  const ua = navigator.userAgent || '';
  if (/Android|Mobile|Tizen|Web0S|SMART-TV|SmartTV|BRAVIA|AFT|MSX/i.test(ua)) return false;
  return /Windows NT|Macintosh|X11|CrOS/.test(ua);
}

function boot(): void {
  initI18n();

  // On a real phone, swap the TV canvas (width=1280) for the device width and
  // flip on the responsive layout BEFORE the first rem scale is computed.
  if (isPhone()) {
    const vp = document.querySelector('meta[name=viewport]');
    if (vp) vp.setAttribute('content', 'width=device-width, initial-scale=1, maximum-scale=1, user-scalable=no');
    document.documentElement.classList.add('is-phone');
    // No double-tap / pinch zoom: the UI is an app, not a page. iOS ignores
    // user-scalable=no since iOS 10, so also veto its gesture events.
    const veto = function (e: Event) {
      if (e.preventDefault) e.preventDefault();
    };
    document.addEventListener('gesturestart', veto);
    document.addEventListener('gesturechange', veto);
    document.addEventListener('dblclick', veto);
  }

  applyViewportScale();
  window.addEventListener('resize', applyViewportScale);


  // Apply cached user settings (lang stays with i18n; quality/engine/legacy-tv)
  // before the first screen renders. Server values sync in the background.
  initSettings();

  // "Режим старого ТВ" decides which host this device should live on
  // (HTTP/1.1-only vs the Cloudflare one) — move if we're on the wrong one.
  steerHost();

  // Diagnostics mode: JS errors + a boot-time viewport snapshot to the server log.
  installGlobalHooks();
  report('boot', viewportInfo());

  const rootEl = document.getElementById('app');
  if (!rootEl) {
    return;
  }

  // Wire remote/keyboard input into the Controller (arrows/enter/back with
  // the 100ms key-repeat throttle) once, globally.
  Controller.initInput();

  // Mouse / touch input layer. Delegated on window; feeds the same Navigator/
  // Controller entry points as the remote (docs/frontend.md).
  initPointer();

  // Any 401 on an authed surface → re-gate to the PIN screen.
  setDeadSessionHook(gateToPin);

  // Global router (activity stack). Back at the root shows an exit toast
  // (real device-exit is handled by MSX/the platform, see docs/frontend.md).
  router.init(rootEl, function () {
    toast({ kind: 'warning', title: t('toast.exit'), text: t('toast.press_back_again') });
  });


  // Idle screensaver (clock/date over rotating art). Global input listeners;
  // disabled when the timeout setting is 0.
  screensaver.init();

  startUpdateWatch();
  // "Open on TV" from the Telegram bot lands on the title page.
  sync.setOpenTitleHandler(function (tmdbID, type, title, resume, season, episode) {
    if (!isLogged()) return;
    // Reset the screen stack first: a title opened while the player is running
    // must not pile a second player on top of the first (both kept playing and
    // reporting state — the remote flickered between them).
    router.replaceRoot(mountHome, '/');
    openTitle(type, tmdbID, resume, season, episode);
    toast({ kind: 'info', icon: '✈', title: t('telegram.opened'), text: title });
  });
  // Remote-control presses from the bot: the player consumes playback actions;
  // night mode is global and works from any screen.
  sync.setRemoteEventHandler(function (action, value, key, str) {
    // D-pad from the bot / Mini App: the same entry points the remote's keys
    // reach, so it works on every screen (title page, source picker, player).
    if (action.slice(0, 4) === 'nav_') {
      screensaver.activity();
      const k = action.slice(4);
      if (k === 'ok') Controller.enter();
      else if (k === 'back') Controller.back();
      else if (k === 'up' || k === 'down' || k === 'left' || k === 'right') Controller.move(k as Direction);
      return;
    }
    if (action === 'night') {
      setNightMode(!isNightMode());
      return;
    }
    if (action === 'set_local') {
      if (applyLocalSetting(key, str === 'true')) {
        toast({ kind: 'info', icon: '⚙', title: t('settings.' + (key === 'legacy_tv_mode' ? 'legacy' : key === 'reduce_motion' ? 'reduce_motion' : 'debug')), text: t(str === 'true' ? 'toggle.on' : 'toggle.off') });
        if (key === 'legacy_tv_mode') steerHost();
      }
      return;
    }
    if (!dispatchRemote(action as RemoteAction, value, str)) {
      toast({ kind: 'info', icon: '🎛', text: t('telegram.remote_no_player') });
    }
  });
  setScreensaverChangedHook(screensaver.reschedule);
  // Tell the server this TV's device-local settings (for the Mini App).
  if (isLogged()) reportDeviceSettings();
  onAuthChange(function () {
    if (isLogged()) reportDeviceSettings();
  });
  routeInitial();
}

// A new server build went live while the app was open: say so once, so the
// user knows a reload picks it up (the bundle is embedded in the binary).
function startUpdateWatch(): void {
  let known = '';
  const poll = function () {
    getPing().then(
      function (p) {
        setFeatures({ youtube: !!(p && p.youtube) });
        const v = p && p.version ? p.version : '';
        if (!v) return;
        if (!known) {
          known = v;
          return;
        }
        if (v !== known) {
          known = v;
          toast({ kind: 'info', icon: '⬆', title: t('app.updated'), text: t('app.updated_hint'), duration: 8000 });
        }
      },
      function () {
        /* offline / restarting — try again next tick */
      }
    );
  };
  poll();
  window.setInterval(poll, 10 * 60 * 1000);
}

// Login gate. If a token is stored we go straight to home and start the sync
// client. Otherwise we probe the catalog: a 401 means the server requires auth
// (PROMIN_REQUIRE_AUTH=true) → show login; anything else means guest browsing
// is allowed → show home. This keeps the flow unchanged when auth is optional.
function routeInitial(): void {
  // Hard gate: no session → PIN entry. No guest browsing (the whole service is
  // closed until a valid PIN is entered).
  if (!isLogged()) {
    router.replaceRoot(mountPinEntry);
    return;
  }
  sync.start();
  // Deep link (#/title/tv/1399, #/catalog/trending, …) or Home.
  openRoute(window.location.hash);
  // Pull server-side settings; the page reloads if the server's language wins.
  syncFromServer();
}

// Bounce to the PIN screen (session died / logged out / switch profile). Wired
// to core/api.ts's dead-session hook so any 401 on an authed surface re-gates.
function gateToPin(): void {
  sync.stop();
  // Drop the token FIRST: api.ts only calls this hook while getToken() is truthy,
  // so clearing it makes the N other in-flight 401s (home fires several requests
  // at once) no-ops instead of N remounts of the PIN screen mid-typing.
  clearLocal();
  router.replaceRoot(mountPinEntry);
}

if (document.readyState === 'loading') {
  document.addEventListener('DOMContentLoaded', boot);
} else {
  boot();
}
