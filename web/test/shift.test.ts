import test from 'node:test';
import assert from 'node:assert/strict';
import { fmtBehind, isLive, shiftFraction, shiftTarget, LIVE_EDGE_S, WINDOW_MARGIN_S } from '../src/core/player/shift.ts';

test('a pause drifts behind the edge by the time spent paused', () => {
  assert.equal(shiftTarget(0, 90, 0, 1200), 90);
  // Already 5 min behind, paused 1 more minute.
  assert.equal(shiftTarget(300, 60, 0, 1200), 360);
});

test('scrubbing forward eats the drift, never past live', () => {
  assert.equal(shiftTarget(0, 90, 60, 1200), 30);
  assert.equal(shiftTarget(0, 90, 500, 1200), 0);
  assert.ok(isLive(shiftTarget(0, 3, 0, 1200)));
  assert.ok(!isLive(LIVE_EDGE_S));
});

test('scrubbing back stops short of the deleted end of the window', () => {
  assert.equal(shiftTarget(0, 0, -5000, 1200), 1200 - WINDOW_MARGIN_S);
  // No window at all: nothing to clamp against, the caller stays live anyway.
  assert.equal(shiftTarget(0, 0, -100, 0), 100);
});

test('the bar puts live at the right and the window start at the left', () => {
  assert.equal(shiftFraction(0, 1200), 1);
  assert.equal(shiftFraction(600, 1200), 0.5);
  assert.equal(shiftFraction(5000, 1200), 0);
  assert.equal(shiftFraction(100, 0), 1);
});

test('fmtBehind is m:ss', () => {
  assert.equal(fmtBehind(0), '0:00');
  assert.equal(fmtBehind(65.4), '1:05');
  assert.equal(fmtBehind(1199), '19:59');
});
