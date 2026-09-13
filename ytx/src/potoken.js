// Proof-of-Origin token minter (BotGuard / WebPO), the way YouTube's own
// players do it, run in Node with jsdom standing in for the browser
// (bgutils-js examples/index-innertube.ts):
//   challenge (program + interpreter) → BotGuard VM → snapshot →
//   WAA GenerateIT (integrity token, ~12 h) → mint tokens per binding.
// Which challenge, which WAA request key and what to bind to is the
// caller's business — attest.js knows the TV app's answers. A minter is
// re-created when its integrity token nears its TTL.

import { BotGuardClient } from 'bgutils-js/botguard';
import { buildURL, getHeaders, USER_AGENT } from 'bgutils-js/utils';
import { WebPoMinter } from 'bgutils-js/webpo';
import { JSDOM, VirtualConsole } from 'jsdom';

const MINTER_MARGIN_MS = 10 * 60 * 1000;

// One DOM per process; the VM reads window/document/navigator and yt.config_.
let dom = null;
function installDom(url, userAgent, ytcfg) {
  if (!dom) {
    dom = new JSDOM('<!DOCTYPE html><html lang="en"><head><title></title></head><body></body></html>', {
      url: url || 'https://www.youtube.com',
      referrer: 'https://www.youtube.com/',
      userAgent: userAgent || USER_AGENT,
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
  if (ytcfg) {
    dom.window.yt = { config_: ytcfg };
    globalThis.yt = dom.window.yt;
  }
}

// opts: { requestKey, challenge: async () => bgChallenge, ytcfg?, pageUrl?, userAgent?, log }
export class PoMinter {
  constructor(opts) {
    this.opts = opts;
    this.minter = null;
    this.until = 0;
    this.creating = null;
  }

  invalidate() {
    this.minter = null;
    this.until = 0;
  }

  async mint(binding) {
    const m = await this.get();
    return m.mintAsWebsafeString(binding);
  }

  get() {
    if (this.minter && this.until > Date.now()) return Promise.resolve(this.minter);
    if (!this.creating) this.creating = this.create().finally(() => { this.creating = null; });
    return this.creating;
  }

  async create() {
    const t0 = Date.now();
    const { requestKey, challenge: getChallenge, ytcfg, pageUrl, userAgent, log } = this.opts;
    installDom(pageUrl, userAgent, ytcfg);
    const challenge = await getChallenge();
    let interpreter = challenge.interpreterJavascript?.privateDoNotAccessOrElseSafeScriptWrappedValue;
    if (!interpreter) {
      const url = challenge.interpreterUrl?.privateDoNotAccessOrElseTrustedResourceUrlWrappedValue;
      if (!url) throw new Error('botguard: no interpreter');
      interpreter = await (await fetch(url.startsWith('//') ? 'https:' + url : url, { headers: { 'user-agent': userAgent || USER_AGENT } })).text();
    }
    new Function(interpreter)();
    const client = await BotGuardClient.create({ program: challenge.program, globalName: challenge.globalName || 'trayride', globalObject: globalThis });
    const webPoSignalOutput = [];
    const botguardResponse = await client.snapshot({ webPoSignalOutput });
    const res = await fetch(buildURL('GenerateIT', true), {
      method: 'POST',
      headers: getHeaders(),
      body: JSON.stringify([requestKey, botguardResponse]),
    });
    if (!res.ok) throw new Error('botguard: GenerateIT ' + res.status);
    const [integrityToken, estimatedTtlSecs, mintRefreshThreshold, websafeFallbackToken] = await res.json();
    this.minter = await WebPoMinter.create({ integrityToken, estimatedTtlSecs, mintRefreshThreshold, websafeFallbackToken }, webPoSignalOutput);
    this.until = Date.now() + Math.max(60_000, (estimatedTtlSecs || 3600) * 1000 - MINTER_MARGIN_MS);
    log?.info('potoken minter ready', { ms: Date.now() - t0, ttl_s: estimatedTtlSecs });
    return this.minter;
  }
}
