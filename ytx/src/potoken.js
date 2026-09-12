// Proof-of-Origin tokens (BotGuard / WebPO) for the web client.
//
// Without one the SABR media server flips StreamProtectionStatus to 2 and
// cuts the stream after ~12 MB. YouTube's web player mints them from a
// BotGuard VM; we do the same in Node with jsdom standing in for the browser
// (bgutils-js examples/index-innertube.ts). Two bindings are used: the
// visitor data (session token, goes into the Innertube session and the
// /player request) and the video id (content token, goes into the SABR
// request). One minter per process, re-created when the integrity token
// nears its TTL; tokens cached per binding.

import { BotGuardClient } from 'bgutils-js/botguard';
import { buildURL, getHeaders, parseLooseJSON, USER_AGENT } from 'bgutils-js/utils';
import { WebPoMinter } from 'bgutils-js/webpo';
import { JSDOM, VirtualConsole } from 'jsdom';

const REQUEST_KEY = 'O43z0dpjhgX20SCx4KAo'; // YouTube web player's WAA key
const TOKEN_TTL_MS = 6 * 3600 * 1000;
const MINTER_MARGIN_MS = 10 * 60 * 1000;

// The BotGuard program expects a YouTube page: a DOM, `yt.config_` (it reads
// EVENT_ID) and the challenge embedded in that very page (`ytAtN`). This is
// what bgutils-js's own Node example does; a WAA-fetched challenge without
// the page context minted tokens the media server ignored (status 2 stayed).
let dom = null;
async function loadPage() {
  const res = await fetch('https://www.youtube.com', {
    headers: { accept: '*/*', 'accept-language': 'en-US,en;q=0.7', 'user-agent': USER_AGENT },
  });
  const html = await res.text();
  const cfg = html.match(/ytcfg\.set\(({.+?})\);/s)?.[1];
  const att = html.match(/window\.ytAtN\(\s*({[\s\S]*?})\s*\)/)?.[1];
  if (!cfg || !att) throw new Error('botguard: youtube.com page without ytcfg/ytAtN');
  if (!dom) {
    dom = new JSDOM('<!DOCTYPE html><html lang="en"><head><title></title></head><body></body></html>', {
      url: 'https://www.youtube.com',
      referrer: 'https://www.youtube.com/',
      userAgent: USER_AGENT,
      virtualConsole: new VirtualConsole(), // silence "canvas not implemented"
    });
    Object.assign(globalThis, {
      window: dom.window,
      document: dom.window.document,
      location: dom.window.location,
      origin: dom.window.origin,
    });
    if (!('navigator' in globalThis)) Object.defineProperty(globalThis, 'navigator', { value: dom.window.navigator });
  }
  dom.window.yt = { config_: JSON.parse(cfg) };
  globalThis.yt = dom.window.yt;
  const challenge = parseLooseJSON(att).R?.bgChallenge;
  if (!challenge) throw new Error('botguard: no bgChallenge in page');
  return challenge;
}

export class PoTokens {
  constructor(log) {
    this.log = log;
    this.minter = null;
    this.minterUntil = 0;
    this.minting = null;
    this.cache = new Map(); // binding -> { token, until }
  }

  async token(binding) {
    const hit = this.cache.get(binding);
    if (hit && hit.until > Date.now()) return hit.token;
    const minter = await this.getMinter();
    const token = await minter.mintAsWebsafeString(binding);
    this.cache.set(binding, { token, until: Date.now() + TOKEN_TTL_MS });
    if (this.cache.size > 500) {
      for (const [k, v] of this.cache) if (v.until < Date.now()) this.cache.delete(k);
    }
    return token;
  }

  getMinter() {
    if (this.minter && this.minterUntil > Date.now()) return Promise.resolve(this.minter);
    if (!this.minting) {
      this.minting = this.createMinter().finally(() => { this.minting = null; });
    }
    return this.minting;
  }

  async createMinter() {
    const t0 = Date.now();
    const challenge = await loadPage();
    let interpreter = challenge.interpreterJavascript?.privateDoNotAccessOrElseSafeScriptWrappedValue;
    if (!interpreter) {
      const url = challenge.interpreterUrl?.privateDoNotAccessOrElseTrustedResourceUrlWrappedValue;
      if (!url) throw new Error('botguard: no interpreter');
      interpreter = await (await fetch(url.startsWith('//') ? 'https:' + url : url, { headers: { 'user-agent': USER_AGENT } })).text();
    }
    new Function(interpreter)();
    const client = await BotGuardClient.create({ program: challenge.program, globalName: challenge.globalName, globalObject: globalThis });
    const webPoSignalOutput = [];
    const botguardResponse = await client.snapshot({ webPoSignalOutput });
    const res = await fetch(buildURL('GenerateIT', true), {
      method: 'POST',
      headers: getHeaders(),
      body: JSON.stringify([REQUEST_KEY, botguardResponse]),
    });
    if (!res.ok) throw new Error('botguard: GenerateIT ' + res.status);
    const [integrityToken, estimatedTtlSecs, mintRefreshThreshold, websafeFallbackToken] = await res.json();
    this.minter = await WebPoMinter.create({ integrityToken, estimatedTtlSecs, mintRefreshThreshold, websafeFallbackToken }, webPoSignalOutput);
    this.minterUntil = Date.now() + Math.max(60_000, (estimatedTtlSecs || 3600) * 1000 - MINTER_MARGIN_MS);
    this.cache.clear();
    this.log.info('potoken minter ready', { ms: Date.now() - t0, ttl_s: estimatedTtlSecs });
    return this.minter;
  }
}
