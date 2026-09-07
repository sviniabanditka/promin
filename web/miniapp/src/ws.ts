// Sync socket: /api/v1/ws?t=<token>. Reconnects with backoff, pings every 25 s,
// refreshes the device list on every (re)connect since presence is not an event.

import { isLang } from './i18n';
import * as api from './api';
import type { Bookmark, Playlist, PlayerState, TimecodeItem } from './api';
import { applyPlayerState, loadBootstrap, refreshDevices, setLang, setState } from './store';

let ws: WebSocket | null = null;
let attempt = 0;
let reconnectTimer = 0;
let pingTimer = 0;
let stopped = false;

export function startWs(): void {
  stopped = false;
  connect();
  document.addEventListener('visibilitychange', () => {
    if (document.visibilityState === 'visible' && (!ws || ws.readyState > WebSocket.OPEN)) {
      clearTimeout(reconnectTimer);
      attempt = 0;
      connect();
    }
  });
}

export function stopWs(): void {
  stopped = true;
  clearTimeout(reconnectTimer);
  clearInterval(pingTimer);
  ws?.close();
  ws = null;
}

function connect(): void {
  if (stopped || !api.getToken()) return;
  try {
    ws = new WebSocket(api.wsUrl());
  } catch {
    schedule();
    return;
  }
  ws.onopen = () => {
    attempt = 0;
    refreshDevices();
    clearInterval(pingTimer);
    pingTimer = window.setInterval(() => ws?.readyState === WebSocket.OPEN && ws.send('{"type":"ping"}'), 25000);
  };
  ws.onmessage = (ev) => {
    let msg: { type?: string; payload?: unknown };
    try {
      msg = JSON.parse(String(ev.data));
    } catch {
      return;
    }
    if (msg.type) handle(msg.type, (msg.payload ?? {}) as Record<string, any>);
  };
  ws.onclose = () => {
    clearInterval(pingTimer);
    ws = null;
    schedule();
  };
  ws.onerror = () => ws?.close();
}

function schedule(): void {
  if (stopped) return;
  clearTimeout(reconnectTimer);
  const delay = Math.min(30000, 1000 * 2 ** Math.min(attempt++, 5));
  reconnectTimer = window.setTimeout(connect, delay);
}

const tcKey = (x: { tmdb_id: number; media_type: string; season?: number | null; episode?: number | null }) =>
  [x.tmdb_id, x.media_type, x.season ?? '', x.episode ?? ''].join(':');

function handle(type: string, p: Record<string, any>): void {
  switch (type) {
    case 'player_state':
      applyPlayerState(p as PlayerState & { device_id?: string });
      break;
    case 'settings_updated':
      setState((s) => ({ settings: { ...s.settings, [String(p.key)]: String(p.value) } }));
      if (p.key === 'lang' && isLang(p.value)) {
        setLang(p.value);
        setState({ home: null });
      }
      break;
    case 'timecode_updated': {
      const tc = p as TimecodeItem;
      setState((s) => ({ timecodes: [tc, ...(s.timecodes ?? []).filter((x) => tcKey(x) !== tcKey(tc))] }));
      break;
    }
    case 'bookmark_added': {
      const b = p as Bookmark;
      setState((s) => ({ bookmarks: [b, ...(s.bookmarks ?? []).filter((x) => !(x.tmdb_id === b.tmdb_id && x.media_type === b.media_type))] }));
      break;
    }
    case 'bookmark_removed':
      setState((s) => ({ bookmarks: (s.bookmarks ?? []).filter((x) => !(x.tmdb_id === p.tmdb_id && x.media_type === p.media_type)) }));
      break;
    case 'playlist_created':
    case 'playlist_updated':
      setState((s) => {
        const rest = (s.playlists ?? []).filter((x) => x.id !== p.id);
        return { playlists: p.deleted ? rest : [p as Playlist, ...rest] };
      });
      break;
    case 'playlist_item_added':
    case 'playlist_item_removed':
      setState((s) => ({
        playlists: (s.playlists ?? []).map((x) =>
          x.id === p.playlist_id ? { ...x, items_count: Math.max(0, x.items_count + (type === 'playlist_item_added' ? 1 : -1)) } : x
        ),
      }));
      break;
    case 'device_settings':
      setState((s) => ({ devices: s.devices.map((d) => (d.id === p.device_id ? { ...d, settings: p.settings ?? {} } : d)) }));
      break;
    case 'queue_updated':
      setState({ queue: Array.isArray(p.items) ? p.items : [] });
      break;
    case 'data_cleared':
      loadBootstrap();
      break;
  }
}
