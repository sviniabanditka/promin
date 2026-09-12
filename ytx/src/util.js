// Small helpers shared by the TV response normaliser and the server.

// InnerTube text: {simpleText} | {runs:[{text}]} | {content} (view models).
export function text(t) {
  if (!t) return '';
  if (typeof t === 'string') return t;
  if (t.simpleText) return t.simpleText;
  if (t.content) return t.content;
  if (Array.isArray(t.runs)) return t.runs.map((r) => r.text || '').join('');
  return '';
}

// Depth-first walk over a JSON tree; fn(key, value, parent) for every object key.
export function walk(o, fn) {
  if (Array.isArray(o)) { for (const v of o) walk(v, fn); return; }
  if (o && typeof o === 'object') {
    for (const k of Object.keys(o)) { fn(k, o[k], o); walk(o[k], fn); }
  }
}

// "1:02:03" → 3723
export function parseDuration(s) {
  if (!s) return 0;
  const parts = String(s).trim().split(':').map((x) => parseInt(x, 10));
  if (parts.some((x) => isNaN(x))) return 0;
  return parts.reduce((acc, x) => acc * 60 + x, 0);
}

export function largestThumb(list) {
  if (!Array.isArray(list) || !list.length) return null;
  return list.reduce((a, b) => ((b.width || 0) > (a.width || 0) ? b : a)).url || null;
}

export class HttpError extends Error {
  constructor(status, code, message) { super(message || code); this.status = status; this.code = code; }
}
