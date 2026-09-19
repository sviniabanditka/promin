// Matching a remembered dub against what a source actually offers. Voice ids
// are per-provider ("cikava_ideya", "1"), so the remembered thing is the NAME,
// and names differ in spelling between sources ("Дубляж | Цікава Ідея" vs
// "Цікава Ідея"). Pure, unit-tested in web/test/voices.test.ts.

export interface NamedVoice {
  id: string;
  name: string;
}

export function normalizeVoice(name: string): string {
  return (name || '')
    .toLowerCase()
    .replace(/ё/g, 'е')
    .replace(/[^0-9a-zа-яіїєґ]+/g, ' ')
    .replace(/^ +| +$/g, '')
    .replace(/ {2,}/g, ' ');
}

// The voice to switch to, or null when the current one is already the
// remembered dub (or nothing resembles it). Exact name first, then one name
// containing the other — "Цікава Ідея" should match "Дубляж | Цікава Ідея"
// without matching every dub of the list.
export function pickPreferredVoice(voices: NamedVoice[], preferred: string, currentID: string): string | null {
  const want = normalizeVoice(preferred);
  if (!want || !voices || !voices.length) return null;

  let exact: NamedVoice | null = null;
  let partial: NamedVoice | null = null;
  for (let i = 0; i < voices.length; i++) {
    const n = normalizeVoice(voices[i].name);
    if (!n) continue;
    if (n === want) {
      exact = voices[i];
      break;
    }
    if (!partial && (n.indexOf(want) !== -1 || want.indexOf(n) !== -1)) partial = voices[i];
  }
  const hit = exact || partial;
  if (!hit || hit.id === currentID) return null;
  return hit.id;
}
