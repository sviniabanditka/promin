// Pointer / touch input layer.
//
// Promin's interaction model is remote-first: the Navigator (geometry) + the
// Controller (modes) drive focus and activation, and nothing here replaces
// that. This module is three thin *event sources* (mouse, touch, wheel) that
// feed the SAME entry points the remote already uses:
//   - hover  → Navigator.focus(el)          (the one writer of _focus, so remote
//                                             arrows resume from the right place)
//   - click  → Controller.enter() / trigger(el,'hover:enter')
//   - wheel/drag → Scroll.scrollBy(...)      (added in P2)
//
// All listeners are delegated on `window` (mirrors the single global keydown in
// controller.ts). The three input kinds coexist because each only fires on its
// own hardware — a TV remote emits neither `mouseover` nor `wheel`, so the
// shipping TV path is byte-for-byte untouched.
//
// P1 scope: hover, click, backdrop-close, input-mode switching. Wheel/touch
// gestures land in P2 (see docs/frontend.md).

import { Navigator } from './nav';
import Controller, { trigger } from './controller';
import { Scroll, setHoverNoScroll } from './scroll';
import { report } from './diag';

// Synthetic mouse events fire ~300ms after a touch; this window makes the mouse
// handlers ignore them so a tap doesn't also hover/mode-flip. Set on touchstart
// in P2; harmless (always 0) until then.
let touchSuppressUntil = 0;
// A completed gesture (drag / swipe-back / catch-on-touch) sets this so the
// trailing click is eaten instead of activating a card. Set in P2.
let suppressClick = false;
// True when the current touch started by catching a coasting scroll — its
// trailing tap must not also activate a card (PTR-1).
let tCaught = false;


function now(): number {
  return new Date().getTime();
}

function setInputMode(mode: 'point' | 'touch'): void {
  const root = document.documentElement;
  if (root.getAttribute('data-input') !== mode) root.setAttribute('data-input', mode);
}

function closestSelector(node: EventTarget | null): HTMLElement | null {
  let el = node as HTMLElement | null;
  while (el && el.nodeType === 1) {
    if (el.classList && el.classList.contains('selector')) return el;
    el = el.parentElement;
  }
  return null;
}

function inActiveCollection(el: HTMLElement): boolean {
  const c = Navigator._collection;
  if (!c) return false;
  for (let i = 0; i < c.length; i++) {
    if (c[i] === el) return true;
  }
  return false;
}

// Hover → focus RING only (no scroll). Route through Navigator.focus so _focus
// is written (remote arrows resume from here) but arm hoverNoScroll around it so
// the synchronous focus dispatch skips the scroll — otherwise every mouseover
// scrolls the lane/page to the card under the cursor ("pages while moving").
// Nearest enclosing scroll-body — the collection root for a lane/grid. Used to
// RETARGET the active collection when hovering a selector outside it, so hover
// focuses tiles in ANY lane/row, not only the currently-active one (a card is
// unfocusable while it's not in Navigator._collection). Returns null for
// selectors not inside a scroll body (menu rail, modals) — those keep the
// old "only when active" behaviour rather than yanking focus across regions.
function nearestScrollBody(el: HTMLElement): HTMLElement | null {
  let node: HTMLElement | null = el.parentElement;
  while (node) {
    if (node.classList && node.classList.contains('scroll__body')) return node;
    node = node.parentElement;
  }
  return null;
}

function onHover(e: Event): void {
  if (now() < touchSuppressUntil) return;
  if (now() < wheelSettleUntil) return; // cards sliding under a parked cursor mid-wheel must not hop the ring
  const s = closestSelector(e.target);
  if (!s) return;
  if (s === Navigator.getFocusedElement()) return; // child→card dedupe
  setHoverNoScroll(true);
  try {
    if (!inActiveCollection(s)) {
      // Hovering a tile in a different lane/row: repoint the collection to that
      // element's own scroll body so Navigator.focus can accept it. Its
      // hover:focus side-effects (home's active-lane index, backdrop) then fire
      // through Controller.focus. Skip when there's no scroll body (rail/modal).
      const body = nearestScrollBody(s);
      if (!body) return;
      Controller.collectionSet(body);
    }
    Navigator.focus(s);
  } finally {
    setHoverNoScroll(false); // a throwing handler must never leave it armed → would kill remote scroll
  }
}

function onMouseMove(): void {
  if (now() < touchSuppressUntil) return;
  setInputMode('point');
}

// Whether the element has its own hover:enter handler registered (controller.ts
// per-element registry). Lets a click activate the ELEMENT rather than routing
// through the active Controller mode, which may belong to a different region.
function hasOwnEnter(el: HTMLElement): boolean {
  const e = el as unknown as { _promin_events?: { [k: string]: unknown[] } };
  const list = e._promin_events && e._promin_events['hover:enter'];
  return !!(list && list.length);
}

function onClick(e: Event): void {
  if (suppressClick || now() < suppressClickUntil) {
    suppressClick = false;
    if (e.preventDefault) e.preventDefault();
    if (e.stopPropagation) e.stopPropagation();
    return;
  }
  activate(e.target as HTMLElement | null);
}

// What a click (native, or synthesized from a pointer tap) does to a target.
function activate(target: HTMLElement | null): void {
  const s = closestSelector(target);
  if (!s) {
    // Tap/click on a modal backdrop dismisses it (parity with remote Back).
    // Only when the click landed ON the overlay root itself (not a child), and
    // only for real dismissable overlays: player__popup is a panel with its own
    // back; sources-overlay is a spinner with no mode (dismissing would kill an
    // in-flight resolve); trailer-overlay is covered by a cross-origin iframe.
    if (target && target.classList) {
      const cl = target.classList;
      // Every dismissable overlay dismisses on an outside click (uniform policy).
      // trailer-overlay is mostly covered by a cross-origin iframe, but its
      // margins are clickable, so include it. sources-overlay is EXCLUDED on
      // purpose: it's a progress spinner with no controller mode of its own —
      // "dismissing" it would abandon an in-flight resolve with no way back.
      if (
        cl.contains('modal-overlay') ||
        cl.contains('settings-modal') ||
        cl.contains('action-sheet-overlay') ||
        cl.contains('trailer-overlay')
      ) {
        Controller.back();
      }
    }
    return;
  }
  if (inActiveCollection(s)) {
    Navigator.focus(s);
    // Prefer the element's OWN handler: hover retargets the collection across
    // regions without switching the Controller mode, so Controller.enter()
    // could dispatch to a stale mode (e.g. clicking a search-history row fired
    // the keyboard's enter → reopened the IME instead of applying the query).
    // Elements with no own handler (e.g. the search bar itself) still go
    // through the mode, which is where their activation lives.
    if (hasOwnEnter(s)) trigger(s, 'hover:enter');
    else Controller.enter();
  } else {
    // Cross-column click: activate the element directly via its per-element
    // registry without moving the focus ring into a non-active collection.
    trigger(s, 'hover:enter');
  }
}

// ---- scrolling: wheel + touch drag + momentum (P2) --------------------

// Walk up from a node collecting the nearest horizontal lane and the nearest
// vertical page scroll — a horizontal lane lives inside the vertical Main, so a
// drag must pick the axis-matching one.
function resolveScrolls(node: EventTarget | null): { lane: Scroll | null; page: Scroll | null } {
  let el = node as HTMLElement | null;
  let lane: Scroll | null = null;
  let page: Scroll | null = null;
  while (el && el.nodeType === 1) {
    if (el.classList && el.classList.contains('scroll__body')) {
      const sc = Scroll.forBody(el);
      if (sc) {
        if (sc.horizontal) {
          if (!lane) lane = sc;
        } else if (!page) page = sc;
      }
    }
    el = el.parentElement;
  }
  return { lane: lane, page: page };
}

// A wheel "gesture" locks to one axis on its first event and holds it until a
// >WHEEL_SETTLE_MS gap. The same window freezes hover (cards slide under the
// cursor during the 0.3s glide) — see onHover.
const WHEEL_SETTLE_MS = 400;
let wheelSettleUntil = 0;
let wheelAxis: '' | 'x' | 'y' = '';
let wheelSettleTimer = 0;
let wheelScroll: Scroll | null = null;

function onWheel(e: WheelEvent): void {
  const r = resolveScrolls(e.target);
  if (now() >= wheelSettleUntil) wheelAxis = ''; // new gesture → re-decide axis
  if (!wheelAxis) {
    // Horizontal only when the input is genuinely horizontal (trackpad deltaX,
    // or shift+wheel); a plain vertical wheel never slides a lane sideways.
    wheelAxis = e.shiftKey || (e.deltaMode === 0 && Math.abs(e.deltaX) > Math.abs(e.deltaY)) ? 'x' : 'y';
  }
  const horizontal = wheelAxis === 'x';
  // Each axis prefers its own scroll, falling back to the other when only one
  // exists (vertical wheel over a horizontal-only lane still scrolls it).
  const sc = horizontal ? r.lane || r.page : r.page || r.lane;
  if (!sc) return; // non-scroll area: leave the native wheel alone, don't hold an axis
  if (e.preventDefault) e.preventDefault();
  wheelSettleUntil = now() + WHEEL_SETTLE_MS;
  const raw = horizontal ? e.deltaX || e.deltaY : e.deltaY;
  // deltaMode 0 = pixels (trackpad/Tizen); 1/2 = lines/pages → a fixed notch.
  const px = e.deltaMode === 0 ? raw : raw > 0 ? 150 : -150;
  // Immediate (no 0.3s glide per notch — a fast wheel/trackpad was always a
  // few hundred ms behind the hand). The settle hook (lazy-append of the next
  // batch) fires once the gesture pauses.
  if (wheelScroll && wheelScroll !== sc) wheelScroll.body().classList.remove('notransition');
  wheelScroll = sc;
  sc.scrollBy(px, false);
  window.clearTimeout(wheelSettleTimer);
  wheelSettleTimer = window.setTimeout(function () {
    if (!wheelScroll) return;
    wheelScroll.body().classList.remove('notransition');
    wheelScroll.fireAnimateEnd();
    wheelScroll = null;
  }, WHEEL_SETTLE_MS);
}

// ---- touch ----
const AXIS_LOCK_PX = 10; // movement before we commit to an axis
const TAP_SLOP_PX = 8; // movement under this = a tap, not a drag
const SWIPE_BACK_MIN = 60; // rightward drag (no lane) that triggers Back
const EDGE_PX = 24; // left-edge zone that always arms swipe-back
const VEL_MIN = 0.25; // px/ms below which we don't fling
const FLING_K = 180; // fling distance = velocity * this
const TOUCH_SUPPRESS_MS = 700; // block synthetic mouse after a touch

let tStartX = 0;
let tStartY = 0;
let tLastPos = 0; // last position on the locked axis
let tLastMovePos = 0; // for velocity sampling
let tLastMoveTime = 0;
let tVelocity = 0;
let tAxis: '' | 'x' | 'y' = '';
let tActive: Scroll | null = null; // the scroll being dragged (null = swipe-back candidate)
let tLane: Scroll | null = null;
let tPage: Scroll | null = null;
let tDragging = false;

// ---- gesture core (point-based; fed by touch OR pointer events) ----------
// iOS WKWebView inside the MSX app delivers pointerdown/up but NO touch events
// and NO click (diag 2026-09-06). So the gesture logic takes plain points, and
// two thin adapters feed it: touch events (TV webviews, Android) and pointer
// events (that iOS case). Once a touch-type pointer event is seen, the touch
// adapter goes quiet so a device sending both does not double-drive.
let pointerDriven = false;
// Time-based click suppression (a one-shot flag would eat the NEXT real click
// when the synthesized activation is followed by no native click at all).
let suppressClickUntil = 0;

function gestureStart(x: number, y: number, target: EventTarget | null): void {
  setInputMode('touch');
  touchSuppressUntil = now() + TOUCH_SUPPRESS_MS;
  tStartX = x;
  tStartY = y;
  tAxis = '';
  tActive = null;
  tDragging = true;
  tVelocity = 0;
  const r = resolveScrolls(target);
  tLane = r.lane;
  tPage = r.page;
  // Catch-on-touch: tapping a coasting list halts it (and the tap is eaten so it
  // doesn't also activate a card).
  tCaught = false;
  if (tLane && tLane.animating) { tLane.stop(); tCaught = true; }
  if (tPage && tPage.animating) { tPage.stop(); tCaught = true; }
  if (tCaught) suppressClick = true;
}

// Returns true when the move was consumed as a drag (caller preventDefaults).
function gestureMove(x: number, y: number): boolean {
  if (!tDragging) return false;
  const dx = x - tStartX;
  const dy = y - tStartY;
  if (!tAxis) {
    if (Math.abs(dx) < AXIS_LOCK_PX && Math.abs(dy) < AXIS_LOCK_PX) return false;
    if (Math.abs(dx) > Math.abs(dy)) {
      tAxis = 'x';
      // Horizontal: drag the lane if there is one; otherwise it's a swipe-back
      // candidate (tActive stays null).
      tActive = tLane;
    } else {
      tAxis = 'y';
      tActive = tPage;
    }
    if (tActive) {
      tActive.body().classList.add('notransition');
      tLastPos = tAxis === 'x' ? x : y;
    }
    tLastMovePos = tAxis === 'x' ? x : y;
    tLastMoveTime = now();
    suppressClick = true; // any committed drag eats the trailing click
  }
  const pos = tAxis === 'x' ? x : y;
  if (tActive) {
    tActive.scrollBy(tLastPos - pos, false); // finger-follow (no transition)
    tLastPos = pos;
  }
  // velocity sample
  const dt = now() - tLastMoveTime;
  if (dt > 0) {
    tVelocity = (tLastMovePos - pos) / dt; // px/ms on the locked axis
    tLastMovePos = pos;
    tLastMoveTime = now();
  }
  return true;
}

// Returns true for a pure tap the caller may turn into an activation.
function gestureEnd(x: number): boolean {
  // Restamp: a drag longer than the touchstart suppress window would otherwise
  // let the post-touchend synthetic-mouse burst steal focus.
  touchSuppressUntil = now() + TOUCH_SUPPRESS_MS;
  if (!tDragging) return false;
  tDragging = false;
  const movedX = x - tStartX;

  // Swipe-back: horizontal drag with no lane to scroll (or starting at the left
  // edge), moved right past the threshold.
  if (tAxis === 'x' && !tActive && (movedX > SWIPE_BACK_MIN || (tStartX <= EDGE_PX && movedX > EDGE_PX))) {
    suppressClick = true;
    Controller.back();
    return false;
  }

  if (tActive) {
    tActive.body().classList.remove('notransition');
    if (Math.abs(tVelocity) > VEL_MIN) {
      tActive.scrollBy(tVelocity * FLING_K, true); // one animated fling (CSS glide)
    } else {
      // Finger-follow left no pending transition → fire the settle hook manually
      // so the vertical Main lazy-appends its next batch.
      tActive.fireAnimateEnd();
    }
    return false;
  }

  // A pure tap (no axis lock, negligible movement) → let the click through —
  // UNLESS this tap was a catch-on-touch (it halted a coasting list; must not
  // also activate the card under the finger).
  if (!tAxis && Math.abs(movedX) < TAP_SLOP_PX && !tCaught) {
    suppressClick = false;
    return true;
  }
  return false;
}

function gestureCancel(): void {
  touchSuppressUntil = now() + TOUCH_SUPPRESS_MS;
  tDragging = false;
  if (tActive) tActive.body().classList.remove('notransition');
}

// ---- touch adapter ----
function onTouchStart(e: TouchEvent): void {
  if (pointerDriven || e.touches.length !== 1) return;
  gestureStart(e.touches[0].clientX, e.touches[0].clientY, e.target);
}
function onTouchMove(e: TouchEvent): void {
  if (pointerDriven || e.touches.length !== 1) return;
  if (gestureMove(e.touches[0].clientX, e.touches[0].clientY) && e.preventDefault) e.preventDefault();
}
function onTouchEnd(e: TouchEvent): void {
  if (pointerDriven) return;
  const t = e.changedTouches[0];
  gestureEnd(t ? t.clientX : tStartX); // the native click that follows does the activation
}

// ---- pointer adapter (touch/pen pointers only; the mouse keeps its own path) ----
let pDownTarget: EventTarget | null = null;
function isTouchPointer(e: PointerEvent): boolean {
  return e.pointerType === 'touch' || e.pointerType === 'pen';
}
function onPointerDown(e: PointerEvent): void {
  if (!isTouchPointer(e) || !e.isPrimary) return;
  pointerDriven = true;
  pDownTarget = e.target;
  gestureStart(e.clientX, e.clientY, e.target);
}
function onPointerMove(e: PointerEvent): void {
  if (!isTouchPointer(e) || !e.isPrimary || !tDragging) return;
  if (gestureMove(e.clientX, e.clientY) && e.preventDefault) e.preventDefault();
}
function onPointerUp(e: PointerEvent): void {
  if (!isTouchPointer(e) || !e.isPrimary) return;
  const tap = gestureEnd(e.clientX);
  if (!tap) return;
  // Activate right away: this webview may never send the click. If it does,
  // the time window below eats it so the tap does not fire twice.
  suppressClickUntil = now() + 700;
  activate((pDownTarget || e.target) as HTMLElement | null);
}
function onPointerCancel(e: PointerEvent): void {
  if (!isTouchPointer(e)) return;
  gestureCancel();
}

function supportsPassive(): boolean {
  let ok = false;
  try {
    const opts = Object.defineProperty({}, 'passive', {
      get: function () {
        ok = true;
        return true;
      },
    });
    window.addEventListener('promin_probe', null as unknown as EventListener, opts);
    window.removeEventListener('promin_probe', null as unknown as EventListener, opts);
  } catch (err) {
    ok = false;
  }
  return ok;
}

// Diagnostics (settings → режим діагностики): the first input events of the
// session, so a device where taps do nothing (phone inside the MSX app) shows
// what it actually delivers — touch, pointer, mouse or nothing at all.
let probeLeft = 25;
function probeInput(e: Event): void {
  if (probeLeft <= 0) return;
  probeLeft--;
  const t = e.target as HTMLElement | null;
  const te = e as TouchEvent;
  const pe = e as PointerEvent;
  report('input', {
    type: e.type,
    target: t && t.nodeType === 1 ? t.tagName + (t.className ? '.' + String(t.className).split(' ')[0] : '') : String(t),
    touches: te.touches ? te.touches.length : undefined,
    pointerType: pe.pointerType,
    x: pe.clientX,
    y: pe.clientY,
  });
}

export function initPointer(): void {
  const probes = ['touchstart', 'touchend', 'pointerdown', 'pointerup', 'mousedown', 'mouseup', 'click'];
  for (let i = 0; i < probes.length; i++) window.addEventListener(probes[i], probeInput, true);
  window.addEventListener('mouseover', onHover);
  window.addEventListener('mousemove', onMouseMove);
  window.addEventListener('click', onClick, true); // capture so suppressClick can eat it
  // Remote / keyboard reasserts pointer (ring-visible) mode.
  window.addEventListener('keydown', function () {
    setInputMode('point');
  });

  // Non-passive so preventDefault works during wheel/drag. Fall back to a
  // boolean `false` (also non-passive) on engines without the options object.
  const nonPassive = supportsPassive() ? ({ passive: false } as AddEventListenerOptions) : false;
  window.addEventListener('wheel', onWheel as EventListener, nonPassive);
  window.addEventListener('touchstart', onTouchStart as EventListener, nonPassive);
  window.addEventListener('touchmove', onTouchMove as EventListener, nonPassive);
  window.addEventListener('touchend', onTouchEnd as EventListener);
  window.addEventListener('touchcancel', gestureCancel);
  if (typeof (window as unknown as { PointerEvent?: unknown }).PointerEvent !== 'undefined') {
    window.addEventListener('pointerdown', onPointerDown as EventListener, nonPassive);
    window.addEventListener('pointermove', onPointerMove as EventListener, nonPassive);
    window.addEventListener('pointerup', onPointerUp as EventListener);
    window.addEventListener('pointercancel', onPointerCancel as EventListener);
  }
}
