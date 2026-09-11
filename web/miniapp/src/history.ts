// Recent searches — the profile setting `search_history` (JSON array, newest
// first), shared with every TV of the profile; the TV writes the same key.
// Read from the store (bootstrap + settings_updated keep it fresh), written
// through setSetting so the TVs get the change.

import { getState, setSetting } from './store';

export const KEY = 'search_history';
const MAX = 15;

export function parse(raw: string | undefined): string[] {
  try {
    const arr = JSON.parse(raw || '[]');
    return Array.isArray(arr) ? arr.filter((x): x is string => typeof x === 'string' && !!x).slice(0, MAX) : [];
  } catch {
    return [];
  }
}

export function list(): string[] {
  return parse(getState().settings[KEY]);
}

export function add(q: string): void {
  const term = q.trim();
  if (!term) return;
  const low = term.toLowerCase();
  void setSetting(KEY, JSON.stringify([term, ...list().filter((x) => x.toLowerCase() !== low)].slice(0, MAX)));
}

export function clear(): void {
  void setSetting(KEY, '[]');
}
