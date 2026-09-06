// Idle screensaver (Lampa-style): after N idle minutes (a setting) a fullscreen
// overlay shows a big clock + date over rotating TMDB backdrops. Any input
// dismisses it and restarts the idle countdown. Never activates while a video
// is actually playing (playback keeps the screen busy). Weather is intentionally
// omitted for v1 — no weather API is wired; the clock/date + art is the ask.
//
// ES5 target (swc): plain functions, no async/await/for-of/spread.

import { el, empty } from '../ui/dom';
import { getScreensaverMin } from './settings';
import { getLang } from './i18n';
import { getBackdrops, getWeather, Backdrop, WeatherDay } from './api';

let idleTimer = 0;
let clockTimer = 0;
let rotTimer = 0;
let overlay: HTMLElement | null = null;
let bgA: HTMLElement | null = null;
let bgB: HTMLElement | null = null;
let showingA = true;
let clockEl: HTMLElement | null = null;
let dateEl: HTMLElement | null = null;
let weatherEl: HTMLElement | null = null;
// Forecast is fetched in the BACKGROUND (init + slow refresh) and rendered
// synchronously on show. Doing the fetch at show-time meant any hiccup — a
// rejected promise, a stale generation guard — silently left the strip hidden,
// which is exactly how it failed in the field. Now the render path has no
// async step at all, so it is as reliable as the clock.
let wxData: { place: string; now: number; code: number; days: WeatherDay[] } | null = null;
let wxTimer = 0;
const WX_REFRESH_MS = 15 * 60 * 1000;
// Bottom-right caption naming the title currently on screen — turns anonymous
// wallpaper into something you can act on ("what IS that film?").
let labelEl: HTMLElement | null = null;

let backdrops: Backdrop[] = [];
let bgIndex = 0;
let fetchedArt = false;
// Bumped whenever the user wakes the screen: a preload that finishes after that
// must not paint or show anything (stale generation).
let gen = 0;

const ROTATE_MS = 20000; // swap the backdrop every 20s
// A single image gets this long to decode before we give up on it and try the
// next one. Without a cap a hanging request would freeze the rotation (and, on
// activation, delay the screensaver forever).
const IMG_TIMEOUT_MS = 8000;
// Activation waits for the first image, but not indefinitely: on a dead network
// the screensaver must still appear (clock over the plain ground).
const FIRST_IMG_WAIT_MS = 6000;

// preload decodes an image off-screen and reports whether it is ready to paint.
// This is what makes the swap seamless: the browser only ever gets a URL it has
// already fully decoded, so a heavy poster or a slow link can no longer paint a
// black square and fill in gradually.
function preload(url: string, done: (ok: boolean) => void): void {
  if (!url) {
    done(false);
    return;
  }
  const img = new Image();
  let settled = false;
  const finish = function (ok: boolean) {
    if (settled) return;
    settled = true;
    img.onload = null;
    img.onerror = null;
    done(ok);
  };
  img.onload = function () {
    finish(true);
  };
  img.onerror = function () {
    finish(false);
  };
  window.setTimeout(function () {
    finish(false);
  }, IMG_TIMEOUT_MS);
  img.src = url;
}

function pad2(n: number): string {
  return n < 10 ? '0' + n : String(n);
}

// ---- idle scheduling -----------------------------------------------------

function schedule(): void {
  if (idleTimer) {
    window.clearTimeout(idleTimer);
    idleTimer = 0;
  }
  const min = getScreensaverMin();
  if (!min || min <= 0) return; // disabled
  idleTimer = window.setTimeout(maybeShow, min * 60 * 1000);
}

function anyVideoPlaying(): boolean {
  const vids = document.getElementsByTagName('video');
  for (let i = 0; i < vids.length; i++) {
    const v = vids[i] as HTMLVideoElement;
    if (!v.paused && !v.ended && v.readyState > 2) return true;
  }
  return false;
}

function maybeShow(): void {
  if (anyVideoPlaying()) {
    schedule(); // still busy — re-arm, don't cover playing video
    return;
  }
  // Bring up the screensaver only once its FIRST backdrop is decoded, so it
  // never appears as a black frame that fills in. A dead network still gets a
  // screensaver (clock over the plain ground) after FIRST_IMG_WAIT_MS.
  const my = gen;
  let shown = false;
  const openWith = function (first: Backdrop | null) {
    if (shown || my !== gen || overlay) return;
    shown = true;
    show(first);
  };
  window.setTimeout(function () {
    openWith(null);
  }, FIRST_IMG_WAIT_MS);

  ensureArt(function () {
    if (my !== gen) return;
    if (!backdrops.length) {
      openWith(null);
      return;
    }
    bgIndex = 0;
    preload(backdrops[0].url, function (ok) {
      if (my !== gen) return;
      openWith(ok ? backdrops[0] : null);
    });
  });
}

// ---- overlay -------------------------------------------------------------

function buildOverlay(): HTMLElement {
  const root = el('div', 'screensaver');
  bgA = el('div', 'screensaver__bg is-on');
  bgB = el('div', 'screensaver__bg');
  root.appendChild(bgA);
  root.appendChild(bgB);
  root.appendChild(el('div', 'screensaver__scrim'));

  const info = el('div', 'screensaver__info');
  // Weather sits ABOVE the clock, same typographic family, no fill — a hairline
  // frame only, so the backdrop stays visible through it.
  weatherEl = el('div', 'screensaver__wx');
  clockEl = el('div', 'screensaver__clock', '');
  dateEl = el('div', 'screensaver__date', '');
  info.appendChild(weatherEl);
  info.appendChild(clockEl);
  info.appendChild(dateEl);
  root.appendChild(info);

  labelEl = el('div', 'screensaver__label', '');
  root.appendChild(labelEl);
  return root;
}

// Weekday/month names per UI language. Intl is deliberately NOT used: the
// project bans it (unreliable on old Tizen/webOS, see core/i18n.ts), and
// toLocaleDateString also returned the WRONG GRAMMATICAL CASE and the wrong
// language ("Суботу" while the UI was Russian) because it follows the device
// locale, not ours. Nominative case, as a standalone date label should read.
const WEEKDAYS: { [lang: string]: string[] } = {
  uk: ['Неділя', 'Понеділок', 'Вівторок', 'Середа', 'Четвер', "П'ятниця", 'Субота'],
  ru: ['Воскресенье', 'Понедельник', 'Вторник', 'Среда', 'Четверг', 'Пятница', 'Суббота'],
  en: ['Sunday', 'Monday', 'Tuesday', 'Wednesday', 'Thursday', 'Friday', 'Saturday'],
};

// Genitive month forms: they follow a day number ("22 серпня"), which is how
// the date is rendered below. English keeps its plain names.
const MONTHS: { [lang: string]: string[] } = {
  uk: ['січня', 'лютого', 'березня', 'квітня', 'травня', 'червня', 'липня', 'серпня', 'вересня', 'жовтня', 'листопада', 'грудня'],
  ru: ['января', 'февраля', 'марта', 'апреля', 'мая', 'июня', 'июля', 'августа', 'сентября', 'октября', 'ноября', 'декабря'],
  en: ['January', 'February', 'March', 'April', 'May', 'June', 'July', 'August', 'September', 'October', 'November', 'December'],
};

// WMO weather codes grouped into the few states worth naming on a 10-foot
// screen. Text labels (no emoji) — emoji rendering on old Tizen webviews is
// unreliable, and plain type matches the clock/date styling.
const WX: { [lang: string]: { [k: string]: string } } = {
  uk: { clear: 'Ясно', cloud: 'Мінлива хмарність', overcast: 'Хмарно', fog: 'Туман', drizzle: 'Мряка', rain: 'Дощ', snow: 'Сніг', storm: 'Гроза' },
  ru: { clear: 'Ясно', cloud: 'Переменная облачность', overcast: 'Облачно', fog: 'Туман', drizzle: 'Морось', rain: 'Дождь', snow: 'Снег', storm: 'Гроза' },
  en: { clear: 'Clear', cloud: 'Partly cloudy', overcast: 'Overcast', fog: 'Fog', drizzle: 'Drizzle', rain: 'Rain', snow: 'Snow', storm: 'Thunderstorm' },
};

function wxKey(code: number): string {
  if (code === 0 || code === 1) return 'clear';
  if (code === 2) return 'cloud';
  if (code === 3) return 'overcast';
  if (code === 45 || code === 48) return 'fog';
  if (code >= 51 && code <= 57) return 'drizzle';
  if (code >= 61 && code <= 67) return 'rain';
  if (code >= 71 && code <= 77) return 'snow';
  if (code >= 80 && code <= 82) return 'rain';
  if (code >= 85 && code <= 86) return 'snow';
  if (code >= 95) return 'storm';
  return 'cloud';
}

function wxLabel(code: number): string {
  const lang = getLang();
  const dict = WX[lang] || WX.uk;
  return dict[wxKey(code)] || '';
}

function deg(n: number): string {
  const r = Math.round(n);
  return (r > 0 ? '+' : '') + r + '°';
}

// Short weekday for the week row (nominative, own table — no Intl).
const WD_SHORT: { [lang: string]: string[] } = {
  uk: ['Нд', 'Пн', 'Вт', 'Ср', 'Чт', 'Пт', 'Сб'],
  ru: ['Вс', 'Пн', 'Вт', 'Ср', 'Чт', 'Пт', 'Сб'],
  en: ['Sun', 'Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat'],
};

function shortWeekday(iso: string): string {
  // "2026-08-22" → parse as LOCAL noon: a bare Date(iso) is parsed as UTC and
  // can land on the previous day for negative offsets.
  const p = iso.split('-');
  const d = new Date(parseInt(p[0], 10), parseInt(p[1], 10) - 1, parseInt(p[2], 10), 12, 0, 0);
  const tbl = WD_SHORT[getLang()] || WD_SHORT.uk;
  return tbl[d.getDay()] || '';
}

function dateLabel(d: Date): string {
  const lang = getLang();
  const wd = (WEEKDAYS[lang] || WEEKDAYS.uk)[d.getDay()] || '';
  const mo = (MONTHS[lang] || MONTHS.uk)[d.getMonth()] || '';
  if (lang === 'en') return wd + ', ' + mo + ' ' + d.getDate();
  return wd + ', ' + d.getDate() + ' ' + mo;
}

function tickClock(): void {
  const d = new Date();
  if (clockEl) clockEl.textContent = pad2(d.getHours()) + ':' + pad2(d.getMinutes());
  if (dateEl) dateEl.textContent = dateLabel(d);
}

function setBackdrop(b: Backdrop): void {
  const next = showingA ? bgB : bgA;
  const cur = showingA ? bgA : bgB;
  if (!next || !cur) return;
  next.style.backgroundImage = 'url("' + b.url + '")';
  if (labelEl) {
    let txt = b.title || '';
    if (txt && b.year) txt += ' · ' + b.year;
    labelEl.textContent = txt;
  }
  // cross-fade: reveal next, hide current
  next.className = 'screensaver__bg is-on';
  cur.className = 'screensaver__bg';
  showingA = !showingA;
}

// rotate advances to the next backdrop, but only PAINTS one that has finished
// decoding. A failed/slow image is skipped (bounded tries) so the rotation
// keeps working instead of showing a half-loaded frame or getting stuck.
let rotating = false;
function rotate(): void {
  if (!overlay || backdrops.length < 2 || rotating) return;
  rotating = true;
  const my = gen;
  let tries = 0;

  const attempt = function () {
    if (!overlay || my !== gen || tries >= Math.min(backdrops.length, 4)) {
      rotating = false;
      return;
    }
    tries++;
    bgIndex = (bgIndex + 1) % backdrops.length;
    const item = backdrops[bgIndex];
    preload(item.url, function (ok) {
      if (!overlay || my !== gen) {
        rotating = false;
        return;
      }
      if (!ok) {
        attempt(); // that one is broken/slow — try the next
        return;
      }
      setBackdrop(item);
      rotating = false;
    });
  };
  attempt();
}

// ensureArt guarantees the backdrop list is populated, then calls back. The
// list is fetched once per session; a failure leaves it empty (clock-only).
function ensureArt(done: () => void): void {
  if (fetchedArt) {
    done();
    return;
  }
  fetchedArt = true;
  loadArt(done);
}

function loadArt(done: () => void): void {
  // Random slice of the WHOLE catalog (server picks random /discover pages),
  // not the home shelves — the screensaver should feel like wallpaper, not like
  // a mirror of the last screen you were on.
  getBackdrops(60).then(
    function (res) {
      backdrops = res && res.backdrops ? res.backdrops : [];
      bgIndex = 0;
      done();
    },
    function () {
      /* no art — clock/date over the plain dark ground still works */
      done();
    }
  );
}

// show mounts the overlay. firstURL is an ALREADY-DECODED backdrop (empty when
// art is unavailable), so the screensaver never appears mid-load.
// fetchWeather refreshes the cached forecast. Silent on failure: the strip is
// simply absent, and the next tick tries again.
function fetchWeather(): void {
  getWeather().then(
    function (res) {
      if (res && res.available && res.forecast) {
        wxData = res.forecast;
        renderWeather(); // if the screensaver is already up, fill it in now
      }
    },
    function () {
      /* keep whatever we had; retried on the next refresh */
    }
  );
}

// renderWeather paints the cached forecast into the strip. No fetch, no guards:
// callable any time, no-op when there is nothing to show or no overlay.
function renderWeather(): void {
  if (!weatherEl || !wxData) return;
  const f = wxData;
  const wx = weatherEl;
  empty(wx);

  const today = el('div', 'screensaver__wx-now');
  today.appendChild(el('span', 'screensaver__wx-temp', deg(f.now)));
  today.appendChild(el('span', 'screensaver__wx-desc', wxLabel(f.code)));
  if (f.place) today.appendChild(el('span', 'screensaver__wx-place', f.place));
  wx.appendChild(today);

  const week = el('div', 'screensaver__wx-week');
  const days: WeatherDay[] = f.days || [];
  for (let i = 0; i < days.length && i < 7; i++) {
    const d = days[i];
    const cell = el('div', 'screensaver__wx-day');
    cell.appendChild(el('div', 'screensaver__wx-dow', i === 0 ? '' : shortWeekday(d.date)));
    cell.appendChild(el('div', 'screensaver__wx-hi', deg(d.max)));
    cell.appendChild(el('div', 'screensaver__wx-lo', deg(d.min)));
    week.appendChild(cell);
  }
  wx.appendChild(week);
}

function show(first: Backdrop | null): void {
  if (overlay) return;
  overlay = buildOverlay();
  document.body.appendChild(overlay);
  tickClock();
  clockTimer = window.setInterval(tickClock, 1000);
  if (first) setBackdrop(first);
  renderWeather(); // from the cached forecast — no network on the show path
  rotTimer = window.setInterval(rotate, ROTATE_MS);
}

function hide(): void {
  gen++; // abandon any in-flight preload/activation
  if (!overlay) return;
  if (clockTimer) {
    window.clearInterval(clockTimer);
    clockTimer = 0;
  }
  if (rotTimer) {
    window.clearInterval(rotTimer);
    rotTimer = 0;
  }
  if (overlay.parentNode) overlay.parentNode.removeChild(overlay);
  overlay = null;
  bgA = bgB = clockEl = dateEl = weatherEl = labelEl = null;
}

// Timestamp of the last dismiss. Activation happens on the UP half of a press
// (controller.ts fires enter() on keyup, pointer.ts on click), so swallowing
// only the DOWN half still let the wake press start a video / open a title.
// While this stamp is fresh, the trailing keyup/click/touchend is eaten too.
let dismissedAt = 0;
const DISMISS_SWALLOW_MS = 150;

function onActivity(e?: Event): void {
  gen++; // cancels a pending activation whose first image is still decoding
  if (overlay) {
    hide();
    dismissedAt = new Date().getTime();
    // Swallow the DOWN half; the UP half is eaten by swallowTrailing below.
    if (e && e.stopPropagation) e.stopPropagation();
    if (e && e.preventDefault && (e.type === 'keydown' || e.type === 'mousedown' || e.type === 'touchstart')) {
      e.preventDefault();
    }
  }
  schedule();
}

function swallowTrailing(e: Event): void {
  if (!dismissedAt) return;
  if (new Date().getTime() - dismissedAt > DISMISS_SWALLOW_MS) {
    dismissedAt = 0;
    return;
  }
  dismissedAt = 0;
  if (e.stopPropagation) e.stopPropagation();
  if (e.preventDefault) e.preventDefault();
}

export function init(): void {
  // Trailing-half swallowers must be registered BEFORE the activity listeners
  // so a wake press never reaches the app's enter()/click handlers.
  window.addEventListener('keyup', swallowTrailing, true);
  window.addEventListener('click', swallowTrailing, true);
  window.addEventListener('touchend', swallowTrailing, true);
  // Forecast in the background: ready before the first activation, refreshed
  // slowly (the server caches it for 30 min anyway).
  // First fetch after the boot traffic settles; the strip is needed minutes
  // later anyway (idle timeout), and it must not race home for sockets.
  window.setTimeout(fetchWeather, 20000);
  if (!wxTimer) wxTimer = window.setInterval(fetchWeather, WX_REFRESH_MS);
  window.addEventListener('keydown', onActivity, true);
  window.addEventListener('mousemove', onActivity, true);
  window.addEventListener('mousedown', onActivity, true);
  window.addEventListener('touchstart', onActivity, true);
  window.addEventListener('wheel', onActivity, true);
  schedule();
}

// Re-arm after the timeout setting changes (settings screen calls this).
export function reschedule(): void {
  schedule();
}

