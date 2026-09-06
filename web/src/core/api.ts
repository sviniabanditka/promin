// Thin HTTP client for the catalog API (docs/api.md). Adds the
// `lang` query param from i18n, applies a timeout, parses JSON, and
// normalizes errors. No async/await (ES5 target has no regenerator
// runtime bundled) — plain Promise chains only. Only ES5 Array/Object
// methods are used (no Array.find/includes/Object.assign) since core-js
// is not in the bundle.

import { getLang } from './i18n';
import { capsQuery, caps } from './capabilities';
import { getToken } from './auth';

const API_BASE = '/api/v1';
const DEFAULT_TIMEOUT = 12000;

// Called once when an authed request 401s (session died) — app.ts wires it to
// the PIN-gate bounce. Kept as a hook so api.ts doesn't import router/screens.
let deadSessionHook: (() => void) | null = null;
export function setDeadSessionHook(fn: () => void): void {
  deadSessionHook = fn;
}

// ---- normalized card DTO (docs/api.md) ---------------------------

export interface Timecode {
  position_sec: number;
  duration_sec: number;
  // tv: the episode the spot belongs to (shelf cards carry it so "Continue"
  // works even when the local timecode cache has evicted the record).
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
  // TMDB per-episode vote average (0..10); shown as a star badge in the list.
  rating?: number | null;
  timecode?: Timecode | null;
}

export interface Season {
  season: number;
  name: string;
  episode_count?: number;
  episodes?: Episode[] | null;
}

// One cast member for the title screen "Актори" lane. `photo` is a ready
// relative image path ("/img/w185/x.jpg") or null → rendered as a placeholder.
export interface Person {
  id: number;
  name: string;
  character?: string;
  photo?: string | null;
}

// One trailer/teaser (backend Card DTO). `key` is the YouTube video id;
// `youtube_url` is the ready watch URL. The backend puts the official
// trailer first, so the title screen just plays trailers[0].
export interface Trailer {
  key: string;
  name?: string;
  type?: string;
  lang?: string;
  youtube_url?: string;
}

export interface Card {
  tmdb_id: number;
  type: 'movie' | 'tv';
  title: string;
  original_title?: string;
  year?: number | null;
  rating?: number | null;
  // IMDb rating (0..10) from OMDb; absent or 0 when no key / N/A. Rendered as a
  // second rating badge next to the TMDB pill on the card.
  imdb_rating?: number | null;
  poster?: string | null;
  backdrop?: string | null;
  overview?: string;
  genres?: string[];
  runtime_minutes?: number | null;
  content_rating?: string;
  keywords?: string[];
  external_ids?: { [k: string]: string };
  timecode?: Timecode | null;
  in_bookmarks?: boolean;
  seasons?: Season[] | null;
  // Enrichment for the title screen (backend Card DTO): cast lane + lightweight
  // "similar" / "recommendations" card lanes (each a slim Card: tmdb_id, type,
  // title, year, poster, rating). Absent/empty → the lane is not rendered.
  cast?: Person[];
  similar?: Card[];
  recommendations?: Card[];
  // Trailers/teasers (YouTube). Absent/empty → the "Трейлер" button is hidden.
  // The first entry is the one the "Трейлер" button plays.
  trailers?: Trailer[];
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

export interface Genre {
  id: number;
  name: string;
}

export interface GenresResponse {
  genres: Genre[];
}

// Season episodes lazy-load response for the title screen. The backend may
// return either { season, episodes } or a full card with `seasons` — both
// are accepted by screens/title.ts. (Assumption: shape not pinned in docs/api.md.)
export interface SeasonResponse {
  season?: number;
  episodes?: Episode[];
  seasons?: Season[] | null;
}

// ---- online sources DTO (Phase 2 streaming) ----------------------------
//
// Two-step contract agreed with the backend Lampac facade:
//   1) GET /sources/online         → list of balancers (this screen)
//   2) GET /sources/online/resolve → concrete streams for one balancer
// (Note: this supersedes the single-shot shape sketched in docs/api.md; we
// write against the facade the backend agent is actually building.)

export interface OnlineSource {
  id: string;
  name: string;
  balanser: string;
  // Requires a companion/rpc-hoster round-trip on the backend; surfaced as a
  quality_note?: string;
}

export interface OnlineSourcesResponse {
  degraded?: boolean;
  sources: OnlineSource[];
}

export interface Stream {
  url: string; // already wrapped in same-origin /relay (or /remux) — play directly
  quality?: string;
  label?: string;
}

export interface Subtitle {
  url: string;
  lang?: string;
  label?: string;
}

export interface Voice {
  id: string;
  name: string;
}

export interface ResolveResponse {
  type: 'hls' | 'mp4';
  streams: Stream[];
  subtitles?: Subtitle[];
  voices?: Voice[];
  // id (from voices) of the dub the streams carry — requested or the source's
  // default. Lets the player checkmark what's actually playing.
  voice?: string;
  // Labels for HLS audio renditions in manifest order (Collaps ships rus0/rus1
  // in the manifest and the real dub names separately).
  audio_names?: string[];
  // Set instead of (non-empty) streams when the balancer couldn't be
  // Why streams is empty despite a 200: the provider could not resolve.
  unresolved?: string;
}

export interface ApiError {
  status: number;
  code?: string;
  message?: string;
}

export type QueryParams = { [k: string]: string | number | null | undefined };

// ---- image path helpers ------------------------------------------------

// Posters/backdrops arrive as ready relative paths like "/img/w500/x.jpg"
// or "/img/original/x.jpg". Rewrite the size segment when a different one
// is wanted (e.g. w1280 for the focus-driven backdrop).
export function imgSize(path: string | null | undefined, size: string): string {
  if (!path) {
    return '';
  }
  if (path.indexOf('/img/') === 0) {
    return path.replace(/^\/img\/[^/]+\//, '/img/' + size + '/');
  }
  return path;
}

// ---- request -----------------------------------------------------------

function buildQuery(params: QueryParams | undefined): string {
  const merged: QueryParams = {};
  if (params) {
    for (const k in params) {
      if (Object.prototype.hasOwnProperty.call(params, k)) {
        merged[k] = params[k];
      }
    }
  }
  if (merged.lang === undefined || merged.lang === null) {
    merged.lang = getLang();
  }

  const parts: string[] = [];
  for (const key in merged) {
    if (Object.prototype.hasOwnProperty.call(merged, key)) {
      const value = merged[key];
      if (value !== null && value !== undefined && value !== '') {
        parts.push(encodeURIComponent(key) + '=' + encodeURIComponent(String(value)));
      }
    }
  }
  return parts.length ? '?' + parts.join('&') : '';
}

// Build request headers, adding Authorization: Bearer <token> when a token is
// stored (docs/api.md — all endpoints except register/login require it). JSON
// body requests also set Content-Type.
function buildHeaders(hasBody: boolean): { [k: string]: string } {
  const h: { [k: string]: string } = { Accept: 'application/json' };
  if (hasBody) h['Content-Type'] = 'application/json';
  const token = getToken();
  if (token) h['Authorization'] = 'Bearer ' + token;
  return h;
}

// Core request: any verb, optional JSON body. Resolves parsed JSON (or null on
// 204/empty), rejects with a normalized ApiError carrying HTTP status + code.
function request<T>(method: string, path: string, params?: QueryParams, body?: unknown, timeoutMs?: number): Promise<T> {
  const url = API_BASE + path + buildQuery(params);
  const hasBody = body !== undefined && body !== null;
  const limit = timeoutMs && timeoutMs > 0 ? timeoutMs : DEFAULT_TIMEOUT;

  return new Promise<T>(function (resolve, reject) {
    let done = false;

    const timer = window.setTimeout(function () {
      if (done) return;
      done = true;
      reject({ status: 0, code: 'timeout', message: 'timeout' } as ApiError);
    }, limit);

    function fail(err: ApiError): void {
      if (done) return;
      done = true;
      window.clearTimeout(timer);
      reject(err);
    }

    const init: RequestInit = { method: method, headers: buildHeaders(hasBody) };
    if (hasBody) init.body = JSON.stringify(body);

    fetch(url, init)
      .then(function (res) {
        return res.text().then(function (text) {
          if (done) return;
          done = true;
          window.clearTimeout(timer);

          let data: unknown = null;
          if (text) {
            try {
              data = JSON.parse(text);
            } catch (e) {
              data = null;
            }
          }

          if (res.status >= 200 && res.status < 300) {
            resolve(data as T);
          } else {
            // A 401 on any authed surface means the session died → re-gate to the
            // PIN screen. Guarded by isLogged so the PIN screen's own wrong-PIN
            // 401 (not logged in) passes through to its shake handler.
            if (res.status === 401 && deadSessionHook && getToken() && path.indexOf('/auth/pin') === -1) {
              deadSessionHook();
            }
            const b = (data || {}) as { error?: { code?: string; message?: string } };
            const err = b.error || {};
            reject({ status: res.status, code: err.code, message: err.message } as ApiError);
          }
        });
      })
      .catch(function (e) {
        fail({ status: 0, code: 'network', message: String(e) });
      });
  });
}

export function get<T>(path: string, params?: QueryParams, timeoutMs?: number): Promise<T> {
  return request<T>('GET', path, params, undefined, timeoutMs);
}

export function post<T>(path: string, body?: unknown, params?: QueryParams, timeoutMs?: number): Promise<T> {
  return request<T>('POST', path, params, body === undefined ? {} : body, timeoutMs);
}

export function put<T>(path: string, body?: unknown, params?: QueryParams): Promise<T> {
  return request<T>('PUT', path, params, body === undefined ? {} : body);
}

export function del<T>(path: string, params?: QueryParams): Promise<T> {
  return request<T>('DELETE', path, params);
}

export function patch<T>(path: string, body?: unknown, params?: QueryParams): Promise<T> {
  return request<T>('PATCH', path, params, body === undefined ? {} : body);
}

// ---- typed endpoint wrappers (task endpoint list, aligned to docs/api.md) --

// Short memo: rail-hopping Каталог → Головна re-fetched home and rebuilt ~120
// cards every time. 60s keeps the continue-watching shelf honest after playback
// (Back from the player doesn't refetch anyway — the DOM is kept).
const HOME_MEMO_MS = 60 * 1000;
let homeMemo: { at: number; lang: string; res: HomeResponse } | null = null;
export function getHome(): Promise<HomeResponse> {
  const lang = getLang();
  if (homeMemo && homeMemo.lang === lang && Date.now() - homeMemo.at < HOME_MEMO_MS) {
    return Promise.resolve(homeMemo.res);
  }
  return get<HomeResponse>('/catalog/home').then(function (res) {
    homeMemo = { at: Date.now(), lang: lang, res: res };
    return res;
  });
}

export function getList(params: QueryParams): Promise<ListResponse> {
  return get<ListResponse>('/catalog/list', params);
}

export function search(params: QueryParams): Promise<ListResponse> {
  return get<ListResponse>('/catalog/search', params);
}

// Canon (docs/api.md): GET /catalog/title/{tmdb_id}?type=movie|tv — так реализован бэкенд.
// getTitle with a 5-minute in-memory cache, for screens that enrich a list of
// ids (Library's continue lane re-fetched up to 20 titles on EVERY Back). Plain
// getTitle stays uncached for the title screen, which wants fresh timecodes.
const titleCache: { [k: string]: { at: number; card: Card } } = {};
const TITLE_CACHE_MS = 5 * 60 * 1000;
export function getTitleCached(type: string, id: number | string): Promise<Card> {
  const k = type + ':' + id;
  const hit = titleCache[k];
  if (hit && Date.now() - hit.at < TITLE_CACHE_MS) return Promise.resolve(hit.card);
  return getTitle(type, id).then(function (card) {
    titleCache[k] = { at: Date.now(), card: card };
    return card;
  });
}

export function getTitle(type: string, id: number | string, params?: QueryParams): Promise<Card> {
  var p: QueryParams = { type: type };
  if (params) {
    for (var k in params) {
      if (Object.prototype.hasOwnProperty.call(params, k)) p[k] = params[k];
    }
  }
  return get<Card>('/catalog/title/' + encodeURIComponent(String(id)), p);
}

export function getTitleSeason(type: string, id: number | string, season: number): Promise<SeasonResponse> {
  return get<SeasonResponse>('/catalog/title/' + encodeURIComponent(String(id)), {
    type: type,
    season: season,
  });
}

// ---- online sources (Phase 2) ------------------------------------------

// Merge device capabilities into a params object without Object.assign.
function withCaps(params: QueryParams): QueryParams {
  const out: QueryParams = {};
  const caps = capsQuery();
  for (const c in caps) {
    if (Object.prototype.hasOwnProperty.call(caps, c)) out[c] = caps[c];
  }
  for (const k in params) {
    if (Object.prototype.hasOwnProperty.call(params, k)) out[k] = params[k];
  }
  return out;
}

// Метаданные тайтла обязательны: часть балансеров Lampac матчит только по
// title+original_title+year (filmix отдаёт 503 без них) или по imdb_id
// (collaps). Прокидываются из карточки TMDB через sources.ts.
export interface OnlineSourcesParams {
  tmdb_id: number | string;
  type: string;
  title?: string;
  original_title?: string;
  year?: number | null;
  imdb_id?: string | null;
  season?: number | null;
  episode?: number | null;
}

export function getOnlineSources(params: OnlineSourcesParams): Promise<OnlineSourcesResponse> {
  // Longer than the 12s default: the backend budgets ~20s Events + nav before
  // it answers, so a 12s client abort was the "first try fails, retry works" bug.
  return get<OnlineSourcesResponse>('/sources/online', withCaps(params as unknown as QueryParams), 14000);
}

export interface ResolveParams {
  balanser: string;
  tmdb_id: number | string;
  type: string;
  title?: string;
  original_title?: string;
  year?: number | null;
  imdb_id?: string | null;
  season?: number | null;
  episode?: number | null;
  voice?: string | null;
}

export function resolveOnline(params: ResolveParams): Promise<ResolveResponse> {
  // Backend resolve budget is ~25s; give the client 28s so it doesn't abort a
  // still-working resolve (the aborted attempt was why a manual retry "fixed" it).
  return get<ResolveResponse>('/sources/online/resolve', withCaps(params as unknown as QueryParams), 28000);
}

// ---- torrents (Phase 4) ------------------------------------------------
//
// Contract agreed with the backend agent (supersedes the docs/api.md sketch —
// we write against the facade actually being built):
//   GET    /sources/torrents  → { torrents:[...] } sorted by seeders desc
//   GET    /torrents/add?id=  → { infohash, files } (opaque magnet id → hash)
//   GET    /torrents/active   → { torrents:[...] } currently seeding on server
//   DELETE /torrents/{infohash}
//   GET    /stream/{infohash}/{fileIdx}  → progressive Range video (see below)

// One aggregated magnet. `id` is opaque (the magnet id the aggregator hands
// back) and is what /torrents/add consumes. size_human is authoritative for
// display; size (bytes) is optional. voices = translation/voice labels.
export interface Torrent {
  id: string;
  title: string;
  tracker?: string;
  size?: number;
  size_human?: string;
  seeders?: number;
  peers?: number;
  quality?: string;
  voices?: string[];
}

export interface TorrentsResponse {
  torrents: Torrent[];
}

// Same title-matching metadata the online tab passes (JacRed matches by
// title/original/year; season/episode narrow series packs).
export interface TorrentsParams {
  tmdb_id: number | string;
  type: string;
  title?: string;
  original_title?: string;
  year?: number | null;
  season?: number | null;
  episode?: number | null;
}

// A file inside an added torrent. `index` is the file index used by /stream.
export interface TorrentFile {
  index: number;
  name: string;
  size?: number;
  is_video?: boolean;
}

export interface AddTorrentResponse {
  infohash: string;
  files: TorrentFile[];
}

// A torrent currently active on the server ("Мої торренти" screen). Fields
// beyond infohash are best-effort — the backend may or may not include files.
export interface ActiveTorrent {
  infohash: string;
  name?: string;
  title?: string;
  size?: number;
  size_human?: string;
  // 0..1 download progress.
  progress?: number;
  // whole torrent already cached/downloaded.
  cached?: boolean;
  files?: TorrentFile[];
}

export interface ActiveTorrentsResponse {
  torrents: ActiveTorrent[];
}

export function getTorrents(params: TorrentsParams): Promise<TorrentsResponse> {
  return get<TorrentsResponse>('/sources/torrents', params as unknown as QueryParams, 15000);
}

export function addTorrent(id: string): Promise<AddTorrentResponse> {
  // Adding a magnet fetches metadata from the swarm (backend budget ~35s).
  return get<AddTorrentResponse>('/torrents/add', { id: id }, 40000);
}

export function getActiveTorrents(): Promise<ActiveTorrentsResponse> {
  return get<ActiveTorrentsResponse>('/torrents/active');
}

export function deleteTorrent(infohash: string): Promise<void> {
  return del<void>('/torrents/' + encodeURIComponent(infohash));
}

// /genres is not in docs/api.md; assumed shape { genres: [{id,name}] } and
// accepts an optional `type`. (Assumption.)
// Random catalog backdrops for the idle screensaver (whole catalog, not the
// home shelves).
export interface Backdrop {
  url: string;
  title: string;
  year?: number;
}
export interface BackdropsResponse {
  backdrops: Backdrop[];
}
export function getBackdrops(limit?: number): Promise<BackdropsResponse> {
  const p: QueryParams = {};
  if (limit != null) p.limit = limit;
  return get<BackdropsResponse>('/catalog/backdrops', p);
}

// Screensaver forecast strip. available=false when no location could be
// resolved or the provider is down — the strip is simply omitted.
export interface WeatherDay {
  date: string;
  code: number;
  min: number;
  max: number;
}
export interface WeatherResponse {
  available: boolean;
  forecast?: {
    place: string;
    now: number;
    code: number;
    days: WeatherDay[];
  };
}
export function getWeather(): Promise<WeatherResponse> {
  return get<WeatherResponse>('/weather');
}

// Genres change essentially never; the catalog refetched them on every mount
// and every type switch.
const genresMemo: { [k: string]: GenresResponse } = {};
export function getGenres(type?: string): Promise<GenresResponse> {
  const k = (type || '') + ':' + getLang();
  if (genresMemo[k]) return Promise.resolve(genresMemo[k]);
  return get<GenresResponse>('/catalog/genres', type ? { type: type } : undefined).then(function (res) {
    genresMemo[k] = res;
    return res;
  });
}

// ---- media URL token helper (docs/api.md) -------------------------------
//
// <video>/<track>/<img> can't send an Authorization header, so same-origin
// media endpoints (/relay, /remux, /stream) accept the token as ?t=<token>.
// The backend usually pre-stamps stream_url with ?t=, so this is a safety net:
// only appends when a token exists and none is already present.
export function mediaUrl(url: string): string {
  if (!url) return url;
  const token = getToken();
  if (!token) return url;
  if (url.indexOf('/relay') === 0 || url.indexOf('/remux') === 0 || url.indexOf('/stream') === 0) {
    // Match the token param specifically — a bare 't=' also matches ?st=/format=ts
    // etc., which would skip stamping and 401 the media request.
    if (/[?&]t=/.test(url)) return url;
    return url + (url.indexOf('?') !== -1 ? '&' : '?') + 't=' + encodeURIComponent(token);
  }
  return url;
}

// Progressive torrent stream URL for a <video> src (docs/api.md). The token is
// stamped on by mediaUrl since <video> can't send an Authorization header. The
// backend picks the container/codec from the device caps we pass here:
//   - mkv=false  → old webview can't demux MKV → backend copy-remuxes to HLS.
//   - hevc=false → TV has no HEVC/AV1 decoder → backend ffprobes and, if the
//     file is HEVC/AV1, transcodes to H264 HLS (docs/streaming.md).
// Either way the URL stays the /stream path; the backend 302-redirects to the
// remux/transcode HLS playlist when needed.
export function streamUrl(infohash: string, fileIdx: number, transcode?: boolean, hdr?: boolean): string {
  let u = '/stream/' + encodeURIComponent(infohash) + '/' + fileIdx;
  const params: string[] = [];
  if (!caps.mkv) params.push('mkv=false');
  // transcode=1 → server transcodes video to H264 (needed when the release is
  // HEVC/AV1 and this device's engine can't decode it — decided client-side
  // from the release name, so the server doesn't ffprobe on the request path
  // and race the torrent download). hdr=1 asks for HDR→SDR tone-mapping.
  if (transcode) {
    params.push('transcode=1');
    if (hdr) params.push('hdr=1');
  }
  // audio=0 = first track by default; the player rewrites this on a track
  // switch (server re-muxes the selected track inline — the bundled hls.js
  // can't play HLS alternate-audio renditions).
  params.push('audio=0');
  if (params.length) u += '?' + params.join('&');
  return mediaUrl(u);
}

// One selectable audio track of a torrent file (GET /torrents/audio).
export interface AudioTrackInfo {
  index: number;
  lang?: string;
  title?: string;
}
export interface TorrentAudioResponse {
  tracks: AudioTrackInfo[];
}
export function getTorrentAudio(infohash: string, fileIdx: number): Promise<TorrentAudioResponse> {
  return get<TorrentAudioResponse>('/torrents/audio', { infohash: infohash, file: fileIdx }, 12000);
}

// ---- auth (docs/api.md) -------------------------------------------------

export interface AuthUser {
  id: number;
  login: string;
  is_admin?: boolean;
}

export interface AuthResponse {
  token: string;
  user: AuthUser;
}

export interface PinBody {
  pin: string;
  device_name?: string;
  device_type?: string;
}
export function authPin(body: PinBody): Promise<AuthResponse> {
  return post<AuthResponse>('/auth/pin', body);
}

export function authLogout(): Promise<void> {
  return post<void>('/auth/logout');
}

// ---- bookmarks (docs/api.md) --------------------------------------------
//
// GET returns minimal rows per docs/api.md; docs/frontend.md §"Избранное" says the same
// endpoint yields normalized cards for the bookmarks grid. We read tolerantly:
// each row may be a bare {tmdb_id, media_type, added_at} or a full Card.

export interface Bookmark {
  tmdb_id: number;
  media_type: string;
  added_at?: number;
  // present when the backend enriches with catalog data (docs/frontend.md)
  title?: string;
  poster?: string | null;
  backdrop?: string | null;
  year?: number | null;
  rating?: number | null;
}

export interface BookmarksResponse {
  bookmarks?: Bookmark[];
  items?: Bookmark[];
}

export function getBookmarks(): Promise<BookmarksResponse> {
  return get<BookmarksResponse>('/bookmarks');
}

export function addBookmark(tmdbId: number, mediaType: string): Promise<unknown> {
  return post<unknown>('/bookmarks', { tmdb_id: tmdbId, media_type: mediaType });
}

export function removeBookmark(tmdbId: number, mediaType: string): Promise<void> {
  return del<void>('/bookmarks/' + encodeURIComponent(String(tmdbId)), { media_type: mediaType });
}


// ---- timecodes (docs/api.md) --------------------------------------------
//
// docs/api.md uses POST /timecodes (upsert, last-write-wins on updated_at) and
// GET /timecodes/{id}?media_type=&season=&episode=. The task summary sketched
// a PUT + /continue variant, but the backend follows docs/api.md — we match that.

export interface TimecodeRecord {
  position_sec: number;
  duration_sec: number;
  updated_at?: number;
}

export interface TimecodeUpsertBody {
  tmdb_id: number;
  media_type: string;
  season?: number | null;
  episode?: number | null;
  position_sec: number;
  duration_sec: number;
  updated_at: number;
}

export interface TimecodeUpsertResponse {
  accepted: boolean;
  position_sec: number;
  updated_at: number;
  duration_sec?: number;
}

export function getTimecode(
  tmdbId: number,
  mediaType: string,
  season?: number | null,
  episode?: number | null
): Promise<TimecodeRecord> {
  const p: QueryParams = { media_type: mediaType };
  if (season != null) p.season = season;
  if (episode != null) p.episode = episode;
  return get<TimecodeRecord>('/timecodes/' + encodeURIComponent(String(tmdbId)), p);
}

export function upsertTimecode(body: TimecodeUpsertBody): Promise<TimecodeUpsertResponse> {
  return post<TimecodeUpsertResponse>('/timecodes', body);
}

// Continue-watching: recently-played, unfinished items (server orders by
// updated_at). Bare {tmdb_id, media_type, season, episode, position/duration}
// — no poster/title, so the Library lane enriches each via getTitle.
export interface ContinueItem {
  tmdb_id: number;
  media_type: string;
  season?: number | null;
  episode?: number | null;
  position_sec: number;
  duration_sec: number;
  updated_at?: number;
}
export interface ContinueResponse {
  items: ContinueItem[];
}

export function getContinue(limit?: number): Promise<ContinueResponse> {
  const p: QueryParams = {};
  if (limit != null) p.limit = limit;
  return get<ContinueResponse>('/timecodes/continue', p);
}

// ---- settings (docs/api.md) ---------------------------------------------

export interface SettingsResponse {
  settings: { [k: string]: string };
}

export function getSettings(): Promise<SettingsResponse> {
  return get<SettingsResponse>('/settings');
}

export function putSetting(key: string, value: string): Promise<unknown> {
  return put<unknown>('/settings/' + encodeURIComponent(key), { value: value });
}

// ---- sync push channel (docs/api.md) ------------------------------------

export interface SyncEvent {
  type: string;
  id: number;
  payload: { [k: string]: unknown };
}

export interface SyncEventsResponse {
  events: SyncEvent[];
  cursor: number;
}

// History rows as carried by the sync bootstrap (the history endpoints
// themselves have no client-side reader and were removed).
export interface HistoryItem {
  tmdb_id: number;
  media_type: string;
  season?: number | null;
  episode?: number | null;
  watched_at?: number;
}

export interface SyncBootstrap {
  bookmarks?: Bookmark[];
  history?: HistoryItem[];
  timecodes?: Array<TimecodeRecord & { tmdb_id: number; media_type: string; season?: number | null; episode?: number | null }>;
  settings?: { [k: string]: string };
  cursor: number;
}

export function getSyncEvents(since: number): Promise<SyncEventsResponse> {
  return get<SyncEventsResponse>('/sync/events', { since: since });
}

export function getSyncBootstrap(): Promise<SyncBootstrap> {
  return get<SyncBootstrap>('/sync/bootstrap');
}

// ---- playlists (docs/api.md) --------------------------------------------

export interface Playlist {
  id: number;
  name: string;
  items_count?: number;
  updated_at?: number;
}

export interface PlaylistsResponse {
  playlists: Playlist[];
}

// Items carry only ids per docs/api.md; the playlist-items screen enriches each
// with catalog data (title/poster) via getTitle on demand.
export interface PlaylistItem {
  id: number;
  tmdb_id: number;
  media_type: string;
  position?: number;
}

export interface PlaylistItemsResponse {
  items: PlaylistItem[];
}

export function getPlaylists(): Promise<PlaylistsResponse> {
  return get<PlaylistsResponse>('/playlists');
}

export function createPlaylist(name: string): Promise<Playlist> {
  return post<Playlist>('/playlists', { name: name });
}

export function renamePlaylist(id: number, name: string): Promise<Playlist> {
  return patch<Playlist>('/playlists/' + encodeURIComponent(String(id)), { name: name });
}

export function deletePlaylist(id: number): Promise<void> {
  return del<void>('/playlists/' + encodeURIComponent(String(id)));
}

export function getPlaylistItems(id: number): Promise<PlaylistItemsResponse> {
  return get<PlaylistItemsResponse>('/playlists/' + encodeURIComponent(String(id)) + '/items');
}

export function addPlaylistItem(id: number, tmdbId: number, mediaType: string): Promise<unknown> {
  return post<unknown>('/playlists/' + encodeURIComponent(String(id)) + '/items', {
    tmdb_id: tmdbId,
    media_type: mediaType,
  });
}

export function removePlaylistItem(id: number, itemId: number): Promise<void> {
  return del<void>(
    '/playlists/' + encodeURIComponent(String(id)) + '/items/' + encodeURIComponent(String(itemId))
  );
}

// ---- devices / sessions (docs/api.md) -----------------------------------

export interface Device {
  token_id: string;
  device_name?: string;
  device_type?: string;
  created_at?: number;
  last_seen?: number;
  current?: boolean;
}

export interface DevicesResponse {
  devices: Device[];
}

export function getDevices(): Promise<DevicesResponse> {
  return get<DevicesResponse>('/auth/devices');
}

// Revoking the current session needs ?force=true (docs/api.md — a guard against
// self-eviction); the devices screen never passes force for the current row.
export function deleteDevice(tokenId: string, force?: boolean): Promise<void> {
  return del<void>('/auth/devices/' + encodeURIComponent(tokenId), force ? { force: 'true' } : undefined);
}

// ---- ping / version ----------------------------------------------------
//
// /api/v1/ping is not pinned in docs/api.md; assumed to return { version } (and
// maybe { status }). The settings screen shows the version and tolerates a
// missing field / failed request. (Assumption.)
export interface PingResponse {
  version?: string;
  status?: string;
}

export function getPing(): Promise<PingResponse> {
  return get<PingResponse>('/ping');
}

// ---- Settings → Danger zone ----------------------------------------------
export function clearMyHistory(): Promise<void> {
  return del<void>('/me/history');
}
// Wipes bookmarks/playlists/history/timecodes/settings and revokes EVERY
// session — the caller must drop the local session and show the PIN gate.
export function deleteMyData(): Promise<void> {
  return del<void>('/me/data');
}

// ---- Telegram companion bot ----------------------------------------------
export interface TelegramStatus {
  enabled: boolean;
  linked: boolean;
  bot_username: string;
}
export interface TelegramLink {
  code: string;
  deep_link: string;
  expires_at: number;
  qr?: string; // data:image/png;base64,… of the deep link
}
export function getTelegramStatus(): Promise<TelegramStatus> {
  return get<TelegramStatus>('/telegram/status');
}
export function createTelegramLink(): Promise<TelegramLink> {
  return post<TelegramLink>('/telegram/link');
}
export function unlinkTelegram(): Promise<void> {
  return del<void>('/telegram/link');
}

// ---- player state → server (Telegram Mini App remote reads it) ----------
export interface PlayerStateReport {
  closed?: boolean;
  tmdb_id?: number | string;
  media_type?: string;
  title?: string;
  season?: number | null;
  episode?: number | null;
  position_sec?: number;
  duration_sec?: number;
  paused?: boolean;
  voice?: string;
}
export function postPlayerState(state: PlayerStateReport): Promise<void> {
  return post<void>('/player/state', state, undefined, 5000);
}

// Sign out every other device of the profile (Settings → account).
export function revokeOtherDevices(): Promise<{ revoked: number }> {
  return del<{ revoked: number }>('/auth/devices');
}
