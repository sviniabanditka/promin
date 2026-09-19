// Pure helpers of the film player, kept out of index.ts so they can be unit
// tested (web/test/player-pure.test.ts): the code that silently breaks on a
// TV — time formatting, the scrub ladder, which stream to open with.

function pad2(n: number): string {
  return n < 10 ? '0' + n : String(n);
}

// Scrub step by consecutive-press count: predictable ladder instead of
// Lampa's 10s×1.03^n (20 presses ≈ 4.5 min, 50 ≈ 19 min — nobody can tell how
// long to hold). 1–5 → 10s, 6–15 → 30s, 16–30 → 1 min, then 5 min.
export function scrubStep(n: number): number {
  return n <= 5 ? 10 : n <= 15 ? 30 : n <= 30 ? 60 : 300;
}

export function speedLabel(rate: number): string {
  return Math.round(rate * 100) / 100 + '×';
}

export function fmtTime(sec: number): string {
  if (!isFinite(sec) || sec < 0) sec = 0;
  const s = Math.floor(sec % 60);
  const m = Math.floor((sec / 60) % 60);
  const h = Math.floor(sec / 3600);
  if (h > 0) return h + ':' + pad2(m) + ':' + pad2(s);
  return pad2(m) + ':' + pad2(s);
}

// The slice of a stream the quality choice looks at.
export interface QualityStream {
  quality?: string;
  label?: string;
}

// A stream this device can decode: HEVC only where the engine really plays it.
export function playableStream(s: QualityStream, canDecodeHevc: boolean): boolean {
  const hay = ((s.quality || '') + ' ' + (s.label || '')).toUpperCase();
  return !(hay.indexOf('HEVC') !== -1 && !canDecodeHevc);
}

// Height in pixels from "1080p" / "1080 HEVC" / "FullHD 1080"; 0 when unknown.
export function qualityOf(s: QualityStream): number {
  const m = /(\d{3,4})p?/.exec((s.quality || '') + ' ' + (s.label || ''));
  return m ? parseInt(m[1], 10) : 0;
}

// Which stream to open with. pref is the settings value ('auto' or a height as
// text). 'auto' = the first playable one not above 1080 (a 4K stream on a
// household VPS is a stall waiting to happen), else the first playable; a set
// preference = the first playable stream naming it, else the first playable.
export function pickQuality(streams: QualityStream[], pref: string, canDecodeHevc: boolean): number {
  if (pref === 'auto') {
    let fallback = -1;
    for (let i = 0; i < streams.length; i++) {
      if (!playableStream(streams[i], canDecodeHevc)) continue;
      if (fallback < 0) fallback = i;
      const q = qualityOf(streams[i]);
      if (q === 0 || q <= 1080) return i;
    }
    return fallback < 0 ? 0 : fallback;
  }
  for (let i = 0; i < streams.length; i++) {
    const s = streams[i];
    const hay = (s.quality || '') + ' ' + (s.label || '');
    if (hay.indexOf(pref) !== -1 && playableStream(s, canDecodeHevc)) return i;
  }
  for (let i = 0; i < streams.length; i++) if (playableStream(streams[i], canDecodeHevc)) return i;
  return 0;
}
