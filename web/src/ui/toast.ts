// Global toast, mounted on <body> so any screen can call it. Uses the
// `.toast` styles from styles.css. Transition on opacity/transform only.
//
// One node is reused for the lifetime of a toast: a new call while one is
// showing rewrites the content in place (no remove/append flash) and restarts
// the timer. `toast('text')` keeps working for every existing call site.

import { el, empty } from './dom';

export type ToastKind = 'info' | 'success' | 'warning' | 'error' | 'progress';

export interface ToastOptions {
  title?: string;
  text: string;
  icon?: string;
  kind?: ToastKind;
  /** ms; omitted = scales with text length (1.8–5 s); 0 = sticky until the next toast. */
  duration?: number;
}

let node: HTMLDivElement | null = null;
let hideTimer = 0;
let removeTimer = 0;

export function toast(message: string): void;
export function toast(opts: ToastOptions): void;
export function toast(arg: string | ToastOptions): void {
  const opts: ToastOptions = typeof arg === 'string' ? { text: arg } : arg;
  const kind: ToastKind = opts.kind || 'info';

  window.clearTimeout(hideTimer);
  window.clearTimeout(removeTimer);

  const fresh = !node || !document.body.contains(node);
  if (fresh) {
    node = el('div', 'toast');
    document.body.appendChild(node);
  }
  const n = node as HTMLDivElement;
  n.className = 'toast toast--' + kind;
  empty(n);

  if (opts.icon) n.appendChild(el('span', 'toast__icon', opts.icon));
  const body = el('div', 'toast__body');
  if (opts.title) body.appendChild(el('div', 'toast__title', opts.title));
  body.appendChild(el('div', 'toast__text', opts.text));
  n.appendChild(body);
  if (kind === 'progress') n.appendChild(el('div', 'toast__progress'));

  if (fresh) {
    // Force reflow so the transition runs from the hidden state.
    void n.offsetWidth;
  }
  n.classList.add('toast-visible');

  const duration =
    typeof opts.duration === 'number'
      ? opts.duration
      : Math.min(5000, 1800 + ((opts.title || '').length + opts.text.length) * 30);
  if (duration <= 0) return; // sticky: lives until the next toast replaces it

  hideTimer = window.setTimeout(function () {
    n.classList.remove('toast-visible');
    removeTimer = window.setTimeout(function () {
      if (n.parentNode) n.parentNode.removeChild(n);
      if (node === n) node = null;
    }, 300);
  }, duration);
}
