// One module-level state object + a hook. Every subscriber re-renders on any
// change; the app is small enough that this beats selectors/signals.

import { useEffect, useState } from 'preact/hooks';
import * as api from './api';
import type { Bookmark, Device, HomeResponse, LocalKey, OpenCmd, PlayerState, Playlist, QueueItem, RemoteAction, RemoteStrAction, TimecodeItem } from './api';
import { isLang, t, type Lang } from './i18n';
import { haptic } from './tg';

export type Phase = 'boot' | 'not_tg' | 'not_linked' | 'error' | 'ready';

export interface LiveState extends PlayerState {
  received_at: number; // Date.now() when this snapshot arrived — interpolation base
}

export interface State {
  phase: Phase;
  error: string | null;
  user: api.AuthUser | null;
  lang: Lang;
  devices: Device[];
  hiddenIds: string[]; // telegram sessions (incl. this one) — not TVs
  deviceId: string | null;
  states: Record<string, LiveState>;
  home: HomeResponse | null;
  bookmarks: Bookmark[] | null;
  playlists: Playlist[] | null;
  timecodes: TimecodeItem[] | null;
  queue: QueueItem[] | null;
  settings: Record<string, string>;
  toast: string | null;
}

const DEVICE_KEY = 'promin_tg_device';

let state: State = {
  phase: 'boot',
  error: null,
  user: null,
  lang: 'en',
  devices: [],
  hiddenIds: [],
  deviceId: (() => {
    try {
      return sessionStorage.getItem(DEVICE_KEY);
    } catch {
      return null;
    }
  })(),
  states: {},
  home: null,
  bookmarks: null,
  playlists: null,
  timecodes: null,
  queue: null,
  settings: {},
  toast: null,
};

const subs = new Set<() => void>();

export function getState(): State {
  return state;
}

export function setState(patch: Partial<State> | ((s: State) => Partial<State>)): void {
  state = { ...state, ...(typeof patch === 'function' ? patch(state) : patch) };
  subs.forEach((f) => f());
}

export function useStore(): State {
  const [, force] = useState(0);
  useEffect(() => {
    const f = () => force((n) => n + 1);
    subs.add(f);
    return () => {
      subs.delete(f);
    };
  }, []);
  return state;
}

// ---- devices -----------------------------------------------------------------

// Only devices that are online right now: the profile accumulates sessions from
// every browser ever used, and a picker full of dead entries helps nobody.
export function visibleDevices(s: State): Device[] {
  return s.devices.filter((d) => d.online && !s.hiddenIds.includes(d.id));
}

// Chosen device if it is online; otherwise the first online one; otherwise the
// chosen (offline) one so the UI can say "offline" instead of "no TV".
export function targetDevice(s: State): Device | null {
  const vis = visibleDevices(s);
  const chosen = vis.find((d) => d.id === s.deviceId);
  if (chosen?.online) return chosen;
  return vis.find((d) => d.online) ?? chosen ?? vis[0] ?? null;
}

export function chooseDevice(id: string): void {
  setState({ deviceId: id });
  try {
    sessionStorage.setItem(DEVICE_KEY, id);
  } catch {
    /* ignore */
  }
}

export function applyPlayerState(p: PlayerState & { device_id?: string }): void {
  const id = p.device_id;
  if (!id) return;
  setState((s) => {
    const states = { ...s.states };
    if (p.closed) delete states[id];
    else states[id] = { ...p, received_at: Date.now() };
    return { states };
  });
}

export async function refreshDevices(): Promise<void> {
  try {
    const { devices } = await api.getDevices();
    setState((s) => {
      const states = { ...s.states };
      for (const d of devices) {
        if (d.state) states[d.id] = { ...d.state, device_id: d.id, received_at: Date.now() };
        else if (!d.online) delete states[d.id];
      }
      return { devices, states };
    });
  } catch {
    /* keep the previous list; polled again shortly */
  }
}

// Sessions of type `telegram` (this Mini App, other phones) are not TVs.
export async function loadHiddenIds(): Promise<void> {
  try {
    const { devices } = await api.getAuthDevices();
    setState({ hiddenIds: devices.filter((d) => d.current || d.device_type === 'telegram').map((d) => d.token_id) });
  } catch {
    /* non-fatal: the chip may list this phone until the next try */
  }
}

// Interpolated playback position for a snapshot.
export function livePos(st: LiveState, now = Date.now()): number {
  if (st.paused) return st.position_sec;
  const p = st.position_sec + (now - st.received_at) / 1000;
  return st.duration_sec > 0 ? Math.min(p, st.duration_sec) : p;
}

// ---- data ----------------------------------------------------------------------

export function setLang(lang: Lang): void {
  api.setApiLang(lang);
  document.documentElement.lang = lang;
  setState({ lang });
}

export async function loadBootstrap(): Promise<void> {
  try {
    const b = await api.getBootstrap();
    setState({ bookmarks: b.bookmarks ?? [], playlists: b.playlists ?? [], timecodes: b.timecodes ?? [], queue: b.queue ?? [], settings: b.settings ?? {} });
    if (isLang(b.settings?.lang) && b.settings.lang !== state.lang) {
      setLang(b.settings.lang);
      setState({ home: null }); // home rows are language-dependent
    }
  } catch {
    /* Library shows retry */
  }
}

// Synced profile setting: optimistic, reverted on error. `lang` goes through setLang.
export async function setSetting(key: string, value: string): Promise<void> {
  const prev = state.settings[key];
  const patch = (v: string | undefined) =>
    setState((s) => {
      const settings = { ...s.settings };
      if (v === undefined) delete settings[key];
      else settings[key] = v;
      return { settings };
    });
  patch(value);
  try {
    await api.putSetting(key, value);
  } catch {
    patch(prev);
    toast(t('common.error'));
    haptic('err');
  }
}

export async function loadHome(): Promise<void> {
  const res = await api.getHome();
  setState({ home: res });
}

export function isBookmarked(s: State, tmdbId: number, type: api.MediaType, fallback?: boolean): boolean {
  if (!s.bookmarks) return !!fallback;
  return s.bookmarks.some((b) => b.tmdb_id === tmdbId && b.media_type === type);
}

export async function toggleBookmark(tmdbId: number, type: api.MediaType, current: boolean): Promise<void> {
  const patch = (on: boolean) =>
    setState((s) => {
      const list = (s.bookmarks ?? []).filter((b) => !(b.tmdb_id === tmdbId && b.media_type === type));
      if (on) list.unshift({ tmdb_id: tmdbId, media_type: type, added_at: Math.floor(Date.now() / 1000) });
      return { bookmarks: list };
    });
  patch(!current);
  try {
    if (current) await api.removeBookmark(tmdbId, type);
    else await api.addBookmark(tmdbId, type);
    haptic('select');
  } catch {
    patch(current);
    toast(t('common.error'));
  }
}

// ---- watch queue ------------------------------------------------------------------
// The server echoes every change as queue_updated (full list) — writes below only
// patch optimistically where the lag would be visible (reorder, remove).

export function queuedItem(s: State, tmdbId: number, type: api.MediaType, season?: number | null, episode?: number | null): QueueItem | undefined {
  return (s.queue ?? []).find((q) => q.tmdb_id === tmdbId && q.media_type === type && (q.season ?? null) === (season ?? null) && (q.episode ?? null) === (episode ?? null));
}

export async function queueToggle(tmdbId: number, type: api.MediaType, season?: number | null, episode?: number | null): Promise<void> {
  haptic('select');
  const cur = queuedItem(state, tmdbId, type, season, episode);
  try {
    if (cur) await queueRemove(cur.id);
    else {
      await api.addQueue(tmdbId, type, season, episode);
      toast(t('queue.added'));
    }
  } catch {
    toast(t('common.error'));
  }
}

export async function queueRemove(id: number): Promise<void> {
  setState((s) => ({ queue: (s.queue ?? []).filter((q) => q.id !== id) }));
  await api.removeQueue(id);
}

export async function queueMove(id: number, position: number): Promise<void> {
  haptic('select');
  setState((s) => {
    const list = [...(s.queue ?? [])];
    const from = list.findIndex((q) => q.id === id);
    if (from < 0) return {};
    const [it] = list.splice(from, 1);
    list.splice(Math.max(0, Math.min(position, list.length)), 0, it);
    return { queue: list.map((q, i) => ({ ...q, position: i })) };
  });
  try {
    await api.moveQueue(id, position);
  } catch {
    toast(t('common.error'));
  }
}

export async function queueClear(): Promise<void> {
  setState({ queue: [] });
  try {
    await api.clearQueue();
  } catch {
    toast(t('common.error'));
  }
}

// Open the head on the TV, then drop it from the queue (only when the send went through).
export async function queuePlayHead(): Promise<void> {
  const head = state.queue?.[0];
  if (!head) return;
  const ok = await sendOpen({ tmdb_id: head.tmdb_id, media_type: head.media_type, season: head.season ?? undefined, episode: head.episode ?? undefined });
  if (ok) queueRemove(head.id).catch(() => {});
}

// ---- send to TV -----------------------------------------------------------------

let toastTimer = 0;
export function toast(msg: string): void {
  setState({ toast: msg });
  clearTimeout(toastTimer);
  toastTimer = window.setTimeout(() => setState({ toast: null }), 2500);
}

async function sendTo(body: (deviceId: string) => api.SendBody, okMsg?: string): Promise<boolean> {
  const dev = targetDevice(state);
  if (!dev || !dev.online) {
    toast(t(dev ? 'remote.offline' : 'remote.no_device'));
    haptic('err');
    return false;
  }
  try {
    await api.send(body(dev.id));
    if (okMsg) toast(okMsg);
    return true;
  } catch (e) {
    const offline = e instanceof api.ApiError && e.code === 'device_offline';
    toast(t(offline ? 'remote.offline' : 'remote.send_failed'));
    haptic('err');
    if (offline) refreshDevices();
    return false;
  }
}

export function sendOpen(open: OpenCmd): Promise<boolean> {
  const dev = targetDevice(state);
  return sendTo((device_id) => ({ device_id, open }), dev ? t('title.sent', { name: dev.name }) : undefined);
}

export function sendRemote(action: RemoteAction, value?: number): Promise<boolean> {
  haptic('tap');
  const dev = targetDevice(state);
  // Optimistic local update so the UI doesn't snap back before the next player_state.
  if (dev && state.states[dev.id]) {
    const st = state.states[dev.id];
    const now = Date.now();
    let next: LiveState | null = null;
    if (action === 'toggle_play') next = { ...st, position_sec: livePos(st, now), paused: !st.paused, received_at: now };
    else if (action === 'seek_to' && value != null) next = { ...st, position_sec: value, received_at: now };
    else if (action === 'seek' && value != null) next = { ...st, position_sec: Math.max(0, livePos(st, now) + value), received_at: now };
    else if (action === 'volume' && value != null) next = { ...st, volume: value, muted: false };
    else if (action === 'mute') next = { ...st, muted: !st.muted };
    if (next) setState((s) => ({ states: { ...s.states, [dev.id]: next! } }));
  }
  return sendTo((device_id) => ({ device_id, remote: value == null ? { action } : { action, value } }));
}

// Pick an audio / subtitle row by id on the target device; optimistic marking.
export function sendRemoteStr(action: RemoteStrAction, str: string): Promise<boolean> {
  haptic('select');
  const dev = targetDevice(state);
  if (dev && state.states[dev.id]) {
    const st = state.states[dev.id];
    const next: LiveState = action === 'set_voice' ? { ...st, voice_id: str } : { ...st, subtitle_id: str };
    setState((s) => ({ states: { ...s.states, [dev.id]: next } }));
  }
  return sendTo((device_id) => ({ device_id, remote: { action, str } }));
}

// Device-local TV setting on the target device: optimistic, reverted on error.
export async function sendLocal(key: LocalKey, on: boolean): Promise<void> {
  haptic('select');
  const dev = targetDevice(state);
  if (!dev) return;
  const patch = (v: string | undefined) =>
    setState((s) => ({ devices: s.devices.map((d) => (d.id === dev.id ? { ...d, settings: { ...(d.settings ?? {}), [key]: v } } : d)) }));
  const prev = dev.settings?.[key];
  patch(on ? 'true' : 'false');
  const ok = await sendTo((device_id) => ({ device_id, remote: { action: 'set_local', key, str: on ? 'true' : 'false' } }));
  if (!ok) patch(prev);
}
