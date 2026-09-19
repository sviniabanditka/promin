// Timeshift arithmetic for the live player (docs/tv.md). Everything is in
// "seconds behind the live edge": 0 is the broadcast, the window's far end is
// as far back as the server still has. Pure, unit-tested in
// web/test/shift.test.ts.

// The step one ◀/▶ (or ⏪/⏩) press moves by.
export const SEEK_STEP_S = 30;
// The last few seconds before the edge are not really behind it: hls.js sits
// there itself, and a target this close means "go live".
export const LIVE_EDGE_S = 5;
// The window's oldest seconds are being deleted while we look at them; never
// land on them.
export const WINDOW_MARGIN_S = 30;

// Where a paused viewer will resume: the frame they paused on kept drifting
// behind the edge while paused (`away`), and they may have scrubbed (`scrub`,
// positive = forward, i.e. closer to live). Clamped into the window.
export function shiftTarget(pauseBehind: number, away: number, scrub: number, windowSec: number): number {
  let behind = pauseBehind + away - scrub;
  if (behind < 0) behind = 0;
  const max = windowSec - WINDOW_MARGIN_S;
  if (max > 0 && behind > max) behind = max;
  return behind;
}

// True when the target is close enough to the edge to just play live.
export function isLive(behind: number): boolean {
  return behind < LIVE_EDGE_S;
}

// 0..1 position of a point `behind` seconds back on a bar that spans the
// window (right end = live edge).
export function shiftFraction(behind: number, windowSec: number): number {
  if (windowSec <= 0) return 1;
  const f = 1 - behind / windowSec;
  return f < 0 ? 0 : f > 1 ? 1 : f;
}

export function fmtBehind(sec: number): string {
  const s = Math.max(0, Math.round(sec));
  const m = Math.floor(s / 60);
  const r = s % 60;
  return m + ':' + (r < 10 ? '0' + r : String(r));
}
