import test from 'node:test';
import assert from 'node:assert/strict';
import { displayLine, normalizeLine, searchCues } from '../src/core/player/cues.ts';

const cues = [
  { start: 10, text: '<i>Ти мене чуєш?</i>' },
  { start: 13, text: 'Я казав тобі,' },
  { start: 15, text: 'що це погана ідея.' },
  { start: 30, text: '{\\an8}Це погана ідея!' },
];

test('normalizeLine drops markup, case and punctuation', () => {
  assert.equal(normalizeLine('<i>Ти мене ЧУЄШ?</i>'), 'ти мене чуєш');
  assert.equal(normalizeLine('{\\an8}Всё — ясно...'), 'все ясно');
});

test('displayLine keeps the words readable', () => {
  assert.equal(displayLine('<i>Ти мене\nчуєш?</i>'), 'Ти мене чуєш?');
});

test('searchCues finds a line and ignores punctuation', () => {
  assert.deepEqual(searchCues(cues, 'чуєш', 10).map((c) => c.start), [10]);
  assert.deepEqual(searchCues(cues, 'погана ідея', 10).map((c) => c.start), [15, 30]);
});

test('a phrase spanning two cues hits the cue it starts in, once', () => {
  assert.deepEqual(searchCues(cues, 'казав тобі що це', 10).map((c) => c.start), [13]);
});

test('too short a query matches nothing, limit caps the rest', () => {
  assert.deepEqual(searchCues(cues, 'я', 10), []);
  assert.equal(searchCues(cues, 'а', 10).length, 0);
  assert.equal(searchCues(cues, 'ідея', 1).length, 1);
});
