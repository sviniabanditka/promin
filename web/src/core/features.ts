// Server-announced optional sections, remembered from the last /api/v1/ping so
// the rail can be built synchronously at boot. The value is refreshed by the
// update watcher in app.ts; a first-ever boot shows the item after the next
// screen build.

const KEY = 'promin:features';

interface Features {
  youtube?: boolean;
}

let cached: Features | null = null;

function load(): Features {
  if (cached) return cached;
  try {
    cached = JSON.parse(window.localStorage.getItem(KEY) || '{}') as Features;
  } catch (e) {
    cached = {};
  }
  return cached;
}

export function setFeatures(f: Features): void {
  cached = f;
  try {
    window.localStorage.setItem(KEY, JSON.stringify(f));
  } catch (e) {
    /* storage unavailable: the in-memory copy still applies this session */
  }
}

export function youtubeEnabled(): boolean {
  return !!load().youtube;
}
