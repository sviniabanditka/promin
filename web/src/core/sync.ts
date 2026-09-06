// Sync client — replaces CUB. Keeps a local cache of bookmarks + timecodes in
// memory (mirrored to localStorage for instant paint on next boot), stays in
// step with the server through a WebSocket push channel (docs/api.md), and
// falls back to polling GET /sync/events?since= when WS is unavailable.
//
// Screens read the cache synchronously (isBookmarked / getTimecode) and
// subscribe() to repaint on remote changes pushed from another device.
//
// ES5 target (swc): no async/await, no for-of, no spread, no Array.find/
// includes, no Object.assign, no WeakMap. WebSocket/localStorage are runtime
// APIs (not ES5 syntax) so es-check is unaffected.

import {
  getSyncBootstrap,
  getSyncEvents,
  getBookmarks,
  addBookmark,
  removeBookmark,
  upsertTimecode,
  Bookmark,
  TimecodeRecord,
  SyncEvent,
} from './api';
import { getToken, isLogged, onAuthChange } from './auth';
import { applyRemoteSetting } from './settings';

// ---- cache -------------------------------------------------------------

interface CachedTimecode extends TimecodeRecord {
  tmdb_id: number;
  media_type: string;
  season: number | null;
  episode: number | null;
}

const CACHE_KEY = 'promin:sync-cache';

// bookmarks keyed "tmdb:media_type"; timecodes keyed "tmdb:media_type:s:e".
let bookmarks: { [key: string]: Bookmark } = {};
let timecodes: { [key: string]: CachedTimecode } = {};
let cursor = 0;

function bkey(tmdbId: number, mediaType: string): string {
  return tmdbId + ':' + mediaType;
}

function tkey(tmdbId: number, mediaType: string, season?: number | null, episode?: number | null): string {
  return tmdbId + ':' + mediaType + ':' + (season != null ? season : '') + ':' + (episode != null ? episode : '');
}

// persist is debounced: it stringifies EVERY bookmark and timecode and does a
// synchronous localStorage write, and it used to run on each 10s playback tick
// and each WS event. Trailing 2s is plenty — the server is the source of truth.
const PERSIST_DEBOUNCE_MS = 2000;
// Timecodes are capped: only the newest MAX_TIMECODES are kept locally, so the
// blob (and its stringify cost) stops growing with every episode ever watched.
const MAX_TIMECODES = 300;
let persistTimer = 0;
function persist(): void {
  if (persistTimer) return;
  persistTimer = window.setTimeout(persistNow, PERSIST_DEBOUNCE_MS);
}
function persistNow(): void {
  persistTimer = 0;
  trimTimecodes();
  try {
    window.localStorage.setItem(
      CACHE_KEY,
      JSON.stringify({ bookmarks: bookmarks, timecodes: timecodes, cursor: cursor })
    );
  } catch (e) {
    /* ignore quota / unavailable */
  }
}
function trimTimecodes(): void {
  const keys: string[] = [];
  for (const k in timecodes) if (Object.prototype.hasOwnProperty.call(timecodes, k)) keys.push(k);
  if (keys.length <= MAX_TIMECODES) return;
  keys.sort(function (a, b) {
    return (timecodes[b].updated_at || 0) - (timecodes[a].updated_at || 0);
  });
  for (let i = MAX_TIMECODES; i < keys.length; i++) delete timecodes[keys[i]];
}

function loadPersisted(): void {
  try {
    const raw = window.localStorage.getItem(CACHE_KEY);
    if (!raw) return;
    const data = JSON.parse(raw) as { bookmarks?: typeof bookmarks; timecodes?: typeof timecodes; cursor?: number };
    if (data.bookmarks) bookmarks = data.bookmarks;
    if (data.timecodes) timecodes = data.timecodes;
    if (typeof data.cursor === 'number') cursor = data.cursor;
  } catch (e) {
    /* ignore corrupt cache */
  }
}

function resetCache(): void {
  if (persistTimer) window.clearTimeout(persistTimer); // a pending write must not resurrect the old cache
  persistTimer = 0;
  bookmarks = {};
  timecodes = {};
  cursor = 0;
  try {
    window.localStorage.removeItem(CACHE_KEY);
  } catch (e) {
    /* ignore */
  }
}

// ---- event emitter -----------------------------------------------------

type Topic = 'bookmarks' | 'timecodes';
const subscribers: { [topic: string]: Array<() => void> } = {};

// Subscribe a screen to cache changes for a topic. Returns an unsubscribe fn.
export function subscribe(topic: Topic, cb: () => void): () => void {
  if (!subscribers[topic]) subscribers[topic] = [];
  subscribers[topic].push(cb);
  return function () {
    const list = subscribers[topic];
    if (!list) return;
    const idx = list.indexOf(cb);
    if (idx >= 0) list.splice(idx, 1);
  };
}

function notify(topic: Topic): void {
  const list = subscribers[topic];
  if (!list) return;
  const copy = list.slice(0);
  for (let i = 0; i < copy.length; i++) copy[i]();
}

// ---- reads (synchronous, from cache) -----------------------------------

export function getBookmarksList(): Bookmark[] {
  const out: Bookmark[] = [];
  for (const k in bookmarks) {
    if (Object.prototype.hasOwnProperty.call(bookmarks, k)) out.push(bookmarks[k]);
  }
  // newest first when added_at is known
  out.sort(function (a, b) {
    return (b.added_at || 0) - (a.added_at || 0);
  });
  return out;
}

export function isBookmarked(tmdbId: number, mediaType: string): boolean {
  return Object.prototype.hasOwnProperty.call(bookmarks, bkey(tmdbId, mediaType));
}

// True once the first bootstrap attempt has settled (so the bookmarks screen
// can show a spinner instead of flashing the empty CTA on a cold cache).
let bookmarksReady = false;
export function isBookmarksReady(): boolean {
  return bookmarksReady;
}

// Every cached timecode of one title (all seasons/episodes) — for "Continue"
// on the title screen, which needs the most recently touched episode.
export function getTimecodesFor(tmdbId: number, mediaType: string): CachedTimecode[] {
  const prefix = tmdbId + ':' + mediaType + ':';
  const out: CachedTimecode[] = [];
  for (const k in timecodes) {
    if (Object.prototype.hasOwnProperty.call(timecodes, k) && k.indexOf(prefix) === 0) out.push(timecodes[k]);
  }
  return out;
}

export function getTimecodeCached(
  tmdbId: number,
  mediaType: string,
  season?: number | null,
  episode?: number | null
): CachedTimecode | null {
  const k = tkey(tmdbId, mediaType, season, episode);
  return Object.prototype.hasOwnProperty.call(timecodes, k) ? timecodes[k] : null;
}

// ---- writes (optimistic; server confirms via REST + WS echo) -----------

// meta: the card's title/poster/etc. so the optimistic entry is already
// enriched — the Library painted a blank card (no title, no poster) until the
// next full bootstrap otherwise.
export function toggleBookmark(tmdbId: number, mediaType: string, meta?: Partial<Bookmark>): Promise<boolean> {
  const k = bkey(tmdbId, mediaType);
  const wasSet = Object.prototype.hasOwnProperty.call(bookmarks, k);
  if (wasSet) {
    delete bookmarks[k];
    notify('bookmarks');
    persist();
    return removeBookmark(tmdbId, mediaType).then(
      function () {
        return false;
      },
      function () {
        // rollback on failure
        bookmarks[k] = { tmdb_id: tmdbId, media_type: mediaType, added_at: Math.floor(Date.now() / 1000) };
        notify('bookmarks');
        persist();
        return true;
      }
    );
  }
  const entry: Bookmark = { tmdb_id: tmdbId, media_type: mediaType, added_at: Math.floor(Date.now() / 1000) };
  if (meta) {
    if (meta.title) entry.title = meta.title;
    if (meta.poster) entry.poster = meta.poster;
    if (meta.backdrop) entry.backdrop = meta.backdrop;
    if (meta.year != null) entry.year = meta.year;
    if (meta.rating != null) entry.rating = meta.rating;
  }
  bookmarks[k] = entry;
  notify('bookmarks');
  persist();
  return addBookmark(tmdbId, mediaType).then(
    function () {
      return true;
    },
    function () {
      delete bookmarks[k];
      notify('bookmarks');
      persist();
      return false;
    }
  );
}

// Save a playback position. Writes cache immediately, POSTs upsert (docs/api.md
// last-write-wins). If the server wins the conflict it echoes the newer record,
// which we adopt.
export function saveTimecode(
  tmdbId: number,
  mediaType: string,
  season: number | null,
  episode: number | null,
  positionSec: number,
  durationSec: number
): void {
  if (!isLogged() || !durationSec || durationSec <= 0) return;
  const updatedAt = Math.floor(Date.now() / 1000);
  const rec: CachedTimecode = {
    tmdb_id: tmdbId,
    media_type: mediaType,
    season: season,
    episode: episode,
    position_sec: positionSec,
    duration_sec: durationSec,
    updated_at: updatedAt,
  };
  timecodes[tkey(tmdbId, mediaType, season, episode)] = rec;
  notify('timecodes');
  persist();

  upsertTimecode({
    tmdb_id: tmdbId,
    media_type: mediaType,
    season: season,
    episode: episode,
    position_sec: positionSec,
    duration_sec: durationSec,
    updated_at: updatedAt,
  }).then(
    function (res) {
      if (res && res.accepted === false) {
        rec.position_sec = res.position_sec;
        if (res.duration_sec) rec.duration_sec = res.duration_sec;
        if (res.updated_at) rec.updated_at = res.updated_at;
        notify('timecodes');
        persist();
      }
    },
    function () {
      /* offline: cache keeps the value, retried implicitly on next save */
    }
  );
}

// ---- incoming events (WS + polling share this) -------------------------

function applyEvent(ev: SyncEvent): void {
  if (!ev || typeof ev.id !== 'number') return;
  if (ev.id > cursor) cursor = ev.id;
  const p = (ev.payload || {}) as { [k: string]: unknown };

  if (ev.type === 'bookmark_added') {
    const tmdb = Number(p.tmdb_id);
    const mt = String(p.media_type);
    const key = bkey(tmdb, mt);
    const at = typeof p.added_at === 'number' ? (p.added_at as number) : Math.floor(Date.now() / 1000);
    // The echo of our own add carries only ids — merge into the enriched entry
    // instead of replacing it with a bare one.
    if (Object.prototype.hasOwnProperty.call(bookmarks, key)) {
      bookmarks[key].added_at = at;
    } else {
      bookmarks[key] = { tmdb_id: tmdb, media_type: mt, added_at: at };
    }
    notify('bookmarks');
    persist();
  } else if (ev.type === 'bookmark_removed') {
    delete bookmarks[bkey(Number(p.tmdb_id), String(p.media_type))];
    notify('bookmarks');
    persist();
  } else if (ev.type === 'timecode_updated') {
    const tmdb = Number(p.tmdb_id);
    const mt = String(p.media_type);
    const season = p.season != null ? Number(p.season) : null;
    const episode = p.episode != null ? Number(p.episode) : null;
    const k = tkey(tmdb, mt, season, episode);
    const incoming = typeof p.updated_at === 'number' ? (p.updated_at as number) : Math.floor(Date.now() / 1000);
    const existing = timecodes[k];
    // Ignore a stale/echo event: the WS may re-deliver our own just-saved
    // position (or an out-of-order older one), which would otherwise stomp a
    // fresher local timecode and make resume jump backwards. See audit #11.
    if (existing && (existing.updated_at || 0) > incoming) return;
    timecodes[k] = {
      tmdb_id: tmdb,
      media_type: mt,
      season: season,
      episode: episode,
      position_sec: Number(p.position_sec) || 0,
      duration_sec: Number(p.duration_sec) || 0,
      updated_at: incoming,
    };
    notify('timecodes');
    persist();
  }
  else if (ev.type === 'settings_updated') {
    applyRemoteSetting(String(p.key || ''), String(p.value || ''));
  } else if (ev.type === 'data_cleared') {
    // Settings → Danger zone on another device (or this one): drop local copies.
    timecodes = {};
    notify('timecodes');
    if (p.scope === 'all') {
      bookmarks = {};
      notify('bookmarks');
    }
    persist();
  }
  // history_added / playlist_* — not surfaced by the current UI; cursor still
  // advances so we don't re-fetch them.
}

// ---- bootstrap ---------------------------------------------------------

function bootstrap(): void {
  getSyncBootstrap().then(
    function (snap) {
      if (!snap) return;
      const bm = snap.bookmarks || [];
      bookmarks = {};
      for (let i = 0; i < bm.length; i++) bookmarks[bkey(bm[i].tmdb_id, bm[i].media_type)] = bm[i];
      const tc = snap.timecodes || [];
      timecodes = {};
      for (let j = 0; j < tc.length; j++) {
        const r = tc[j];
        timecodes[tkey(r.tmdb_id, r.media_type, r.season, r.episode)] = {
          tmdb_id: r.tmdb_id,
          media_type: r.media_type,
          season: r.season != null ? r.season : null,
          episode: r.episode != null ? r.episode : null,
          position_sec: r.position_sec,
          duration_sec: r.duration_sec,
          updated_at: r.updated_at,
        };
      }
      if (typeof snap.cursor === 'number') cursor = snap.cursor;
      bookmarksReady = true;
      notify('bookmarks');
      notify('timecodes');
      persist();
    },
    function () {
      // No /sync/bootstrap? Fall back to just bookmarks so the star state works.
      getBookmarks().then(
        function (res) {
          const list = (res && (res.bookmarks || res.items)) || [];
          bookmarks = {};
          for (let i = 0; i < list.length; i++) bookmarks[bkey(list[i].tmdb_id, list[i].media_type)] = list[i];
          bookmarksReady = true;
          notify('bookmarks');
          persist();
        },
        function () {
          /* ignore — cache stays as loaded from localStorage */
          bookmarksReady = true;
          notify('bookmarks');
        }
      );
    }
  );
}

// ---- WebSocket + polling transport -------------------------------------

let ws: WebSocket | null = null;
let wsWanted = false;
let reconnectDelay = 1000;
let reconnectTimer = 0;
let pingTimer = 0;
let pollTimer = 0;

const MAX_RECONNECT = 30000;
const PING_MS = 25000;
const POLL_MS = 15000;

function wsUrl(token: string): string {
  const proto = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
  return proto + '//' + window.location.host + '/api/v1/ws?t=' + encodeURIComponent(token);
}

function stopTimers(): void {
  if (reconnectTimer) {
    window.clearTimeout(reconnectTimer);
    reconnectTimer = 0;
  }
  if (pingTimer) {
    window.clearInterval(pingTimer);
    pingTimer = 0;
  }
  if (pollTimer) {
    window.clearInterval(pollTimer);
    pollTimer = 0;
  }
}

// HTTP catch-up: pull everything with id > cursor (docs/api.md). Used after a
// reconnect and as the sole channel when WebSocket is unavailable.
function pollOnce(): void {
  if (!wsWanted || !isLogged()) return;
  getSyncEvents(cursor).then(
    function (res) {
      if (!res) return;
      const events = res.events || [];
      for (let i = 0; i < events.length; i++) applyEvent(events[i]);
      if (typeof res.cursor === 'number' && res.cursor > cursor) cursor = res.cursor;
    },
    function (err) {
      // 410 Gone → cursor too old, re-bootstrap a full snapshot.
      if (err && err.status === 410) bootstrap();
    }
  );
}

function startPolling(): void {
  if (pollTimer) return;
  pollOnce();
  pollTimer = window.setInterval(pollOnce, POLL_MS);
}

function scheduleReconnect(): void {
  if (!wsWanted) return;
  if (reconnectTimer) return;
  // Keep syncing over HTTP while the socket is down.
  startPolling();
  reconnectTimer = window.setTimeout(function () {
    reconnectTimer = 0;
    connectWs();
  }, reconnectDelay);
  reconnectDelay = Math.min(reconnectDelay * 2, MAX_RECONNECT);
}

function connectWs(): void {
  if (!wsWanted) return;
  // Don't open a second socket over a live/connecting one (orphans the first).
  if (ws && (ws.readyState === 0 || ws.readyState === 1)) return;
  const token = getToken();
  if (!token) return;
  if (typeof (window as unknown as { WebSocket?: unknown }).WebSocket !== 'function') {
    // No WebSocket in this webview → pure polling.
    startPolling();
    return;
  }

  let sock: WebSocket;
  try {
    sock = new WebSocket(wsUrl(token));
  } catch (e) {
    scheduleReconnect();
    return;
  }
  ws = sock;

  sock.onopen = function () {
    reconnectDelay = 1000;
    // Socket is live; the periodic HTTP poll can stand down (we caught up once).
    pollOnce();
    if (pollTimer) {
      window.clearInterval(pollTimer);
      pollTimer = 0;
    }
    if (pingTimer) window.clearInterval(pingTimer);
    pingTimer = window.setInterval(function () {
      try {
        sock.send(JSON.stringify({ type: 'ping' }));
      } catch (e) {
        /* ignore */
      }
    }, PING_MS);
  };

  sock.onmessage = function (msg: MessageEvent) {
    let data: SyncEvent | { type?: string } | null = null;
    try {
      data = JSON.parse(String(msg.data));
    } catch (e) {
      return;
    }
    if (!data || !data.type || data.type === 'pong') return;
    applyEvent(data as SyncEvent);
  };

  sock.onerror = function () {
    try {
      sock.close();
    } catch (e) {
      /* ignore */
    }
  };

  sock.onclose = function () {
    if (ws === sock) ws = null;
    if (pingTimer) {
      window.clearInterval(pingTimer);
      pingTimer = 0;
    }
    scheduleReconnect();
  };
}

// ---- lifecycle ---------------------------------------------------------

export function start(): void {
  if (wsWanted) return; // idempotent: login fires start() twice (onAuthChange + onAuthOk)
  if (!isLogged()) return;
  wsWanted = true;
  reconnectDelay = 1000;
  bookmarksReady = false; // a fresh session hasn't settled its first bootstrap yet
  loadPersisted();
  bootstrap();
  connectWs();
}

export function stop(): void {
  wsWanted = false;
  bookmarksReady = false;
  stopTimers();
  if (ws) {
    const sock = ws;
    ws = null;
    try {
      sock.close();
    } catch (e) {
      /* ignore */
    }
  }
  resetCache();
  notify('bookmarks');
  notify('timecodes');
}

// Re-wire the transport whenever login state flips (login → start, logout →
// stop). app.ts also calls start() explicitly after a fresh login.
onAuthChange(function () {
  if (isLogged()) {
    if (!wsWanted) start();
  } else {
    if (wsWanted) stop();
  }
});
