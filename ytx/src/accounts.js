// One YouTube account per Promin profile. The account store is a JSON file
// (refresh tokens are the credential — keep the volume private); sessions are
// signed-in youtubei.js TV clients kept in memory and rebuilt on demand.
//
// Sign-in is YouTube's device-code flow as the TV app: youtubei.js fetches the
// TV app's client identity from youtube.com/tv, emits the user code, polls
// for approval and hands us the tokens; refreshed tokens come back through
// `update-credentials` and are persisted.

import { promises as fs } from 'node:fs';
import path from 'node:path';
import { Innertube, ClientType, UniversalCache, Platform } from 'youtubei.js';
import { HttpError } from './util.js';

// The player's decipher code runs as real JS (Node), not youtubei.js's bundled
// interpreter, which is slower and lags behind player changes.
Platform.shim.eval = async (data, env) => {
  const props = [];
  if (env.n) props.push(`n: exportedVars.nFunction("${env.n}")`);
  if (env.sig) props.push(`sig: exportedVars.sigFunction("${env.sig}")`);
  return new Function(`${data.output}\nreturn { ${props.join(', ')} }`)();
};

export class Accounts {
  constructor(dataDir, log) {
    this.file = path.join(dataDir, 'accounts.json');
    this.log = log;
    this.store = {}; // profileId → { credentials, linked_at, name? }
    this.sessions = new Map(); // profileId → Innertube
    this.pending = new Map(); // profileId → { user_code, verification_url, expires_at, error? , yt }
  }

  async load() {
    try {
      this.store = JSON.parse(await fs.readFile(this.file, 'utf8'));
    } catch (e) {
      if (e.code !== 'ENOENT') throw e;
      this.store = {};
    }
  }

  async save() {
    await fs.mkdir(path.dirname(this.file), { recursive: true });
    const tmp = this.file + '.tmp';
    await fs.writeFile(tmp, JSON.stringify(this.store, null, 1), { mode: 0o600 });
    await fs.rename(tmp, this.file);
  }

  status(id) {
    const p = this.pending.get(id);
    const acc = this.store[id];
    return {
      linked: !!acc,
      linked_at: acc?.linked_at || null,
      pending: !!p && !p.error,
      user_code: p && !p.error ? p.user_code : undefined,
      verification_url: p && !p.error ? p.verification_url : undefined,
      expires_at: p && !p.error ? p.expires_at : undefined,
      error: p?.error,
    };
  }

  async newClient() {
    return Innertube.create({ client_type: ClientType.TV, cache: new UniversalCache(false), generate_session_locally: true });
  }

  // Start (or return the running) device-code flow for a profile.
  async startLogin(id) {
    const cur = this.pending.get(id);
    if (cur && !cur.error && cur.expires_at > Date.now()) return this.status(id);
    const yt = await this.newClient();
    const entry = { yt, user_code: null, verification_url: null, expires_at: 0, error: null };
    this.pending.set(id, entry);

    const codeReady = new Promise((resolve) => {
      yt.session.once('auth-pending', (d) => {
        entry.user_code = d.user_code;
        entry.verification_url = d.verification_url;
        entry.expires_at = Date.now() + (d.expires_in || 1800) * 1000;
        resolve();
      });
    });
    yt.session.once('auth-error', (e) => {
      entry.error = String(e?.message || e);
      this.log.warn('login error', { id, error: entry.error });
    });
    yt.session.once('auth', ({ credentials }) => {
      this.store[id] = { credentials, linked_at: Date.now() };
      this.save().catch((e) => this.log.error('save failed', { error: String(e) }));
      this.attach(id, yt);
      this.sessions.set(id, yt);
      this.pending.delete(id);
      this.log.info('account linked', { id });
    });
    // signIn() without credentials runs the device flow and resolves on approval.
    yt.session.signIn().catch((e) => {
      if (!entry.error) entry.error = String(e?.message || e);
    });
    await Promise.race([codeReady, new Promise((r) => setTimeout(r, 15000))]);
    if (!entry.user_code && !entry.error) entry.error = 'no device code from Google';
    return this.status(id);
  }

  attach(id, yt) {
    yt.session.on('update-credentials', ({ credentials }) => {
      if (!this.store[id]) return;
      this.store[id].credentials = credentials;
      this.save().catch((e) => this.log.error('save failed', { error: String(e) }));
    });
  }

  // Signed-in client for a profile; 404 when the profile has no account.
  async session(id) {
    const cached = this.sessions.get(id);
    if (cached) return cached;
    const acc = this.store[id];
    if (!acc) throw new HttpError(404, 'not_linked', 'this profile has no YouTube account');
    const yt = await this.newClient();
    this.attach(id, yt);
    try {
      await yt.session.signIn(acc.credentials);
    } catch (e) {
      throw new HttpError(502, 'signin_failed', String(e?.message || e));
    }
    this.sessions.set(id, yt);
    return yt;
  }

  async unlink(id) {
    const yt = this.sessions.get(id);
    this.sessions.delete(id);
    this.pending.delete(id);
    delete this.store[id];
    await this.save();
    if (yt) {
      try { await yt.session.signOut(); } catch (e) { /* revoke is best effort */ }
    }
  }

  // Drop a cached session so the next call rebuilds it (after a 401 from InnerTube).
  reset(id) {
    this.sessions.delete(id);
  }
}
