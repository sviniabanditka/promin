// One media track of a video over HTTP, pulled from YouTube through SABR by
// the profile's signed-in TV session.
//
// GET /stream/:vid/video|audio → fragments as the SabrStream produces them,
// chunked. Promin's remux job reads the two tracks as ffmpeg inputs and
// copy-muxes them into HLS — the same pipeline torrents already use.
//
// Every client is SABR-only now (server-side ABR over UMP), and the media
// server cuts a stream after ~12 MB unless it carries a PO token it accepts.
// attest.js mints the one the TV app itself would present (bound to the
// account's living-room token id); /player is made with it (tv.js) and the
// SABR request carries it too. The signed-in TV client is not bot-checked
// from the VPS, unlike anonymous web clients. History, resume and
// age-restricted videos come with the account.

import { SabrStream } from 'googlevideo/sabr-stream';
import { buildSabrFormat, EnabledTrackTypes } from 'googlevideo/utils';
import { Constants } from 'youtubei.js';
import { player } from './tv.js';
import { sessionPot, invalidate } from './attest.js';
import { HttpError } from './util.js';

const QUALITIES = ['2160p', '1440p', '1080p', '720p', '480p', '360p'];

// SabrStream has no seek API: its request position is the total duration of
// the segments it has downloaded. To begin at `startMs` we hand it a restore
// state whose formats already "have" one phantom segment of that length and
// no buffered ranges, so the first request asks for startMs. Two details:
// the server sends the init segment only when the request names no
// "initialized" formats (`prepareFormatSelections`), so that first request is
// built as if the map were empty; and the server's real format metadata
// replaces our entries, so the phantom is re-added on `formatInitialization`.
function resumeState(stream, options, startMs, durationMs) {
  const { videoFormat, audioFormat } = stream.selectFormats(options);
  const prepare = stream.prepareFormatSelections.bind(stream);
  let first = true;
  stream.prepareFormatSelections = (formats, ranges) => {
    if (!first) return prepare(formats, ranges);
    first = false;
    const saved = stream.initializedFormatsMap;
    stream.initializedFormatsMap = new Map();
    try { return prepare(formats, ranges); } finally { stream.initializedFormatsMap = saved; }
  };
  const phantom = () => [-1, { segmentNumber: -1, durationMs: String(startMs) }];
  const fake = (f) => ({
    formatKey: `${f.itag}:${f.xtags || ''}`,
    formatInitializationMetadata: { formatId: { itag: f.itag, lastModified: f.lastModified, xtags: f.xtags }, mimeType: f.mimeType, durationUnits: '0', durationTimescale: '0' },
    downloadedSegments: [phantom()],
    lastMediaHeaders: [],
  });
  stream.on('formatInitialization', (f) => { const [k, v] = phantom(); f.downloadedSegments.set(k, v); });
  // The end-of-stream audit expects segments from 0; with a phantom it would
  // report the skipped range as an error after all data was delivered.
  stream.validateDownloadedSegments = () => {};
  return { durationMs, playerTimeMs: startMs, initializedFormats: [fake(videoFormat), fake(audioFormat)] };
}

function playable(pr) {
  const ps = pr.playability_status || {};
  if (ps.status !== 'OK') throw new HttpError(409, 'unplayable', ps.reason || ps.status || 'unplayable');
  if (!pr.streaming_data?.server_abr_streaming_url) throw new HttpError(502, 'no_sabr', 'no serverAbrStreamingUrl');
  return pr;
}

// Can media be fetched now? Same player call the tracks will use (cached).
export async function probe(yt, videoId) {
  playable(await player(yt, videoId));
  return { ok: true };
}

export async function openTrack(yt, videoId, track, quality, log, startSec = 0) {
  if (!QUALITIES.includes(quality)) quality = '1080p';
  const pr = playable(await player(yt, videoId));
  const sd = pr.streaming_data;
  const abrUrl = await yt.session.player?.decipher(sd.server_abr_streaming_url);
  const ustreamer = pr.player_config?.media_common_config?.media_ustreamer_request_config?.video_playback_ustreamer_config;
  if (!abrUrl || !ustreamer) throw new HttpError(502, 'no_sabr', 'missing SABR parameters');
  const pot = await sessionPot(yt);

  const stream = new SabrStream({
    formats: sd.adaptive_formats.map(buildSabrFormat),
    serverAbrStreamingUrl: abrUrl,
    videoPlaybackUstreamerConfig: ustreamer,
    poToken: pot,
    clientInfo: {
      clientName: parseInt(Constants.CLIENT_NAME_IDS[yt.session.context.client.clientName]),
      clientVersion: yt.session.context.client.clientVersion,
    },
  });
  stream.on('reloadPlayerResponse', async (ctx) => {
    try {
      const p2 = playable(await player(yt, videoId, ctx));
      const u = await yt.session.player?.decipher(p2.streaming_data?.server_abr_streaming_url);
      const c = p2.player_config?.media_common_config?.media_ustreamer_request_config?.video_playback_ustreamer_config;
      if (u && c) { stream.setStreamingURL(u); stream.setUstreamerConfig(c); }
    } catch (e) { log.warn('reload player failed', { videoId, error: String(e).slice(0, 200) }); }
  });
  stream.on('streamProtectionStatusUpdate', (s) => {
    // 2 = token not accepted (the stream will be cut in ~1 min), 3 = refused.
    // Drop the cached config/token so the next play starts clean.
    if (s.status >= 2) { log.warn('sabr stream protection', { videoId, track, status: s.status }); invalidate(yt); }
  });
  stream.on('error', (e) => log.warn('sabr error', { videoId, track, error: String(e).slice(0, 200) }));

  const options = {
    videoQuality: quality,
    audioQuality: 'AUDIO_QUALITY_MEDIUM',
    preferH264: true, preferMP4: true, preferOpus: false,
    enabledTrackTypes: track === 'audio' ? EnabledTrackTypes.AUDIO_ONLY : EnabledTrackTypes.VIDEO_ONLY,
  };
  const durationMs = Number(pr.video_details?.duration || 0) * 1000;
  const startMs = Math.floor(Math.max(0, startSec) * 1000);
  if (startMs > 0 && durationMs > 0 && startMs < durationMs - 5000) options.state = resumeState(stream, options, startMs, durationMs);
  const { videoStream, audioStream, selectedFormats } = await stream.start(options);
  const fmt = track === 'audio' ? selectedFormats.audioFormat : selectedFormats.videoFormat;
  return { stream, readable: track === 'audio' ? audioStream : videoStream, format: fmt, abort: () => stream.abort() };
}
