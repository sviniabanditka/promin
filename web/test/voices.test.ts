import test from 'node:test';
import assert from 'node:assert/strict';
import { normalizeVoice, pickPreferredVoice } from '../src/core/player/voices.ts';

const voices = [
  { id: '1', name: 'Оригінал' },
  { id: '2', name: 'Дубляж | Цікава Ідея' },
  { id: '3', name: 'HDrezka Studio' },
];

test('normalizeVoice folds case and punctuation', () => {
  assert.equal(normalizeVoice('Дубляж | Цікава  Ідея'), 'дубляж цікава ідея');
  assert.equal(normalizeVoice('HDrezka Studio'), 'hdrezka studio');
});

test('an exact name wins', () => {
  assert.equal(pickPreferredVoice(voices, 'HDrezka Studio', '1'), '3');
});

test('a partial name matches the fuller one', () => {
  assert.equal(pickPreferredVoice(voices, 'Цікава Ідея', '1'), '2');
});

test('nothing to do when the wanted dub is already playing', () => {
  assert.equal(pickPreferredVoice(voices, 'Цікава Ідея', '2'), null);
  assert.equal(pickPreferredVoice(voices, 'HDrezka Studio', '3'), null);
});

test('no preference, no list, no match → no switch', () => {
  assert.equal(pickPreferredVoice(voices, '', '1'), null);
  assert.equal(pickPreferredVoice([], 'Цікава Ідея', ''), null);
  assert.equal(pickPreferredVoice(voices, 'Netflix', '1'), null);
});
