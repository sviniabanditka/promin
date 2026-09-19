// Searching the loaded subtitle track for a line. Pure: the player hands over
// the cues it already has (a <track> or hls.js parsed them), this decides what
// matches. No DOM, unit-tested in web/test/cues.test.ts.

export interface CueLine {
  // Seconds in the video's own clock (the player adds timeBase / subOffset).
  start: number;
  text: string;
}

// Subtitle cues carry markup (<i>, {\an8}), hard line breaks and stray
// punctuation; the viewer types none of that.
export function normalizeLine(raw: string): string {
  return (raw || '')
    .replace(/<[^>]*>/g, ' ')
    .replace(/\{[^}]*\}/g, ' ')
    .toLowerCase()
    .replace(/ё/g, 'е')
    .replace(/[^0-9a-zа-яіїєґ'']+/g, ' ')
    .replace(/^ +| +$/g, '')
    .replace(/ {2,}/g, ' ');
}

// Plain reading text for the result row: markup out, line breaks to spaces.
export function displayLine(raw: string): string {
  return (raw || '')
    .replace(/<[^>]*>/g, '')
    .replace(/\{[^}]*\}/g, '')
    .replace(/\s+/g, ' ')
    .replace(/^ +| +$/g, '');
}

// Cues matching the query, in play order, at most `limit`. A spoken phrase
// often straddles two cues, so each cue is also tested joined with the next
// one; the row returned is where the phrase starts.
export function searchCues(cues: CueLine[], query: string, limit: number): CueLine[] {
  const q = normalizeLine(query);
  const out: CueLine[] = [];
  if (q.length < 2) return out;

  const norm: string[] = [];
  for (let i = 0; i < cues.length; i++) norm.push(normalizeLine(cues[i].text));

  for (let i = 0; i < cues.length; i++) {
    const self = norm[i].indexOf(q) !== -1;
    if (!self) {
      const hasNext = i + 1 < cues.length;
      if (!hasNext) continue;
      if ((norm[i] + ' ' + norm[i + 1]).indexOf(q) === -1) continue;
      // The whole phrase sits in the next cue: let that row be the hit.
      if (norm[i + 1].indexOf(q) !== -1) continue;
    }
    out.push(cues[i]);
    if (out.length >= limit) break;
  }
  return out;
}
