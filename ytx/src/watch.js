// Watch history + resume point on the YouTube account.
//
// Media is fetched anonymously (stream.js), so YouTube learns nothing from
// playback itself. What its own clients do is send two kinds of stats pings
// from the signed-in /player response's `playbackTracking`:
//   videostats_playback_url  — once per playback session (cpn): "watched",
//                              the video lands in FEhistory;
//   videostats_watchtime_url — periodically with st/et/cmt: watch time and
//                              the resume point (playbackStartConfig on the
//                              next /player, progress bar on history tiles).
// Promin's player reports its position through the Go server to
// POST /v1/accounts/:id/watch/:vid; one cpn per (profile, video) for 6 h.
//
// youtubei.js only adds the OAuth bearer to InnerTube calls, so the header is
// set by hand here (refreshing the token first when it is about to expire).

import { randomBytes } from 'node:crypto';
import { player } from './tv.js';

const SESSION_TTL_MS = 6 * 3600 * 1000;
const sessions = new Map(); // `${account}:${videoId}` → { cpn, lastEt, at }

function cpn() {
  // 16 chars of the URL-safe alphabet, like the players do.
  return randomBytes(12).toString('base64url').slice(0, 16);
}

async function authHeaders(yt) {
  const oauth = yt.session.oauth;
  if (oauth?.oauth2_tokens && oauth.shouldRefreshToken()) await oauth.refreshAccessToken();
  const h = { 'X-Goog-Visitor-Id': yt.session.context.client.visitorData || '' };
  if (oauth?.oauth2_tokens?.access_token) h.Authorization = 'Bearer ' + oauth.oauth2_tokens.access_token;
  return h;
}

function statsUrl(base, yt, params) {
  const u = new URL(base.replace('https://s.', 'https://www.'));
  const c = yt.session.context.client;
  u.searchParams.set('ver', '2');
  u.searchParams.set('c', String(c.clientName || 'TVHTML5').toLowerCase());
  u.searchParams.set('cver', c.clientVersion || '');
  u.searchParams.set('cbrver', c.clientVersion || '');
  for (const k of Object.keys(params)) u.searchParams.set(k, String(params[k]));
  return u;
}

// Reports a playback position. Returns what YouTube answered (2xx expected).
export async function watch(yt, account, videoId, positionSec, durationSec, log) {
  const pr = await player(yt, videoId);
  const pt = pr.playback_tracking;
  if (!pt?.videostats_watchtime_url) return { ok: false, reason: 'no playback tracking' };
  const key = account + ':' + videoId;
  const now = Date.now();
  let s = sessions.get(key);
  const first = !s || now - s.at > SESSION_TTL_MS;
  if (first) {
    s = { cpn: cpn(), lastEt: positionSec, at: now };
    sessions.set(key, s);
    if (sessions.size > 1000) for (const [k, v] of sessions) if (now - v.at > SESSION_TTL_MS) sessions.delete(k);
  }
  const headers = await authHeaders(yt);
  const pos = Math.max(0, positionSec).toFixed(3);
  const statuses = [];
  if (first && pt.videostats_playback_url) {
    const r = await fetch(statsUrl(pt.videostats_playback_url, yt, { cpn: s.cpn, cmt: pos, fmt: '137', rtn: '0', rt: '0', len: durationSec ? durationSec.toFixed(3) : '' }), { headers });
    statuses.push(r.status);
    await r.arrayBuffer().catch(() => {});
  }
  const r = await fetch(statsUrl(pt.videostats_watchtime_url, yt, {
    cpn: s.cpn,
    st: Math.min(s.lastEt, positionSec).toFixed(3),
    et: pos,
    cmt: pos,
    len: durationSec ? durationSec.toFixed(3) : '',
    state: 'playing',
    fmt: '137',
    rt: Math.round((now - s.at) / 1000),
  }), { headers });
  statuses.push(r.status);
  await r.arrayBuffer().catch(() => {});
  s.lastEt = positionSec;
  s.at = now;
  if (statuses.some((c) => c >= 400)) log.warn('watch ping rejected', { videoId, statuses });
  return { ok: statuses.every((c) => c < 400), statuses, cpn: s.cpn };
}
