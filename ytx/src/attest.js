// What makes a signed-in TV session's SABR stream survive: the TV app's own
// PO-token recipe, reconstructed from tv-player-ias.js (2026-09):
//
//   1. GET youtube.com/tv with the OAuth bearer (Cobalt UA) → ytcfg of the
//      signed-in TV app: LIVING_ROOM_PO_TOKEN_ID, LIVING_ROOM_EACR_TOKEN,
//      DATASYNC_ID, INNERTUBE_CONTEXT.client.tvAppInfo.
//   2. Every InnerTube request carries tvAppInfo.livingRoomPoTokenId
//      (required — without it the media server keeps StreamProtectionStatus
//      2 and cuts the stream after ~12 MB).
//   3. BotGuard challenge from /att/get (ENGAGEMENT_TYPE_UNBOUND, eacrToken),
//      WAA request key of the TV player ("Z1elNkAKLpSR3oPOUMSN"; the web
//      player's "O43z0dpjhgX20SCx4KAo" is refused for TV).
//   4. Session token bound to livingRoomPoTokenId (the player's HF():
//      livingRoomPoTokenId || datasyncId || visitor — datasync alone was
//      refused), attached to /player as serviceIntegrityDimensions.poToken
//      and to the SABR request. Measured: status 1, 40 MB in 33 s; every
//      other binding/key/challenge combination stayed at status 2.
//
// Per Innertube session: the ytcfg for ~6 h, the token for ~1 h, both
// dropped on invalidate() (a stream that still reports status 2).

import { PoMinter } from './potoken.js';

export const TV_UA = 'Mozilla/5.0 (ChromiumStylePlatform) Cobalt/25.lts.30.1034943-gold (unlike Gecko), Unknown_TV_Unknown_0/Unknown (Unknown, Unknown)';
const TV_REQUEST_KEY = 'Z1elNkAKLpSR3oPOUMSN';
const CFG_TTL_MS = 6 * 3600 * 1000;
const POT_TTL_MS = 60 * 60 * 1000;

let log = { info() {}, warn() {} };
export function setLogger(l) { log = l; }

const state = new WeakMap(); // yt → { cfg, cfgAt, minter, pot, potAt }

async function bearer(yt) {
  const oauth = yt.session.oauth;
  if (oauth?.oauth2_tokens && oauth.shouldRefreshToken()) await oauth.refreshAccessToken();
  return oauth?.oauth2_tokens?.access_token ? 'Bearer ' + oauth.oauth2_tokens.access_token : '';
}

function entry(yt) {
  let s = state.get(yt);
  if (!s) { s = { cfg: null, cfgAt: 0, minter: null, pot: '', potAt: 0 }; state.set(yt, s); }
  return s;
}

// The signed-in TV app's page config; also stamps tvAppInfo on the session.
export async function tvConfig(yt) {
  const s = entry(yt);
  if (s.cfg && s.cfgAt + CFG_TTL_MS > Date.now()) return s.cfg;
  const t0 = Date.now();
  const res = await fetch('https://www.youtube.com/tv', { headers: { 'User-Agent': TV_UA, Authorization: await bearer(yt), 'Accept-Language': 'en' } });
  const html = await res.text();
  const m = html.match(/ytcfg\.set\(({.+?})\);/s);
  if (!m) throw new Error('youtube.com/tv without ytcfg (status ' + res.status + ')');
  const cfg = JSON.parse(m[1]);
  if (!cfg.LIVING_ROOM_PO_TOKEN_ID) throw new Error('tv ytcfg without LIVING_ROOM_PO_TOKEN_ID (logged_in=' + cfg.LOGGED_IN + ')');
  const client = cfg.INNERTUBE_CONTEXT?.client || {};
  yt.session.context.client.tvAppInfo = Object.assign({}, client.tvAppInfo || {}, { livingRoomPoTokenId: cfg.LIVING_ROOM_PO_TOKEN_ID });
  s.cfg = cfg;
  s.cfgAt = Date.now();
  s.minter = null;
  s.pot = '';
  log.info('tv app config', { ms: Date.now() - t0, logged_in: cfg.LOGGED_IN, eacr: !!cfg.LIVING_ROOM_EACR_TOKEN });
  return cfg;
}

// The session PO token for this signed-in TV session.
export async function sessionPot(yt) {
  const s = entry(yt);
  if (s.pot && s.potAt + POT_TTL_MS > Date.now()) return s.pot;
  const cfg = await tvConfig(yt);
  if (!s.minter) {
    s.minter = new PoMinter({
      requestKey: TV_REQUEST_KEY,
      ytcfg: cfg,
      pageUrl: 'https://www.youtube.com/tv',
      userAgent: TV_UA,
      log,
      challenge: async () => {
        const args = { engagementType: 'ENGAGEMENT_TYPE_UNBOUND', parse: false };
        if (cfg.LIVING_ROOM_EACR_TOKEN) args.eacrToken = cfg.LIVING_ROOM_EACR_TOKEN;
        const r = await yt.actions.execute('/att/get', args);
        if (!r.data?.bgChallenge) throw new Error('att/get without bgChallenge');
        return r.data.bgChallenge;
      },
    });
  }
  s.pot = await s.minter.mint(cfg.LIVING_ROOM_PO_TOKEN_ID);
  s.potAt = Date.now();
  return s.pot;
}

// A stream still reporting status ≥ 2: forget everything so the next play
// fetches a fresh config, challenge and token.
export function invalidate(yt) {
  state.delete(yt);
}
