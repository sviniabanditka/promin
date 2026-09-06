// Controller — a port of Lampa's src/core/controller.js (the "modes" layer
// that sits on top of the Navigator).
//
// A named set of handlers (a "controller": menu / content / items_line …)
// is active at a time. Remote keys are dispatched to the active
// controller's left/right/up/down/enter/back handlers, exactly like
// keypad.js -> Controller.move()/enter()/back() upstream. The Navigator's
// 'focus' event is piped into Controller.focus() (app.js:411), which paints
// the `.focus` class on the target and fires a 'hover:focus' element event
// that lane components listen to for centering.
//
// Key repeat is throttled to one dispatch per 100ms, byte-for-byte with
// keypad.js keydownTrigger (`if (time > Date.now() - 100) return`), so
// holding a d-pad direction can't outrun the 0.3s translate transition.

import { Navigator, Direction } from './nav';
import { Scroll } from './scroll';
import { toast } from '../ui/toast';
import { report, isDebug } from './diag';

export interface ControllerCalls {
  toggle?: () => void;
  update?: () => void;
  gone?: (name: string) => void;
  left?: () => void;
  right?: () => void;
  up?: () => void;
  down?: () => void;
  enter?: () => void;
  back?: () => void;
  [name: string]: unknown;
}

// ---- element event system (Lampa uses jQuery .on/.trigger) -------------

interface EventfulElement extends HTMLElement {
  _promin_events?: { [type: string]: Array<() => void> };
}

export function on(el: HTMLElement, type: string, fn: () => void): void {
  const e = el as EventfulElement;
  if (!e._promin_events) e._promin_events = {};
  if (!e._promin_events[type]) e._promin_events[type] = [];
  e._promin_events[type].push(fn);
}

export function trigger(el: HTMLElement, type: string): void {
  const e = el as EventfulElement;
  if (e._promin_events && e._promin_events[type]) {
    const list = e._promin_events[type].slice(0);
    for (let i = 0; i < list.length; i++) {
      list[i]();
    }
  }
}

// ---- controller state --------------------------------------------------

const SELECTOR = '.selector';

let active: ControllerCalls | null = null;
let active_name = '';
const controlls: { [name: string]: ControllerCalls } = {};
let select_active: HTMLElement | false = false;

function add(name: string, calls: ControllerCalls): void {
  controlls[name] = calls;
}

// remove drops a mode registration. Registrations were permanent, so a closed
// modal's handlers (closing over its whole DOM — an episode list with stills)
// stayed reachable until the same name was registered again.
function remove(name: string): void {
  if (controlls[name] === active) return; // still active — the caller toggles away first
  delete controlls[name];
}

function run(name: string): void {
  if (active) {
    const handler = active[name];
    if (typeof handler === 'function') {
      (handler as () => void)();
    } else if (typeof handler === 'string') {
      run(handler);
    }
  }
}

function move(direction: Direction): void {
  run(direction);
}

// moveOr: the one-liner every list mode repeats — move the ring if it can,
// otherwise do the fallback (leave to the rail, switch mode...). 80+ copies of
// `if (Navigator.canmove(d)) Navigator.move(d); else …` collapsed into this,
// which is also the single place a future key-repeat acceleration would live.
function moveOr(direction: Direction, fallback?: () => void): void {
  if (Navigator.canmove(direction)) Navigator.move(direction);
  else if (fallback) fallback();
}

function enter(): void {
  if (active && active.enter) {
    run('enter');
  } else if (select_active) {
    trigger(select_active, 'hover:enter');
  }
}

function back(): void {
  run('back');
}

function toggle(name: string): void {
  if (active && active.gone) active.gone(name);

  if (controlls[name]) {
    active = controlls[name];
    active_name = name;

    if (active.toggle) active.toggle();
    if (active.update) active.update();
  }
}

function clearSelects(): void {
  if (select_active) select_active.classList.remove('focus');
  select_active = false;
}

// Called by the Navigator 'focus' event. Paints the focus class and fires
// the 'hover:focus' element event (lanes center on it, Home tints the
// backdrop from it).
function focus(target: HTMLElement): void {
  trigger(target, 'hover:focus');

  // O(1): unpaint only the previously-focused element (the sole '.focus' holder —
  // focus() is the only place that adds it), not a loop over the whole collection.
  if (select_active && select_active !== target) select_active.classList.remove('focus');
  target.classList.add('focus');

  select_active = target;

  autoScrollTo(target);
}

// Walk up from the focused element; for every enclosing `.scroll__body` that
// has a live Scroll instance, bring the element into view. This makes vertical
// (title/sources) and horizontal (lanes) scroll follow the focus everywhere,
// so screens don't each have to remember scroll.update in every hover:focus.
function autoScrollTo(target: HTMLElement): void {
  let node: HTMLElement | null = target;
  while (node) {
    if (node.classList && node.classList.contains('scroll__body')) {
      const sc = Scroll.forBody(node);
      if (sc) {
        // For a home lane the focusable card sits below its lane title. If the
        // vertical scroll aligned to the card, the title would slide up under
        // the fixed .head. Instead align to the enclosing .items-line (title +
        // strip) so the current lane's title always stays visible below .head.
        // Other vertical lists (title/sources) have no .items-line wrapper, so
        // they keep aligning to the focused element itself — unchanged.
        let child: HTMLElement | null = target;
        while (child && child.parentElement !== node) child = child.parentElement;
        // Align to the enclosing block for a home/title lane (.items-line) or the
        // title screen's info block (.full-start-new) so its title/poster stays
        // put; other vertical lists (episodes/sources) align to the focused row.
        if (
          child &&
          child.classList &&
          (child.classList.contains('items-line') || child.classList.contains('full-start-new'))
        ) {
          sc.update(child);
        } else {
          sc.update(target);
        }
      }
    }
    node = node.parentElement;
  }
}

function collectionSet(html: HTMLElement): void {
  const found = html.querySelectorAll(SELECTOR);
  const colection: HTMLElement[] = [];
  for (let i = 0; i < found.length; i++) {
    colection.push(found[i] as HTMLElement);
  }

  clearSelects();
  Navigator.setCollection(colection);
}

function collectionAppend(append: HTMLElement): void {
  Navigator.multiAdd([append]);
}

function collectionFocus(target: HTMLElement | false, html: HTMLElement): void {
  if (target && target.offsetParent === null) target = false;

  if (target) {
    Navigator.focus(target);
  } else {
    const found = html.querySelectorAll(SELECTOR);
    const colection: HTMLElement[] = [];
    for (let i = 0; i < found.length; i++) {
      const el = found[i] as HTMLElement;
      if (!el.classList.contains('hide')) colection.push(el);
    }
    if (colection.length) Navigator.focus(colection[0]);
  }
}

function enabled(): { name: string; controller: ControllerCalls | null } {
  return { name: active_name, controller: active };
}

function clear(): void {
  clearSelects();
  Navigator.setCollection([]);
}

// ---- input wiring (keypad.js) ------------------------------------------

let time = 0;
// Set when Enter went DOWN inside a native text field (IME confirm). The field
// blurs on that keydown, so by keyup the focus is already on a result — without
// this the same press would open it.
let swallowEnterUp = false;

// Remote media keys (Tizen/webOS/generic) → controller action names. Modes that
// care (the player) implement them; everywhere else they are no-ops.
const MEDIA_KEYS: { [code: number]: string } = {
  10252: 'playpause',
  415: 'play',
  19: 'pause',
  413: 'stop',
  412: 'rewind',
  417: 'forward',
  10233: 'next',
  10232: 'prev',
};

// Tizen only delivers media keys to a webview that asked for them.
function registerTizenKeys(): void {
  const tz = (window as unknown as { tizen?: { tvinputdevice?: { registerKey: (k: string) => void } } }).tizen;
  if (!tz || !tz.tvinputdevice) return;
  const names = [
    'MediaPlayPause',
    'MediaPlay',
    'MediaPause',
    'MediaStop',
    'MediaRewind',
    'MediaFastForward',
    'MediaTrackNext',
    'MediaTrackPrevious',
  ];
  for (let i = 0; i < names.length; i++) {
    try {
      tz.tvinputdevice.registerKey(names[i]);
    } catch (e) {
      /* key unsupported on this model — skip */
    }
  }
}

// Long-press OK → 'hover:long' on the focused element (cards use it for a
// one-press bookmark toggle). The following keyup must NOT also fire enter.
const LONG_MS = 600;
let enterDownAt = 0;
let longTimer = 0;
let longFired = false;

function inTextField(): Element | null {
  const ae = document.activeElement;
  if (ae && (ae.tagName === 'INPUT' || ae.tagName === 'TEXTAREA')) return ae;
  return null;
}

function isEnter(code: number): boolean {
  return code === 13 || code === 29443 || code === 117 || code === 65385;
}

function select_active_name(): string {
  const e = enabled();
  return e && e.name ? e.name : '';
}

function isBack(code: number): boolean {
  return code === 8 || code === 27 || code === 461 || code === 10009 || code === 88;
}

// Back by keyCode OR by key name. Host webviews differ: Xiaomi MiTV via MSX
// delivers the remote's Back as keyCode 145 / key "ScrollLock" (seen in the
// diag log), other Android hosts as 4 (which doubles as Lampa's legacy
// "left"), some browsers as 166 (BrowserBack) or only as e.key
// "GoBack"/"BrowserBack". A code 4 counts as Back only on Android — elsewhere
// it stays "left".
function isBackEvent(e: KeyboardEvent, code: number): boolean {
  if (isBack(code)) return true;
  const k = e.key || '';
  if (k === 'GoBack' || k === 'BrowserBack' || k === 'XF86Back' || k === 'Escape' || k === 'Backspace' || k === 'ScrollLock') return true;
  if (code === 166 || code === 10182 || code === 145) return true;
  if (code === 4) return isAndroidHost();
  return false;
}
function isAndroidHost(): boolean {
  try {
    return /android/i.test(navigator.userAgent || '');
  } catch (err) {
    return false;
  }
}



function keyCode(e: KeyboardEvent): number {
  return e.keyCode || (e as unknown as { which: number }).which;
}

function initInput(): void {
  // Pipe Navigator focus into the mode layer (app.js:411).
  Navigator.follow('focus', function (event) {
    focus(event.elem);
  });

  registerTizenKeys();

  window.addEventListener('keydown', function (e: KeyboardEvent) {
    const code = keyCode(e);
    // Diagnostics mode: every key to the server log (+ a toast, if visible).
    if (isDebug()) {
      report('key', { keyCode: code, key: e.key || '', code: e.code || '', mode: select_active_name() });
      toast('key ' + code + ' / ' + (e.key || '?'));
    }

    // A native text field (search <input>, IME) owns its keys: letters, Backspace,
    // Esc, arrows and 'x' (which isBack treats as Back!) must reach the field, not
    // the D-pad layer — otherwise one Backspace wipes the whole query and fast
    // typing is swallowed by the throttle's preventDefault. The remote's own Back
    // key still closes the field: blur → the screen's blur handler takes over.
    const field = inTextField();
    if (field) {
      if (code === 10009 || code === 461) {
        e.preventDefault();
        (field as HTMLElement).blur();
      } else if (isEnter(code)) {
        swallowEnterUp = true;
      }
      return;
    }

    if (isEnter(code)) {
      // enter fires on keyup; here we only arm the long-press timer (once —
      // held keys repeat keydown).
      if (!enterDownAt) {
        enterDownAt = Date.now();
        longTimer = window.setTimeout(function () {
          longTimer = 0;
          longFired = true;
          if (select_active) trigger(select_active, 'hover:long');
        }, LONG_MS);
      }
      return;
    }

    // 100ms throttle, verbatim from keypad.js keydownTrigger.
    if (time > Date.now() - 100) {
      if (!isEnter(code)) e.preventDefault();
      return;
    }
    time = Date.now();

    if (isBackEvent(e, code)) {
      e.preventDefault();
      back();
      return;
    }

    let dir: Direction | null = null;
    if (code === 37 || code === 4) dir = 'left';
    else if (code === 38 || code === 29460) dir = 'up';
    else if (code === 39 || code === 5) dir = 'right';
    else if (code === 40 || code === 29461) dir = 'down';

    if (dir) {
      e.preventDefault();
      move(dir);
      return;
    }

    const media = MEDIA_KEYS[code];
    if (media) {
      e.preventDefault();
      run(media);
    }
  });

  window.addEventListener('keyup', function (e: KeyboardEvent) {
    time = 0;
    const code = keyCode(e);
    if (isEnter(code)) {
      if (longTimer) window.clearTimeout(longTimer);
      longTimer = 0;
      enterDownAt = 0;
      if (longFired) {
        longFired = false; // the long-press already acted
        return;
      }
      if (swallowEnterUp) {
        swallowEnterUp = false;
        return;
      }
      if (!e.defaultPrevented) enter();
    }
  });
}

export default {
  add,
  remove,
  toggle,
  move,
  moveOr,
  enter,
  back,
  focus,
  collectionSet,
  collectionFocus,
  collectionAppend,
  enabled,
  clear,
  initInput,
};
