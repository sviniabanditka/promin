// Shared "how do we play this torrent file" logic. Both the title screen and the
// standalone Torrents screen used to build the player media separately — and the
// Torrents copy forgot the device-capability check, so the same .mkv/HEVC file
// remuxed fine from a title card and gave a black screen from the list.

import { streamUrl, TorrentFile } from './api';
import { caps, canDecodeHevc } from './capabilities';
import { PlayerMedia } from './player';
import { t } from './i18n';

export interface TorrentMedia {
  media: PlayerMedia;
  // True when the backend will answer /stream with an HLS playlist (remux or
  // transcode) instead of a progressive file — the player must use its HLS engine.
  hls: boolean;
}

// The backend serves /stream progressively (type mp4) UNLESS device caps force a
// server-side remux/transcode to HLS: an .mkv on a webview that can't demux MKV
// (mkv=false → copy_mkv), or an HEVC/AV1 release on a TV with no such decoder
// (hevc=false → transcode). In those cases /stream 302-redirects to an HLS
// playlist, so a progressive <video> would poll the manifest forever.
export function torrentMedia(infohash: string, file: TorrentFile, torrentTitle: string): TorrentMedia {
  const name = (file.name || '') + ' ' + (torrentTitle || '');
  const isMkv = /\.mkv$/i.test(file.name || '');
  const looksHevc = /hevc|h\.?265|x265|av1|2160p|\b4k\b/i.test(name);
  const looksHdr = /\bhdr|dolby.?vision|hdr10|\bdv\b/i.test(name) && !/\bsdr\b/i.test(name);
  // Transcode when the release is HEVC/AV1 and this device's engine can't decode
  // it (hls.js/MSE can't). copy_mkv only remuxes the container, so it'd leave
  // HEVC unplayable — transcode supersedes it.
  const needsTranscode = looksHevc && !canDecodeHevc();
  const hls = needsTranscode || (isMkv && !caps.mkv);
  const url = streamUrl(infohash, file.index, needsTranscode, looksHdr);
  return {
    hls: hls,
    media: {
      type: hls ? 'hls' : 'mp4',
      streams: [{ url: url, label: file.name || torrentTitle || t('sources.torrents') }],
      subtitles: [],
      voices: [],
      currentVoice: null,
    },
  };
}

// Season/episode from a release file name: "S01E05", "1x05", "E05", "05 серія".
// Series packs keep every episode in one torrent; without this every file of the
// pack shared ONE timecode slot, so resuming episode 3 landed inside episode 5.
export function parseEpisode(name: string): { season: number | null; episode: number } | null {
  let m = /S(\d{1,2})\s*E(\d{1,3})/i.exec(name);
  if (m) return { season: parseInt(m[1], 10), episode: parseInt(m[2], 10) };
  m = /(?:^|\D)(\d{1,2})x(\d{2,3})(?:\D|$)/i.exec(name);
  if (m) return { season: parseInt(m[1], 10), episode: parseInt(m[2], 10) };
  m = /(?:^|\D)E(\d{2,3})(?:\D|$)/i.exec(name);
  if (m) return { season: null, episode: parseInt(m[1], 10) };
  m = /(?:^|\D)(\d{1,3})\s*(?:серия|серія|эпизод|епізод|episode|ep\.?)/i.exec(name);
  if (m) return { season: null, episode: parseInt(m[1], 10) };
  return null;
}
