// ytx — Promin's YouTube sidecar. Internal HTTP API, no auth of its own:
// promin's Go server is the only caller (cluster network) and maps its
// profiles onto account ids.
//
//   POST   /v1/accounts/:id/login            start the TV device-code sign-in
//   GET    /v1/accounts/:id                  link status (+ pending user code)
//   DELETE /v1/accounts/:id                  unlink
//   GET    /v1/accounts/:id/browse/:page     home|subscriptions|history|playlists|library|trending|liked|watch_later|UC…|VL…  ?cont=
//   GET    /v1/accounts/:id/search?q=&cont=
//   GET    /v1/accounts/:id/video/:vid       details + related
//   GET    /v1/accounts/:id/stream/:vid/probe                         200 or 409 unplayable (media reachable?)
//   GET    /v1/accounts/:id/stream/:vid/:track?quality=1080p&start=SEC   video|audio track, chunked media
//   POST   /v1/accounts/:id/watch/:vid {position_sec, duration_sec}      history + resume point pings
//   GET    /healthz
//
// Env: YTX_ADDR (default :8091), YTX_DATA (default /data/ytx).

import http from 'node:http';
import { Accounts } from './accounts.js';
import { browse, search, video } from './tv.js';
import { openTrack, probe } from './stream.js';
import { watch } from './watch.js';
import { HttpError } from './util.js';

const ADDR = process.env.YTX_ADDR || ':8091';
const DATA = process.env.YTX_DATA || '/data/ytx';

const log = {
  info: (m, f = {}) => console.log(JSON.stringify({ level: 'info', msg: m, ...f, t: new Date().toISOString() })),
  warn: (m, f = {}) => console.warn(JSON.stringify({ level: 'warn', msg: m, ...f, t: new Date().toISOString() })),
  error: (m, f = {}) => console.error(JSON.stringify({ level: 'error', msg: m, ...f, t: new Date().toISOString() })),
};

const accounts = new Accounts(DATA, log);
await accounts.load();

function json(res, status, body) {
  const s = JSON.stringify(body);
  res.writeHead(status, { 'Content-Type': 'application/json; charset=utf-8', 'Content-Length': Buffer.byteLength(s), 'Cache-Control': 'no-store' });
  res.end(s);
}

const ID = /^[A-Za-z0-9_-]{1,64}$/;

async function route(req, res) {
  const url = new URL(req.url, 'http://ytx');
  const seg = url.pathname.split('/').filter(Boolean);
  if (req.method === 'GET' && url.pathname === '/healthz') return json(res, 200, { ok: true, accounts: Object.keys(accounts.store).length });
  if (seg[0] !== 'v1' || seg[1] !== 'accounts' || !seg[2] || !ID.test(seg[2])) throw new HttpError(404, 'not_found', 'no such route');
  const id = seg[2];
  const rest = seg.slice(3);

  if (rest.length === 0) {
    if (req.method === 'GET') return json(res, 200, accounts.status(id));
    if (req.method === 'DELETE') { await accounts.unlink(id); return json(res, 204, {}); }
  }
  if (rest[0] === 'login' && req.method === 'POST') return json(res, 200, await accounts.startLogin(id));
  if (rest[0] === 'watch' && rest[1] && req.method === 'POST') {
    const body = await readJson(req);
    const yt = await accounts.session(id);
    return json(res, 200, await watch(yt, id, rest[1], Number(body.position_sec) || 0, Number(body.duration_sec) || 0, log));
  }

  if (req.method !== 'GET') throw new HttpError(405, 'method', 'GET only');
  const yt = await accounts.session(id);
  const cont = url.searchParams.get('cont') || undefined;

  try {
    if (rest[0] === 'browse' && rest[1]) return json(res, 200, await browse(yt, rest[1], cont));
    if (rest[0] === 'search') return json(res, 200, await search(yt, url.searchParams.get('q') || '', cont));
    if (rest[0] === 'video' && rest[1]) return json(res, 200, await video(yt, rest[1]));
    if (rest[0] === 'stream' && rest[1] && rest[2] === 'probe') return json(res, 200, await probe(rest[1], log));
    if (rest[0] === 'stream' && rest[1] && (rest[2] === 'video' || rest[2] === 'audio')) return await streamTrack(req, res, rest[1], rest[2], url.searchParams.get('quality') || '1080p', Number(url.searchParams.get('start')) || 0);
  } catch (e) {
    // A rejected token: drop the cached session so the next call re-signs in.
    if (e instanceof HttpError && e.code === 'youtube_auth') accounts.reset(id);
    throw e;
  }
  throw new HttpError(404, 'not_found', 'no such route');
}

function readJson(req) {
  return new Promise((resolve, reject) => {
    let data = '';
    req.on('data', (c) => { data += c; if (data.length > 4096) { reject(new HttpError(413, 'too_large', 'body too large')); req.destroy(); } });
    req.on('end', () => { try { resolve(data ? JSON.parse(data) : {}); } catch { reject(new HttpError(400, 'bad_json', 'invalid JSON')); } });
    req.on('error', reject);
  });
}

async function streamTrack(req, res, vid, track, quality, startSec) {
  if (!/^[A-Za-z0-9_-]{11}$/.test(vid)) throw new HttpError(400, 'bad_id', 'video id');
  // Anonymous web playback (stream.js); the profile only has to be linked.
  const t = await openTrack(vid, track, quality, log, startSec);
  const mime = (t.format.mimeType || '').split(';')[0] || (track === 'audio' ? 'audio/mp4' : 'video/mp4');
  res.writeHead(200, { 'Content-Type': mime, 'Cache-Control': 'no-store', 'X-Ytx-Itag': String(t.format.itag || ''), 'X-Ytx-Quality': t.format.qualityLabel || '' });
  let bytes = 0;
  const started = Date.now();
  req.on('close', () => { t.abort(); log.info('track closed by client', { vid, track, bytes, ms: Date.now() - started }); });
  const reader = t.readable.getReader();
  try {
    for (;;) {
      const { value, done } = await reader.read();
      if (done) break;
      bytes += value.length;
      if (!res.write(Buffer.from(value))) await new Promise((r) => res.once('drain', r));
    }
  } catch (e) {
    log.warn('track read error', { vid, track, error: String(e).slice(0, 200) });
  } finally {
    res.end();
    log.info('track finished', { vid, track, bytes, ms: Date.now() - started });
  }
}

const server = http.createServer((req, res) => {
  route(req, res).catch((e) => {
    if (res.headersSent) { res.end(); return; }
    if (e instanceof HttpError) return json(res, e.status, { error: { code: e.code, message: e.message } });
    log.error('unhandled', { error: String(e?.stack || e).slice(0, 400) });
    json(res, 500, { error: { code: 'internal', message: 'internal error' } });
  });
});

const [host, port] = ADDR.startsWith(':') ? ['0.0.0.0', Number(ADDR.slice(1))] : ADDR.split(':');
server.listen(Number(port), host, () => log.info('ytx listening', { addr: ADDR, data: DATA }));
