// Auth state — the single source of truth for the session token + user.
// Token/user persist in localStorage so a logged-in TV stays logged in across
// reboots. core/api.ts reads getToken() to attach `Authorization: Bearer`;
// core/sync.ts reads it for the WS `?t=` param.
//
// ES5 target (swc): plain functions/const/let, no async/await, no spread,
// no Array.find/includes, no Object.assign.

import { authLogout, authPin, AuthUser } from './api';
import { caps } from './capabilities';

const TOKEN_KEY = 'promin:token';
const USER_KEY = 'promin:user';

function readStorage(key: string): string | null {
  try {
    return window.localStorage.getItem(key);
  } catch (e) {
    return null;
  }
}

function writeStorage(key: string, value: string | null): void {
  try {
    if (value === null) window.localStorage.removeItem(key);
    else window.localStorage.setItem(key, value);
  } catch (e) {
    /* storage unavailable — session just won't persist */
  }
}

function parseUser(): AuthUser | null {
  const raw = readStorage(USER_KEY);
  if (!raw) return null;
  try {
    return JSON.parse(raw) as AuthUser;
  } catch (e) {
    return null;
  }
}

let token: string | null = readStorage(TOKEN_KEY);
let user: AuthUser | null = parseUser();

type Listener = () => void;
const listeners: Listener[] = [];

// Fired whenever login state changes (login / logout). app.ts, ui/menu.ts and
// core/sync.ts subscribe to (re)wire the session.
export function onAuthChange(fn: Listener): void {
  listeners.push(fn);
}

function emit(): void {
  const copy = listeners.slice(0);
  for (let i = 0; i < copy.length; i++) copy[i]();
}

export function getToken(): string | null {
  return token;
}

export function getUser(): AuthUser | null {
  return user;
}

export function isLogged(): boolean {
  return !!token;
}

function store(t: string, u: AuthUser): void {
  token = t;
  user = u;
  writeStorage(TOKEN_KEY, t);
  writeStorage(USER_KEY, JSON.stringify(u));
  emit();
}

// A human-ish device label from the detected platform so the session list
// (docs/api.md GET /auth/devices) is readable. device_type matches capabilities.
function deviceName(): string {
  const names: { [k: string]: string } = {
    tizen: 'Samsung TV',
    webos: 'LG TV',
    androidtv: 'Android TV',
    browser: 'Browser',
  };
  return names[caps.platform] || 'Promin';
}

function deviceType(): string {
  return caps.platform;
}

// PIN login (the gate). Resolves to the profile and stores its session.
export function loginPin(pin: string): Promise<AuthUser> {
  return authPin({ pin: pin, device_name: deviceName(), device_type: deviceType() }).then(function (res) {
    store(res.token, res.user);
    return res.user;
  });
}

export function clearLocal(): void {
  token = null;
  user = null;
  writeStorage(TOKEN_KEY, null);
  writeStorage(USER_KEY, null);
  emit();
}

// Best-effort server revoke, then always clear locally regardless of outcome.
export function logout(): Promise<void> {
  const had = !!token;
  const finish = function () {
    clearLocal();
  };
  if (!had) {
    finish();
    return Promise.resolve();
  }
  return authLogout().then(finish, finish);
}
