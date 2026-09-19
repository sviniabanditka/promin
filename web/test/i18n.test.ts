// The three dictionaries drift apart silently: a missing key renders as the key
// itself on the TV. Parity is checked by reading the source, so no import of the
// DOM-bound module is needed.
import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';

const src = readFileSync(new URL('../src/core/i18n.ts', import.meta.url), 'utf8');

function keysOf(lang: string): string[] {
  const from = src.indexOf('  ' + lang + ': {');
  assert.notEqual(from, -1, 'dictionary ' + lang + ' not found');
  const rest = src.slice(from);
  const end = rest.indexOf('\n  },');
  const body = rest.slice(0, end === -1 ? rest.length : end);
  return body.split('\n').map((l) => /^\s*'([^']+)':/.exec(l)).filter((m) => m).map((m) => m![1]);
}

test('uk / ru / en carry the same keys', () => {
  const uk = keysOf('uk');
  const ru = keysOf('ru');
  const en = keysOf('en');
  assert.ok(uk.length > 300, 'suspiciously few keys: ' + uk.length);
  for (const [name, list] of [['ru', ru], ['en', en]] as [string, string[]][]) {
    const missing = uk.filter((k) => list.indexOf(k) === -1);
    const extra = list.filter((k) => uk.indexOf(k) === -1);
    assert.deepEqual(missing, [], name + ' is missing keys');
    assert.deepEqual(extra, [], name + ' has keys uk does not');
  }
});

test('no duplicate keys inside one dictionary', () => {
  for (const lang of ['uk', 'ru', 'en']) {
    const list = keysOf(lang);
    const dupes = list.filter((k, i) => list.indexOf(k) !== i);
    assert.deepEqual(dupes, [], lang + ' has duplicate keys');
  }
});
