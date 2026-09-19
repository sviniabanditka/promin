// Release-name parsing and the "can this panel play it" ordering. Pure module,
// so the check runs under `node --test` with type stripping — no DOM, no build.
import test from 'node:test';
import assert from 'node:assert/strict';
import { byPlaybackCost, isHDR, needsHevcDecoder, parseMeta, playbackCost } from '../src/core/release.ts';

test('parseMeta reads quality, codec, hdr and year', () => {
  const m = parseMeta('Dune.Part.Two.2024.2160p.UHD.BluRay.HDR.x265-GROUP');
  assert.equal(m.quality, '2160p');
  assert.equal(m.codec, 'H.265');
  assert.equal(m.hdr, 'HDR');
  assert.equal(m.year, '2024');
  assert.equal(parseMeta('Movie 4K WEB-DL').quality, '2160p');
  assert.equal(parseMeta('Movie.1080p.x264').codec, 'H.264');
  assert.equal(parseMeta(undefined).quality, '');
});

test('needsHevcDecoder treats every 4K release as HEVC', () => {
  assert.ok(needsHevcDecoder('Movie.2160p.WEB-DL'));
  assert.ok(needsHevcDecoder('Movie.1080p.HEVC'));
  assert.ok(needsHevcDecoder('Movie.1080p.AV1'));
  assert.ok(!needsHevcDecoder('Movie.1080p.x264.AC3'));
});

test('isHDR ignores an explicit SDR release', () => {
  assert.ok(isHDR('Movie.2160p.HDR10.x265'));
  assert.ok(isHDR('Movie.2160p.Dolby.Vision'));
  assert.ok(!isHDR('Movie.2160p.SDR.HDR.x265')); // SDR wins: no tone-mapping
  assert.ok(!isHDR('Movie.1080p.x264'));
});

test('playbackCost: 4K without a decoder is heavy, 1080p HEVC is a transcode', () => {
  assert.equal(playbackCost('Movie.2160p.x265', false), 'heavy');
  assert.equal(playbackCost('Movie.1080p.HEVC', false), 'transcode');
  assert.equal(playbackCost('Movie.1080p.x264', false), 'direct');
  // A panel that decodes HEVC itself plays everything directly.
  assert.equal(playbackCost('Movie.2160p.x265', true), 'direct');
});

test('byPlaybackCost demotes without reshuffling inside a bucket', () => {
  const list = [
    { title: 'A.2160p.x265' },
    { title: 'B.1080p.x264' },
    { title: 'C.1080p.HEVC' },
    { title: 'D.1080p.x264' },
  ];
  const out = byPlaybackCost(list, (x) => x.title, false).map((x) => x.title[0]);
  assert.deepEqual(out, ['B', 'D', 'C', 'A']);
  // Nothing moves when the device can decode it all.
  assert.deepEqual(
    byPlaybackCost(list, (x) => x.title, true).map((x) => x.title[0]),
    ['A', 'B', 'C', 'D']
  );
});
