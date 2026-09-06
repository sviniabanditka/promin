// Recent-search history, persisted in localStorage (Lampa parity:
// src/interaction/search keeps a "history" list of past queries). Stores up to
// MAX unique queries, newest first (case-insensitive de-dup). No ES6: plain
// loops, indexOf, no Array.find/includes/spread — matches the ES5 bundle.

const KEY = 'promin:search_history';
const MAX = 15;

export function list(): string[] {
  try {
    const raw = window.localStorage.getItem(KEY);
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

function save(items: string[]): void {
  try {
    window.localStorage.setItem(KEY, JSON.stringify(items.slice(0, MAX)));
  } catch (e) {
    /* storage unavailable/full: history just won't persist */
  }
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
  try {
    window.localStorage.removeItem(KEY);
  } catch (e) {
    /* ignore */
  }
}
