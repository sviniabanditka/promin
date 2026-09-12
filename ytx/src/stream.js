// One media track of a video over HTTP, pulled from YouTube through SABR.
//
// GET /stream/:vid/video|audio → fragments as the SabrStream produces them,
// chunked. Promin's remux job reads the two tracks as ffmpeg inputs and
// copy-muxes them into HLS — the same pipeline torrents already use.
//
// Who asks YouTube for the media: an *anonymous WEB client* with BotGuard PO
// tokens, not the profile's signed-in TV session. Measured 2026-09:
// - every client now serves adaptive formats through SABR only; the media
//   server flips StreamProtectionStatus to 2 and cuts the stream after
//   ~12 MB (~70 s of 1080p) unless the request carries a PO token it accepts;
// - a web-minted token is not accepted for the TV client (status stays 2);
// - the same token, video-id bound, with the WEB client → status 1, full
//   speed (60 MB in 23 s);
// - an old TV build ("TV_DOWNGRADED") still gets plain URLs, but googlevideo
//   caps them at ~10 MB as well (403), so that is no way out either.
// Feeds, history and subscriptions stay on the signed-in TV session; the
// price is that playback is not written to the account's history and
// age-restricted videos do not play (anonymous). The session (visitor data +
// session-bound token) is rebuilt every few hours or after an upstream error.

import { Innertube, UniversalCache, YTNodes, Constants } from 'youtubei.js';
import { SabrStream } from 'googlevideo/sabr-stream';
import { buildSabrFormat, EnabledTrackTypes } from 'googlevideo/utils';
import { PoTokens } from './potoken.js';
import { HttpError } from './util.js';

const QUALITIES = ['2160p', '1440p', '1080p', '720p', '480p', '360p'];
const SESSION_TTL_MS = 4 * 3600 * 1000;
const PLAYER_TTL_MS = 5 * 60 * 1000; // both tracks of one play share a player response

class Playback {
  constructor(log) {
    this.log = log;
    this.pot = new PoTokens(log);
    this.yt = null;
    this.ytUntil = 0;
    this.creating = null;
    this.players = new Map(); // videoId -> { pr, at }
  }

  session() {
    if (this.yt && this.ytUntil > Date.now()) return Promise.resolve(this.yt);
    if (!this.creating) this.creating = this.create().finally(() => { this.creating = null; });
    return this.creating;
  }

  async create() {
    const t0 = Date.now();
    const probe = await Innertube.create({ cache: new UniversalCache(false), generate_session_locally: true });
    const visitor = probe.session.context.client.visitorData;
    const sessionPot = await this.pot.token(visitor);
    this.yt = await Innertube.create({ cache: new UniversalCache(false), generate_session_locally: true, visitor_data: visitor, po_token: sessionPot });
    this.ytUntil = Date.now() + SESSION_TTL_MS;
    this.players.clear();
    this.log.info('playback session ready', { ms: Date.now() - t0 });
    return this.yt;
  }

  reset() {
    this.yt = null;
    this.ytUntil = 0;
    this.players.clear();
  }

  // /player as the web client, with the content-bound token; cached briefly.
  async player(videoId, reload) {
    if (!reload) {
      const hit = this.players.get(videoId);
      if (hit && hit.at + PLAYER_TTL_MS > Date.now()) return hit.pr;
    }
    const yt = await this.session();
    const ep = new YTNodes.NavigationEndpoint({ watchEndpoint: { videoId } });
    const args = {
      playbackContext: { contentPlaybackContext: { vis: 0, splay: false, lactMilliseconds: '-1', signatureTimestamp: yt.session.player?.signature_timestamp } },
      serviceIntegrityDimensions: { poToken: await this.pot.token(videoId) },
      contentCheckOk: true,
      racyCheckOk: true,
      parse: true,
    };
    if (reload) args.playbackContext.reloadPlaybackContext = reload;
    let pr;
    try {
      pr = await ep.call(yt.actions, args);
    } catch (e) {
      this.reset();
      throw new HttpError(502, 'youtube_upstream', String(e?.message || e).slice(0, 200));
    }
    const ps = pr.playability_status || {};
    if (ps.status !== 'OK') throw new HttpError(409, 'unplayable', ps.reason || ps.status || 'unplayable');
    if (!pr.streaming_data?.server_abr_streaming_url) throw new HttpError(502, 'no_sabr', 'no serverAbrStreamingUrl');
    if (!reload) this.players.set(videoId, { pr, at: Date.now() });
    return pr;
  }
}

let playback = null;

export async function openTrack(videoId, track, quality, log) {
  if (!QUALITIES.includes(quality)) quality = '1080p';
  if (!playback) playback = new Playback(log);
  const pr = await playback.player(videoId);
  const yt = await playback.session();
  const sd = pr.streaming_data;
  const abrUrl = await yt.session.player?.decipher(sd.server_abr_streaming_url);
  const ustreamer = pr.player_config?.media_common_config?.media_ustreamer_request_config?.video_playback_ustreamer_config;
  if (!abrUrl || !ustreamer) throw new HttpError(502, 'no_sabr', 'missing SABR parameters');

  const stream = new SabrStream({
    formats: sd.adaptive_formats.map(buildSabrFormat),
    serverAbrStreamingUrl: abrUrl,
    videoPlaybackUstreamerConfig: ustreamer,
    poToken: await playback.pot.token(videoId),
    clientInfo: {
      clientName: parseInt(Constants.CLIENT_NAME_IDS[yt.session.context.client.clientName]),
      clientVersion: yt.session.context.client.clientVersion,
    },
  });
  stream.on('reloadPlayerResponse', async (ctx) => {
    try {
      const p2 = await playback.player(videoId, ctx);
      const u = await yt.session.player?.decipher(p2.streaming_data?.server_abr_streaming_url);
      const c = p2.player_config?.media_common_config?.media_ustreamer_request_config?.video_playback_ustreamer_config;
      if (u && c) { stream.setStreamingURL(u); stream.setUstreamerConfig(c); }
    } catch (e) { log.warn('reload player failed', { videoId, error: String(e).slice(0, 200) }); }
  });
  stream.on('streamProtectionStatusUpdate', (s) => {
    // 2 = token not accepted (the stream will be cut in ~1 min), 3 = refused.
    if (s.status >= 2) log.warn('sabr stream protection', { videoId, track, status: s.status });
  });
  stream.on('error', (e) => log.warn('sabr error', { videoId, track, error: String(e).slice(0, 200) }));

  const { videoStream, audioStream, selectedFormats } = await stream.start({
    videoQuality: quality,
    audioQuality: 'AUDIO_QUALITY_MEDIUM',
    preferH264: true, preferMP4: true, preferOpus: false,
    enabledTrackTypes: track === 'audio' ? EnabledTrackTypes.AUDIO_ONLY : EnabledTrackTypes.VIDEO_ONLY,
  });
  const fmt = track === 'audio' ? selectedFormats.audioFormat : selectedFormats.videoFormat;
  return { stream, readable: track === 'audio' ? audioStream : videoStream, format: fmt, abort: () => stream.abort() };
}
