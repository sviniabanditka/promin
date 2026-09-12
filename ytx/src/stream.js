// One media track of a video over HTTP, pulled from YouTube through SABR.
//
// GET /stream/:vid/video|audio → fragments as the SabrStream produces them,
// chunked. Promin's remux job reads the two tracks as ffmpeg inputs and
// copy-muxes them into HLS — the same pipeline torrents already use.

import { SabrStream } from 'googlevideo/sabr-stream';
import { buildSabrFormat, EnabledTrackTypes } from 'googlevideo/utils';
import { Constants } from 'youtubei.js';
import { player } from './tv.js';
import { HttpError } from './util.js';

const QUALITIES = ['2160p', '1440p', '1080p', '720p', '480p', '360p'];

export async function openTrack(yt, videoId, track, quality, log) {
  if (!QUALITIES.includes(quality)) quality = '1080p';
  const pr = await player(yt, videoId);
  const ps = pr.playability_status || {};
  if (ps.status !== 'OK') throw new HttpError(409, 'unplayable', ps.reason || ps.status || 'unplayable');
  const sd = pr.streaming_data;
  if (!sd?.server_abr_streaming_url) throw new HttpError(502, 'no_sabr', 'no serverAbrStreamingUrl');
  const abrUrl = await yt.session.player?.decipher(sd.server_abr_streaming_url);
  const ustreamer = pr.player_config?.media_common_config?.media_ustreamer_request_config?.video_playback_ustreamer_config;
  if (!abrUrl || !ustreamer) throw new HttpError(502, 'no_sabr', 'missing SABR parameters');

  const stream = new SabrStream({
    formats: sd.adaptive_formats.map(buildSabrFormat),
    serverAbrStreamingUrl: abrUrl,
    videoPlaybackUstreamerConfig: ustreamer,
    clientInfo: {
      clientName: parseInt(Constants.CLIENT_NAME_IDS[yt.session.context.client.clientName]),
      clientVersion: yt.session.context.client.clientVersion,
    },
  });
  stream.on('reloadPlayerResponse', async (ctx) => {
    try {
      const p2 = await player(yt, videoId, ctx);
      const u = await yt.session.player?.decipher(p2.streaming_data?.server_abr_streaming_url);
      const c = p2.player_config?.media_common_config?.media_ustreamer_request_config?.video_playback_ustreamer_config;
      if (u && c) { stream.setStreamingURL(u); stream.setUstreamerConfig(c); }
    } catch (e) { log.warn('reload player failed', { videoId, error: String(e) }); }
  });
  stream.on('error', (e) => log.warn('sabr error', { videoId, track, error: String(e).slice(0, 200) }));

  const { videoStream, audioStream, selectedFormats } = await stream.start({
    videoQuality: quality,
    audioQuality: 'AUDIO_QUALITY_MEDIUM',
    preferH264: true, preferMP4: true, preferOpus: false,
    enabledTrackTypes: track === 'audio' ? EnabledTrackTypes.AUDIO_ONLY : track === 'video' ? EnabledTrackTypes.VIDEO_ONLY : EnabledTrackTypes.VIDEO_AND_AUDIO,
  });
  const fmt = track === 'audio' ? selectedFormats.audioFormat : selectedFormats.videoFormat;
  return { stream, readable: track === 'audio' ? audioStream : videoStream, format: fmt, abort: () => stream.abort() };
}
