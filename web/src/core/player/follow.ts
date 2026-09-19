// Two TVs of one account playing the same frame: the leading player ticks its
// position, this decides what the follower does about the gap. Pure, so the
// thresholds can be reasoned about (and tested) without a video element.

export interface FollowAction {
  // Absolute position to jump to, or null to stay and let the rate do the work.
  seek: number | null;
  // Rate to play at until the next tick.
  rate: number;
}

// Past this the gap is a different scene, not drift: jump.
export const HARD_SEEK_S = 3;
// Under this nobody can tell the two sets apart: play normally.
export const IN_SYNC_S = 0.4;
// 3% is enough to eat a second of drift within a couple of ticks and small
// enough not to be heard (the panel pitch-corrects).
export const RATE_STEP = 0.03;

export function followCorrection(leaderPos: number, myPos: number, baseRate: number): FollowAction {
  const rate = baseRate > 0 ? baseRate : 1;
  const delta = leaderPos - myPos;
  const gap = delta < 0 ? -delta : delta;
  if (gap > HARD_SEEK_S) return { seek: leaderPos, rate: rate };
  if (gap > IN_SYNC_S) return { seek: null, rate: rate * (delta > 0 ? 1 + RATE_STEP : 1 - RATE_STEP) };
  return { seek: null, rate: rate };
}
