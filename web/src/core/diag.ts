// Diagnostics channel (Налаштування → «Режим діагностики», device-local).
//
// A TV has no devtools and its toasts may even be off-screen (an Android TV
// WebView reported a viewport taller than the panel). When the switch is on,
// the client POSTs probes to /api/v1/diag and the server writes them to its
// log (`msg=diag`), readable with kubectl. Every future "what does this device
// really send/see" question should hang a report() off this switch instead of
// inventing a new flag.
//
// ES5 target: no async/await, no Object.assign.

import { post } from './api';
import { isDebugMode } from './settings';

let seq = 0;
let lastAt = 0;
const MIN_GAP_MS = 40; // key repeat at 100ms — never flood the server

export function isDebug(): boolean {
  return isDebugMode();
}

// Fire-and-forget. `data` must be JSON-serialisable and small.
export function report(kind: string, data?: { [k: string]: unknown }): void {
  if (!isDebugMode()) return;
  const now = Date.now();
  if (now - lastAt < MIN_GAP_MS) return;
  lastAt = now;
  seq++;
  try {
    post('/diag', { kind: kind, seq: seq, host: window.location.host, data: data || {} }).then(
      function () {},
      function () {
        /* offline / not logged in — diagnostics are best-effort */
      }
    );
  } catch (e) {
    /* ignore */
  }
}

export function viewportInfo(): { [k: string]: unknown } {
  const w = window as unknown as { devicePixelRatio?: number; visualViewport?: { width: number; height: number } };
  const de = document.documentElement;
  return {
    inner: window.innerWidth + 'x' + window.innerHeight,
    outer: window.outerWidth + 'x' + window.outerHeight,
    screen: screen.width + 'x' + screen.height,
    avail: screen.availWidth + 'x' + screen.availHeight,
    doc: de.clientWidth + 'x' + de.clientHeight,
    visual: w.visualViewport ? Math.round(w.visualViewport.width) + 'x' + Math.round(w.visualViewport.height) : null,
    dpr: w.devicePixelRatio || 1,
    rootFont: de.style.fontSize,
    touch: 'ontouchstart' in window,
    maxTouchPoints: (navigator as unknown as { maxTouchPoints?: number }).maxTouchPoints,
    pointerEvents: typeof (window as unknown as { PointerEvent?: unknown }).PointerEvent !== 'undefined',
    htmlClass: de.className,
    ua: navigator.userAgent,
  };
}

// Global error/rejection hooks — installed once at boot; they only send when
// the switch is on (checked per event, so toggling later works).
let hooked = false;
export function installGlobalHooks(): void {
  if (hooked) return;
  hooked = true;
  window.addEventListener('error', function (e: ErrorEvent) {
    report('error', { message: String(e.message), source: String(e.filename || ''), line: e.lineno, col: e.colno });
  });
  const w = window as unknown as { addEventListener: (t: string, fn: (e: unknown) => void) => void };
  w.addEventListener('unhandledrejection', function (e: unknown) {
    const r = e as { reason?: unknown };
    report('unhandledrejection', { reason: String(r && r.reason) });
  });
}
