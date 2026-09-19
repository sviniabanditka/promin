import test from 'node:test';
import assert from 'node:assert/strict';
import { fmtTime, pickQuality, playableStream, qualityOf, scrubStep, speedLabel } from '../src/core/player/pure.ts';

test('fmtTime: mm:ss under an hour, h:mm:ss above, garbage is 00:00', () => {
  assert.equal(fmtTime(0), '00:00');
  assert.equal(fmtTime(65), '01:05');
  assert.equal(fmtTime(3599.9), '59:59');
  assert.equal(fmtTime(3600), '1:00:00');
  assert.equal(fmtTime(NaN), '00:00');
  assert.equal(fmtTime(-5), '00:00');
});

test('scrubStep is the documented ladder', () => {
  assert.equal(scrubStep(1), 10);
  assert.equal(scrubStep(5), 10);
  assert.equal(scrubStep(6), 30);
  assert.equal(scrubStep(15), 30);
  assert.equal(scrubStep(16), 60);
  assert.equal(scrubStep(30), 60);
  assert.equal(scrubStep(31), 300);
});

test('speedLabel rounds to two decimals', () => {
  assert.equal(speedLabel(1), '1×');
  assert.equal(speedLabel(1.25), '1.25×');
  assert.equal(speedLabel(1.333333), '1.33×');
});

test('qualityOf reads the height out of either field', () => {
  assert.equal(qualityOf({ quality: '1080p' }), 1080);
  assert.equal(qualityOf({ label: 'FullHD 1080 HEVC' }), 1080);
  assert.equal(qualityOf({ quality: '4K' }), 0);
  assert.equal(qualityOf({}), 0);
});

test('HEVC is playable only where the engine decodes it', () => {
  assert.ok(playableStream({ quality: '1080p' }, false));
  assert.ok(!playableStream({ quality: '1080p HEVC' }, false));
  assert.ok(playableStream({ quality: '1080p HEVC' }, true));
});

const streams = [{ quality: '2160p' }, { quality: '1080p HEVC' }, { quality: '1080p' }, { quality: '720p' }];

test('auto: first playable stream not above 1080', () => {
  assert.equal(pickQuality(streams, 'auto', false), 2);
  assert.equal(pickQuality(streams, 'auto', true), 1);
  // Only 4K on offer: it is still the answer rather than nothing.
  assert.equal(pickQuality([{ quality: '2160p' }], 'auto', false), 0);
  assert.equal(pickQuality([], 'auto', false), 0);
});

test('a set preference wins when playable, else the first playable', () => {
  assert.equal(pickQuality(streams, '720', false), 3);
  assert.equal(pickQuality(streams, '2160', false), 0);
  assert.equal(pickQuality([{ quality: '1080p HEVC' }, { quality: '720p' }], '1080', false), 1);
  // Nothing is playable at all: index 0, the player will show its error screen.
  assert.equal(pickQuality([{ quality: '1080p HEVC' }], '1080', false), 0);
});
