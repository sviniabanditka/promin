// Server-announced optional sections, remembered from the last /api/v1/ping so
// the rail can be built synchronously at boot. The value is refreshed by the
// update watcher in app.ts; a first-ever boot shows the item after the next
// screen build.

const KEY = 'promin:features';

interface Features {
  youtube?: boolean;
  tv?: boolean;
}

// What the admin allowed THIS profile (GET /auth/me → features). Missing =
// everything: a first boot before /me answers must not hide sections.
export interface UserFeatures {
  online?: boolean;
  torrents?: boolean;
  youtube?: boolean;
  tv?: boolean;
  tv_countries?: string[];
}

const USER_KEY = 'promin:features:user';
let userCached: UserFeatures | null = null;

function loadUser(): UserFeatures {
  if (userCached) return userCached;
  try {
    userCached = JSON.parse(window.localStorage.getItem(USER_KEY) || '{}') as UserFeatures;
  } catch (e) {
    userCached = {};
  }
  return userCached;
}

export function setUserFeatures(f: UserFeatures | null): void {
  userCached = f || {};
  try {
    window.localStorage.setItem(USER_KEY, JSON.stringify(userCached));
  } catch (e) {
    /* in-memory copy still applies */
  }
}

function allowed(key: 'online' | 'torrents' | 'youtube' | 'tv'): boolean {
  const v = loadUser()[key];
  return v === undefined || !!v;
}

export function onlineEnabled(): boolean {
  return allowed('online');
}

export function torrentsEnabled(): boolean {
  return allowed('torrents');
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
  return !!load().youtube && allowed('youtube');
}

export function tvEnabled(): boolean {
  return !!load().tv && allowed('tv');
}
