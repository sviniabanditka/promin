// User settings store (docs/frontend.md, docs/api.md).
//
// Single source of truth for the four user-facing preferences the settings
// screen exposes:
//   - lang            → owned by core/i18n (its own 'promin:lang' storage);
//                       this module only bridges the server PUT for it.
//   - default_quality → preselected stream in the player (auto/1080/720/480).
//   - player_engine   → engine override in core/player (auto/hls.js/native).
//   - legacy_tv_mode  → forces demuxed_hls=false in capabilities + drops heavy
//                       animations via a <body class="legacy-tv">.
//
// Values live in localStorage for instant paint on cold boot (docs/frontend.md), and
// are mirrored to the backend (PUT /settings/{key}) when logged in so they sync
// across devices. On boot syncFromServer() pulls GET /settings and applies it.
//
// ES5 target (swc): plain functions/const/let, no async/await, no spread,
// no Array.find/includes, no Object.assign.

import { getSettings, putSetting, postDeviceSettings } from './api';
import { isLogged } from './auth';
import { setLang, getLang, Lang } from './i18n';
import { setLegacyOverride } from './capabilities';

export type Quality = 'auto' | '2160' | '1080' | '720' | '480';
export type Engine = 'auto' | 'hlsjs' | 'native';
export type SubSize = 'small' | 'medium' | 'large';

const STORAGE_KEY = 'promin:settings';

interface Store {
  default_quality: Quality;
  player_engine: Engine;
  // DEVICE-LOCAL (never synced): this TV can't do HTTP/2 media / demuxed HLS.
  // The single switch for the old-Samsung path — no User-Agent autodetect.
  legacy_tv_mode: boolean;
  // DEVICE-LOCAL: drop transitions/animations (weak GPU), independent of the
  // transport mode above.
  reduce_motion: boolean;
  // DEVICE-LOCAL: diagnostics mode — the client reports key codes, viewport,
  // JS errors and other probes to the server log (core/diag.ts). The one
  // switch every future "what does this TV actually do" check hangs off.
  debug_mode: boolean;
  // Playback rate, remembered across episodes/titles (the player applies it on
  // every load, and the speed menu writes it back).
  player_speed: number;
  // Idle minutes before the screensaver activates. 0 = disabled.
  screensaver_min: number;
  // Subtitle text size in the player's own cue renderer.
  subtitle_size: SubSize;
  // Night mode: a black shade over the WHOLE app (video, menus, subtitles) —
  // TV webviews expose no backlight API, so this imitates "brightness 0".
  // Synced: it's the user's habit, not the device's quirk.
  night_mode: boolean;
  night_dim: number; // shade opacity in %, 50..90 step 5
}

// Diagnostics is opt-in (Settings → diagnostics mode, or ?debug=1 in the URL).
const DEBUG_DEFAULT = false;

const store: Store = {
  default_quality: 'auto',
  player_engine: 'auto',
  legacy_tv_mode: false,
  reduce_motion: false,
  debug_mode: DEBUG_DEFAULT,
  player_speed: 1,
  screensaver_min: 5,
  night_mode: false,
  night_dim: 85,
  subtitle_size: 'medium',
};

function isSubSize(v: unknown): v is SubSize {
  return v === 'small' || v === 'medium' || v === 'large';
}

function isQuality(v: unknown): v is Quality {
  return v === 'auto' || v === '2160' || v === '1080' || v === '720' || v === '480';
}

function isEngine(v: unknown): v is Engine {
  return v === 'auto' || v === 'hlsjs' || v === 'native';
}

// Playback rates the speed menu offers; anything else is rejected so a corrupt
// cache can't leave playback stuck at 0x or 10x.
function isSpeed(v: unknown): boolean {
  return typeof v === 'number' && v >= 0.25 && v <= 4;
}

function isScreensaverMin(v: unknown): boolean {
  return v === 0 || v === 3 || v === 5 || v === 10;
}

function isLang(v: unknown): v is Lang {
  return v === 'uk' || v === 'ru' || v === 'en';
}

function persist(): void {
  try {
    window.localStorage.setItem(STORAGE_KEY, JSON.stringify(store));
  } catch (e) {
    /* storage unavailable/full — settings just won't persist across sessions */
  }
}

function readLocal(): void {
  try {
    const raw = window.localStorage.getItem(STORAGE_KEY);
    if (!raw) return;
    const data = JSON.parse(raw) as { [k: string]: unknown };
    if (isQuality(data.default_quality)) store.default_quality = data.default_quality;
    if (isEngine(data.player_engine)) store.player_engine = data.player_engine;
    store.legacy_tv_mode = !!data.legacy_tv_mode;
    store.reduce_motion = !!data.reduce_motion;
    store.debug_mode = data.debug_mode === undefined ? DEBUG_DEFAULT : !!data.debug_mode;
    if (isSpeed(data.player_speed)) store.player_speed = data.player_speed as number;
    if (isScreensaverMin(data.screensaver_min)) store.screensaver_min = data.screensaver_min as number;
    if (isSubSize(data.subtitle_size)) store.subtitle_size = data.subtitle_size;
    store.night_mode = !!data.night_mode;
    if (isNightDim(data.night_dim)) store.night_dim = data.night_dim as number;
  } catch (e) {
    /* ignore corrupt cache */
  }
}

// Legacy TV mode feeds the capabilities layer (demuxed_hls=false, h1, hls.js).
function applyLegacy(): void {
  setLegacyOverride(store.legacy_tv_mode);
}

// <body class="reduce-motion"> — CSS kills transitions/animations. Plain
// className string edits to stay friendly to the oldest webviews.
function setBodyClass(name: string, on: boolean): void {
  try {
    const body = document.body;
    if (!body) return;
    const cls = body.className || '';
    const has = (' ' + cls + ' ').indexOf(' ' + name + ' ') !== -1;
    if (on && !has) {
      body.className = cls ? cls + ' ' + name : name;
    } else if (!on && has) {
      const parts = cls.split(/\s+/);
      const kept: string[] = [];
      for (let i = 0; i < parts.length; i++) {
        if (parts[i] && parts[i] !== name) kept.push(parts[i]);
      }
      body.className = kept.join(' ');
    }
  } catch (e) {
    /* ignore */
  }
}
function applyReduceMotion(): void {
  setBodyClass('reduce-motion', store.reduce_motion);
}

// A host switch (core/legacy.ts) hands the mode over in the URL: the two hosts
// are different origins with separate localStorage.
// ?debug=1 switches diagnostics on before login — for a device where the PIN
// screen itself cannot be operated (phone in MSX: taps do nothing).
function readLegacyFromURL(): void {
  try {
    const m = /[?&]legacy=([01])/.exec(window.location.search);
    if (m) store.legacy_tv_mode = m[1] === '1';
    const d = /[?&]debug=([01])/.exec(window.location.search);
    if (d) store.debug_mode = d[1] === '1';
  } catch (e) {
    /* ignore */
  }
}

// ---- boot --------------------------------------------------------------

export function initSettings(): void {
  readLocal();
  readLegacyFromURL();
  persist();
  applyLegacy();
  applyReduceMotion();
  applyNight();
}

// ---- night mode ---------------------------------------------------------

export function isNightDim(v: unknown): boolean {
  return typeof v === 'number' && v >= 50 && v <= 90 && v % 5 === 0;
}
export const NIGHT_DIM_VALUES: number[] = [50, 55, 60, 65, 70, 75, 80, 85, 90];

// One fixed div on <body> (created lazily), opacity = night_dim%. A plain
// element rather than a filter on <video>: the old Tizen GPU handles a
// composited black quad fine, a brightness filter on a video not so much.
function applyNight(): void {
  try {
    const body = document.body;
    if (!body) return;
    let shade = document.getElementById('night-shade');
    if (!shade) {
      shade = document.createElement('div');
      shade.id = 'night-shade';
      body.appendChild(shade);
    }
    shade.style.opacity = String(store.night_dim / 100);
    setBodyClass('night-mode', store.night_mode);
  } catch (e) {
    /* ignore */
  }
}
export function isNightMode(): boolean {
  return store.night_mode;
}
export function setNightMode(on: boolean): void {
  store.night_mode = on;
  persist();
  applyNight();
  pushSetting('night_mode', on ? 'true' : 'false');
}
export function getNightDim(): number {
  return store.night_dim;
}
export function setNightDim(v: number): void {
  if (!isNightDim(v)) return;
  store.night_dim = v;
  persist();
  applyNight();
  pushSetting('night_dim', String(v));
}

// A synced setting changed on another device (sync event settings_updated):
// mirror the ones that must take effect immediately.
let langChangedHook: (() => void) | null = null;
export function setLangChangedHook(fn: () => void): void {
  langChangedHook = fn;
}

let screensaverChangedHook: (() => void) | null = null;
export function setScreensaverChangedHook(fn: () => void): void {
  screensaverChangedHook = fn;
}

export function applyRemoteSetting(key: string, value: string): void {
  if (key === 'default_quality' && isQuality(value)) {
    store.default_quality = value;
    persist();
  } else if (key === 'player_engine' && isEngine(value)) {
    store.player_engine = value;
    persist();
  } else if (key === 'subtitle_size' && isSubSize(value)) {
    store.subtitle_size = value;
    persist();
  } else if (key === 'screensaver_min') {
    const sv = parseInt(value, 10);
    if (isScreensaverMin(sv)) {
      store.screensaver_min = sv;
      persist();
      if (screensaverChangedHook) screensaverChangedHook();
    }
  } else if (key === 'lang') {
    if (isLang(value) && value !== getLang()) {
      setLang(value);
      if (langChangedHook) langChangedHook();
    }
  } else if (key === 'night_mode') {
    store.night_mode = value === 'true';
    persist();
    applyNight();
  } else if (key === 'night_dim') {
    const nd = parseInt(value, 10);
    if (isNightDim(nd)) {
      store.night_dim = nd;
      persist();
      applyNight();
    }
  } else if (key === 'player_speed') {
    const sp = parseFloat(value);
    if (isSpeed(sp)) {
      store.player_speed = sp;
      persist();
    }
  }
}

// ---- reads (synchronous) -----------------------------------------------

export function getDefaultQuality(): Quality {
  return store.default_quality;
}

export function getPlayerEngine(): Engine {
  return store.player_engine;
}

export function getPlayerSpeed(): number {
  return store.player_speed;
}

export function setPlayerSpeed(v: number): void {
  if (!isSpeed(v)) return;
  store.player_speed = v;
  persist();
  pushSetting('player_speed', String(v));
}

export function isLegacyTv(): boolean {
  return store.legacy_tv_mode;
}
export function isReduceMotion(): boolean {
  return store.reduce_motion;
}
export function isDebugMode(): boolean {
  return store.debug_mode;
}
export function setDebugMode(on: boolean): void {
  store.debug_mode = on;
  persist();
  reportDeviceSettings();
}
// Device-local: not mirrored to the server (another TV must not inherit it).
export function setReduceMotion(on: boolean): void {
  store.reduce_motion = on;
  persist();
  applyReduceMotion();
  reportDeviceSettings();
}

// ---- writes (apply immediately, best-effort server mirror) -------------

function pushSetting(key: string, value: string): void {
  if (!isLogged()) return;
  putSetting(key, value).then(
    function () {},
    function () {
      /* offline: localStorage keeps the value; server catches up on next edit */
    }
  );
}

export function setDefaultQuality(v: Quality): void {
  store.default_quality = v;
  persist();
  pushSetting('default_quality', v);
}

export function setPlayerEngine(v: Engine): void {
  store.player_engine = v;
  persist();
  pushSetting('player_engine', v);
}

export function getSubSize(): SubSize {
  return store.subtitle_size;
}

export function setSubSize(v: SubSize): void {
  store.subtitle_size = v;
  persist();
  pushSetting('subtitle_size', v);
}

export function getScreensaverMin(): number {
  return store.screensaver_min;
}
export function setScreensaverMin(v: number): void {
  store.screensaver_min = v;
  persist();
  pushSetting('screensaver_min', String(v));
}

// Device-local: not mirrored to the server — it describes THIS TV's webview,
// and syncing it would drag every other device onto the HTTP/1.1 path.
export function setLegacyTv(on: boolean): void {
  store.legacy_tv_mode = on;
  persist();
  applyLegacy();
  reportDeviceSettings();
}

// Device-local settings are invisible to the profile; report them so the Mini
// App can show and flip them for THIS TV (remote action set_local).
export function reportDeviceSettings(): void {
  if (!isLogged()) return;
  postDeviceSettings({
    legacy_tv_mode: store.legacy_tv_mode ? 'true' : 'false',
    reduce_motion: store.reduce_motion ? 'true' : 'false',
    debug_mode: store.debug_mode ? 'true' : 'false',
  }).then(
    function () {},
    function () {}
  );
}

// A device-local setting flipped from the Mini App.
export function applyLocalSetting(key: string, on: boolean): boolean {
  if (key === 'legacy_tv_mode') setLegacyTv(on);
  else if (key === 'reduce_motion') setReduceMotion(on);
  else if (key === 'debug_mode') setDebugMode(on);
  else return false;
  return true;
}

// lang lives in i18n; this just applies + mirrors it to the server.
export function setLanguage(lang: Lang): void {
  setLang(lang);
  pushSetting('lang', lang);
}

// ---- server sync -------------------------------------------------------

// Pull server-side settings and apply them over the local defaults. Called at
// boot (background) and after a fresh login. `onLangChanged` is invoked only
// when the server's lang differs from the currently applied one, so the caller
// can repaint the visible screen (text is language-dependent).
export function syncFromServer(onLangChanged?: () => void): void {
  if (!isLogged()) return;
  getSettings().then(
    function (res) {
      const s = res && res.settings ? res.settings : null;
      if (!s) return;

      if (isQuality(s.default_quality)) store.default_quality = s.default_quality;
      if (isEngine(s.player_engine)) store.player_engine = s.player_engine;
      if (typeof s.player_speed === 'string') {
        const sp = parseFloat(s.player_speed);
        if (isSpeed(sp)) store.player_speed = sp;
      }
      if (typeof s.screensaver_min === 'string') {
        const sv = parseInt(s.screensaver_min, 10);
        if (isScreensaverMin(sv)) store.screensaver_min = sv;
      }
      if (isSubSize(s.subtitle_size)) store.subtitle_size = s.subtitle_size;
      if (typeof s.night_mode === 'string') store.night_mode = s.night_mode === 'true';
      if (typeof s.night_dim === 'string') {
        const nd = parseInt(s.night_dim, 10);
        if (isNightDim(nd)) store.night_dim = nd;
      }
      persist();
      applyLegacy();
      applyNight();

      if (isLang(s.lang) && s.lang !== getLang()) {
        setLang(s.lang);
        if (onLangChanged) onLangChanged();
      }
    },
    function () {
      /* no server settings — local values stand */
    }
  );
}
