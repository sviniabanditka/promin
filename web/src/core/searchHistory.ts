// Recent-search history. Newest first, case-insensitive de-dup, MAX entries.
//
// Two copies: localStorage (instant, works logged-out) and the profile setting
// `search_history` (a JSON array) so every TV and the Mini App share one list.
// Local edits push to the server; a `settings_updated` from another device
// lands through applyRemote(). No ES6: plain loops, indexOf — ES5 bundle.

import { putSetting } from './api';
import { isLogged } from './auth';

const KEY = 'promin:search_history';
export const SETTING_KEY = 'search_history';
const MAX = 15;

type Listener = () => void;
const listeners: Listener[] = [];

function parse(raw: string | null): string[] {
  try {
    if (!raw) return [];
    const arr = JSON.parse(raw);
    if (Object.prototype.toString.call(arr) !== '[object Array]') return [];
    const out: string[] = [];
    for (let i = 0; i < arr.length && out.length < MAX; i++) {
      if (typeof arr[i] === 'string' && arr[i]) out.push(arr[i]);
    }
    return out;
  } catch (e) {
    return [];
  }
}

export function list(): string[] {
  try {
    return parse(window.localStorage.getItem(KEY));
  } catch (e) {
    return [];
  }
}

function saveLocal(items: string[]): string {
  const json = JSON.stringify(items.slice(0, MAX));
  try {
    window.localStorage.setItem(KEY, json);
  } catch (e) {
    /* storage unavailable/full: history just won't persist */
  }
  return json;
}

function notify(): void {
  const copy = listeners.slice(0);
  for (let i = 0; i < copy.length; i++) copy[i]();
}

// Local edit: store, tell the screen, mirror to the profile.
function save(items: string[]): void {
  const json = saveLocal(items);
  notify();
  if (isLogged()) {
    putSetting(SETTING_KEY, json).then(
      function () {},
      function () {
        /* offline: the local copy stands; next edit retries */
      }
    );
  }
}

// The profile's copy arrived (boot sync or another device edited it).
export function applyRemote(json: string): void {
  const items = parse(json);
  if (JSON.stringify(items) === JSON.stringify(list())) return;
  saveLocal(items);
  notify();
}

// Re-render hook for the search screen. Returns the unsubscribe function.
export function subscribe(fn: Listener): () => void {
  listeners.push(fn);
  return function () {
    const i = listeners.indexOf(fn);
    if (i >= 0) listeners.splice(i, 1);
  };
}

// Add a query to the top; move an existing (case-insensitive) match up instead
// of duplicating it.
export function add(query: string): void {
  const q = (query || '').trim();
  if (!q) return;
  const cur = list();
  const low = q.toLowerCase();
  const out: string[] = [q];
  for (let i = 0; i < cur.length; i++) {
    if (cur[i].toLowerCase() !== low) out.push(cur[i]);
  }
  save(out);
}

export function remove(query: string): void {
  const cur = list();
  const low = (query || '').toLowerCase();
  const out: string[] = [];
  for (let i = 0; i < cur.length; i++) {
    if (cur[i].toLowerCase() !== low) out.push(cur[i]);
  }
  save(out);
}

export function clear(): void {
  save([]);
}
