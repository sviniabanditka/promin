// Recent searches, newest first, in localStorage (Telegram's webview keeps it
// per bot). De-duplicated case-insensitively; MAX entries.

const KEY = 'promin_tg_search_history';
const MAX = 10;

export function list(): string[] {
  try {
    const arr = JSON.parse(localStorage.getItem(KEY) || '[]');
    return Array.isArray(arr) ? arr.filter((x): x is string => typeof x === 'string' && !!x).slice(0, MAX) : [];
  } catch {
    return [];
  }
}

function save(items: string[]): void {
  try {
    localStorage.setItem(KEY, JSON.stringify(items.slice(0, MAX)));
  } catch {
    /* storage unavailable — history just won't persist */
  }
}

export function add(q: string): void {
  const term = q.trim();
  if (!term) return;
  const low = term.toLowerCase();
  save([term, ...list().filter((x) => x.toLowerCase() !== low)]);
}

export function clear(): void {
  save([]);
}
