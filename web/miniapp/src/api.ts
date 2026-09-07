// REST client for the Mini App (docs/api.md + docs/miniapp.md). Bearer token
// lives in memory + localStorage (Telegram re-opens the webview often; a
// sessionStorage token died with it and forced a fresh auth every open).

const BASE = '/api/v1';
const TOKEN_KEY = 'promin_tg_token';
const TIMEOUT_MS = 15000;

export type MediaType = 'movie' | 'tv';

export class ApiError extends Error {
  constructor(public status: number, public code?: string, message?: string) {
    super(message || code || 'HTTP ' + status);
  }
}

let token: string | null = null;
try {
  token = localStorage.getItem(TOKEN_KEY);
} catch {
  /* storage blocked */
}
let lang = 'en';
let unauthorizedHook: ((force?: boolean) => void) | null = null;

export function getToken(): string | null {
  return token;
}
export function setToken(t: string | null): void {
  token = t;
  try {
    if (t) localStorage.setItem(TOKEN_KEY, t);
    else localStorage.removeItem(TOKEN_KEY);
  } catch {
    /* storage blocked */
  }
}
export function setApiLang(l: string): void {
  lang = l;
}
export function onUnauthorized(fn: (force?: boolean) => void): void {
  unauthorizedHook = fn;
}
// The server dropped every session (DELETE /me/data): re-auth via initData now.
export function sessionLost(): void {
  unauthorizedHook?.(true);
}

type Q = Record<string, string | number | boolean | null | undefined>;

function qs(q?: Q): string {
  if (!q) return '';
  const p = new URLSearchParams();
  for (const k in q) {
    const v = q[k];
    if (v !== undefined && v !== null && v !== '') p.set(k, String(v));
  }
  const s = p.toString();
  return s ? '?' + s : '';
}

async function req<T>(method: string, path: string, q?: Q, body?: unknown): Promise<T> {
  const headers: Record<string, string> = { Accept: 'application/json' };
  if (body !== undefined) headers['Content-Type'] = 'application/json';
  if (token) headers.Authorization = 'Bearer ' + token;
  const ctl = new AbortController();
  const timer = setTimeout(() => ctl.abort(), TIMEOUT_MS);
  let res: Response;
  try {
    res = await fetch(BASE + path + qs(q), {
      method,
      headers,
      body: body === undefined ? undefined : JSON.stringify(body),
      signal: ctl.signal,
    });
  } catch (e) {
    throw new ApiError(0, 'network', String(e));
  } finally {
    clearTimeout(timer);
  }
  const text = await res.text();
  let data: any = null;
  try {
    data = text ? JSON.parse(text) : null;
  } catch {
    data = null;
  }
  if (res.ok) return data as T;
  if (res.status === 401 && token && path !== '/tg/auth') unauthorizedHook?.();
  throw new ApiError(res.status, data?.error?.code, data?.error?.message);
}

const get = <T,>(p: string, q?: Q) => req<T>('GET', p, q);
const post = <T,>(p: string, body?: unknown, q?: Q) => req<T>('POST', p, q, body ?? {});
const put = <T,>(p: string, body?: unknown) => req<T>('PUT', p, undefined, body ?? {});
const del = <T,>(p: string, q?: Q) => req<T>('DELETE', p, q);

// ---- DTOs -------------------------------------------------------------------

export interface Timecode {
  position_sec: number;
  duration_sec: number;
  season?: number | null;
  episode?: number | null;
}

export interface Episode {
  episode: number;
  name: string;
  air_date?: string | null;
  overview?: string;
  still?: string | null;
  runtime_minutes?: number | null;
  rating?: number | null;
  timecode?: Timecode | null;
}

export interface Season {
  season: number;
  name: string;
  episode_count?: number;
  episodes?: Episode[] | null;
}

export interface Card {
  tmdb_id: number;
  type: MediaType;
  title: string;
  original_title?: string;
  year?: number | null;
  rating?: number | null;
  imdb_rating?: number | null;
  poster?: string | null;
  backdrop?: string | null;
  overview?: string;
  genres?: string[];
  runtime_minutes?: number | null;
  content_rating?: string;
  timecode?: Timecode | null;
  in_bookmarks?: boolean;
  seasons?: Season[] | null;
}

export interface HomeRow {
  id: string;
  title: string;
  items: Card[];
}
export interface HomeResponse {
  rows: HomeRow[];
}
export interface ListResponse {
  page: number;
  total_pages: number;
  items: Card[];
}
// GET /catalog/title/{id}?season=N — backend may answer with either shape.
export interface SeasonResponse {
  season?: number;
  episodes?: Episode[];
  seasons?: Season[] | null;
}

export interface PlayerState {
  tmdb_id: number;
  media_type: MediaType;
  title: string;
  season?: number | null;
  episode?: number | null;
  position_sec: number;
  duration_sec: number;
  paused: boolean;
  source?: string;
  voice?: string;
  // Audio / subtitle menus of the TV player (ids for set_voice / set_subtitle).
  voices?: { id: string; name: string }[];
  voice_id?: string;
  subtitles?: { id: string; label: string }[];
  subtitle_id?: string; // "off" when none
  volume?: number; // 0..100
  muted?: boolean;
  updated_at: number;
  device_id?: string;
  closed?: boolean;
}

export type LocalKey = 'legacy_tv_mode' | 'reduce_motion' | 'debug_mode';
export interface Device {
  id: string;
  name: string;
  online: boolean;
  state: PlayerState | null;
  settings: Partial<Record<LocalKey, string>> | null; // device-local; null = TV has not reported yet
}

export interface Bookmark {
  tmdb_id: number;
  media_type: MediaType;
  added_at: number;
}
export interface Playlist {
  id: number;
  name: string;
  items_count: number;
  updated_at: number;
}
export interface PlaylistItem {
  id: number;
  tmdb_id: number;
  media_type: MediaType;
  position: number;
}
export interface TimecodeItem {
  tmdb_id: number;
  media_type: MediaType;
  season?: number | null;
  episode?: number | null;
  position_sec: number;
  duration_sec: number;
  updated_at: number;
}
export interface QueueItem {
  id: number;
  tmdb_id: number;
  media_type: MediaType;
  season: number | null;
  episode: number | null;
  position: number;
}
export interface Bootstrap {
  bookmarks: Bookmark[];
  playlists: Playlist[];
  timecodes: TimecodeItem[];
  settings: Record<string, string>;
  queue?: QueueItem[];
  cursor: number;
}
export interface AuthDevice {
  token_id: string;
  device_name: string;
  device_type: string;
  created_at: number;
  last_seen: number;
  current: boolean;
}
export interface AuthUser {
  id: number;
  login: string;
}

export type RemoteAction = 'toggle_play' | 'seek' | 'seek_to' | 'prev' | 'next' | 'mute' | 'night' | 'sleep' | 'volume';
export type RemoteStrAction = 'set_voice' | 'set_subtitle';
export interface OpenCmd {
  tmdb_id: number;
  media_type: MediaType;
  resume?: boolean;
  season?: number;
  episode?: number;
}
export type SendBody = { device_id: string } & (
  | { open: OpenCmd }
  | { remote: { action: RemoteAction; value?: number } }
  | { remote: { action: RemoteStrAction; str: string } }
  | { remote: { action: 'set_local'; key: LocalKey; str: 'true' | 'false' } }
);

// ---- image helper (posters arrive as "/img/w500/x.jpg"; /img is auth-free) ---

export function img(path: string | null | undefined, size: string): string {
  if (!path) return '';
  return path.startsWith('/img/') ? path.replace(/^\/img\/[^/]+\//, '/img/' + size + '/') : path;
}

// ---- endpoints ---------------------------------------------------------------

export const tgAuth = (init_data: string) => post<{ token: string; user: AuthUser }>('/tg/auth', { init_data });
export const getDevices = () => get<{ devices: Device[] }>('/tg/devices');
export const send = (body: SendBody) => post<unknown>('/tg/send', body);

export const getHome = () => get<HomeResponse>('/catalog/home', { lang });
export const search = (q: string) => get<ListResponse>('/catalog/search', { q, lang });
export const getTitle = (type: MediaType, id: number, season?: number) =>
  get<Card & SeasonResponse>('/catalog/title/' + id, { type, season, lang });

// Library rows are bare {tmdb_id, media_type}; the TV client enriches the same way.
// ponytail: N+1 title fetches (capped by callers); drop when the backend returns cards.
const titleCache = new Map<string, Promise<Card>>();
export function getTitleCached(type: MediaType, id: number): Promise<Card> {
  const k = lang + ':' + type + ':' + id;
  let p = titleCache.get(k);
  if (!p) {
    p = getTitle(type, id).catch((e) => {
      titleCache.delete(k);
      throw e;
    });
    titleCache.set(k, p);
  }
  return p;
}

export const getBootstrap = () => get<Bootstrap>('/sync/bootstrap');
export const addBookmark = (tmdb_id: number, media_type: MediaType) => post<Bookmark>('/bookmarks', { tmdb_id, media_type });
export const removeBookmark = (tmdb_id: number, media_type: MediaType) => del<void>('/bookmarks/' + tmdb_id, { media_type });
export const getPlaylistItems = (id: number) => get<{ items: PlaylistItem[] }>('/playlists/' + id + '/items');
// Watch queue (docs/miniapp.md).
export const addQueue = (tmdb_id: number, media_type: MediaType, season?: number | null, episode?: number | null) =>
  post<QueueItem>('/queue', { tmdb_id, media_type, season: season ?? undefined, episode: episode ?? undefined });
export const removeQueue = (id: number) => del<void>('/queue/' + id);
export const moveQueue = (id: number, position: number) => put<void>('/queue/' + id + '/move', { position });
export const clearQueue = () => del<void>('/queue');
export const putSetting = (key: string, value: string) => put<{ key: string; value: string }>('/settings/' + encodeURIComponent(key), { value });

export const getAuthDevices = () => get<{ devices: AuthDevice[] }>('/auth/devices');
export const revokeDevice = (token_id: string) => del<void>('/auth/devices/' + encodeURIComponent(token_id));
export const revokeOtherDevices = () => del<{ revoked: number }>('/auth/devices');
export const unlinkTelegram = () => del<void>('/telegram/link');
export interface TelegramLink {
  chat_id: number;
  first_name: string;
  username: string;
  created_at: number;
}
export const getTelegramLinks = () => get<{ links: TelegramLink[] }>('/telegram/links');
export const unlinkTelegramChat = (chatId: number) => del<void>('/telegram/links/' + chatId);
export const createTelegramLink = () => post<{ code: string; deep_link: string; expires_at: number }>('/telegram/link');
export const clearHistory = () => del<void>('/me/history');
export const deleteAllData = () => del<void>('/me/data');

export function wsUrl(): string {
  const proto = location.protocol === 'https:' ? 'wss:' : 'ws:';
  return proto + '//' + location.host + BASE + '/ws?t=' + encodeURIComponent(token || '');
}
