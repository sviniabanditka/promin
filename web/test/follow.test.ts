import test from 'node:test';
import assert from 'node:assert/strict';
import { followCorrection, HARD_SEEK_S, IN_SYNC_S, RATE_STEP } from '../src/core/player/follow.ts';

test('a big gap is a jump, not a nudge', () => {
  const a = followCorrection(600, 500, 1);
  assert.equal(a.seek, 600);
  assert.equal(a.rate, 1);
});

test('a small lag speeds the follower up, a small lead slows it down', () => {
  assert.deepEqual(followCorrection(101, 100, 1), { seek: null, rate: 1 + RATE_STEP });
  assert.deepEqual(followCorrection(100, 101, 1), { seek: null, rate: 1 - RATE_STEP });
});

test('inside the tolerance nothing happens', () => {
  assert.deepEqual(followCorrection(100.2, 100, 1), { seek: null, rate: 1 });
  assert.deepEqual(followCorrection(100, 100.2, 1), { seek: null, rate: 1 });
});

test('the viewer\'s own playback speed is the baseline, not 1x', () => {
  assert.equal(followCorrection(100, 100, 1.5).rate, 1.5);
  assert.equal(followCorrection(101, 100, 1.5).rate, 1.5 * (1 + RATE_STEP));
  assert.equal(followCorrection(600, 500, 1.25).rate, 1.25);
  // A nonsense base rate must not stop playback.
  assert.equal(followCorrection(100, 100, 0).rate, 1);
});

test('the thresholds are the documented ones', () => {
  assert.equal(followCorrection(100 + HARD_SEEK_S + 0.01, 100, 1).seek, 100 + HARD_SEEK_S + 0.01);
  assert.equal(followCorrection(100 + HARD_SEEK_S - 0.01, 100, 1).seek, null);
  // Just inside the tolerance plays at the base rate (exactly IN_SYNC_S is
  // float noise territory, so the check sits a hair below it).
  assert.equal(followCorrection(100 + IN_SYNC_S - 0.01, 100, 1).rate, 1);
  assert.equal(followCorrection(100 + IN_SYNC_S + 0.01, 100, 1).rate, 1 + RATE_STEP);
});
