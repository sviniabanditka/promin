// Pure release-name parsing, shared by the torrent list (badges, ordering) and
// by the play path (transcode decision). No DOM, no imports — the one place
// that knows how to read "Movie.2019.2160p.HDR.x265-GROUP".

export interface TorrentMeta {
  quality: string;
  codec: string;
  hdr: string;
  dv: string;
  year: string;
}

export function parseMeta(title: string | undefined): TorrentMeta {
  const s = ' ' + (title || '') + ' ';
  const out: TorrentMeta = { quality: '', codec: '', hdr: '', dv: '', year: '' };

  let m = s.match(/\b(2160p|1080p|720p|480p)\b/i);
  if (m) out.quality = m[1].toLowerCase();
  else if (/\b(4k|uhd)\b/i.test(s)) out.quality = '2160p';

  if (/\bhdr10\+?\b/i.test(s) || /\bhdr\b/i.test(s)) out.hdr = 'HDR';
  if (/dolby\s*vision/i.test(s) || /\b(dovi|dv)\b/i.test(s)) out.dv = 'Dolby Vision';

  if (/\b(hevc|h\.?265|x265)\b/i.test(s)) out.codec = 'H.265';
  else if (/\b(avc|h\.?264|x264)\b/i.test(s)) out.codec = 'H.264';

  m = s.match(/\b((?:19|20)\d{2})\b/);
  if (m) out.year = m[1];

  return out;
}

// True when playing this release needs an HEVC/AV1 decoder. 2160p/4K counts:
// practically every 4K release is HEVC even when the name never says so, and
// guessing wrong here is a black screen, not a slow start.
export function needsHevcDecoder(name: string): boolean {
  return /hevc|h\.?265|x265|av1|2160p|\b4k\b/i.test(name);
}

export function isHDR(name: string): boolean {
  return /\bhdr|dolby.?vision|hdr10|\bdv\b/i.test(name) && !/\bsdr\b/i.test(name);
}

// What this device would have to do to play the release:
//   direct    — it just plays
//   transcode — server transcodes HEVC/AV1 → H264 first (slow start, works)
//   heavy     — the same, but 4K: one VPS cannot transcode that in real time,
//               so the row is demoted and marked instead of silently stalling.
export type PlaybackCost = 'direct' | 'transcode' | 'heavy';

export function playbackCost(name: string, canDecodeHevc: boolean): PlaybackCost {
  if (canDecodeHevc || !needsHevcDecoder(name)) return 'direct';
  return /2160p|\b4k\b|\buhd\b/i.test(name) ? 'heavy' : 'transcode';
}

// Stable "playable first" ordering: keeps the incoming order (seeders desc)
// inside each bucket. Array.prototype.sort is NOT stable on the Chromium ~47
// webview, hence buckets rather than a comparator.
export function byPlaybackCost<T>(list: T[], nameOf: (item: T) => string, canDecodeHevc: boolean): T[] {
  const direct: T[] = [];
  const transcode: T[] = [];
  const heavy: T[] = [];
  for (let i = 0; i < list.length; i++) {
    const cost = playbackCost(nameOf(list[i]), canDecodeHevc);
    if (cost === 'direct') direct.push(list[i]);
    else if (cost === 'transcode') transcode.push(list[i]);
    else heavy.push(list[i]);
  }
  return direct.concat(transcode, heavy);
}
