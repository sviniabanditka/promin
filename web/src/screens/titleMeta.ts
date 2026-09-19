// Pure display helpers of the title screen: clock/runtime formatting, quality
// and seed badges, release-name parsing. No screen state.

import { el } from '../ui/dom';
import { t } from '../core/i18n';

export function pad2(n: number): string {
  return n < 10 ? '0' + n : String(n);
}
// Clock like the player: "25:12" or "1:01:23". Used for episode watched/total.
export function fmtClock(sec: number): string {
  if (!isFinite(sec) || sec < 0) sec = 0;
  const s = Math.floor(sec % 60);
  const m = Math.floor((sec / 60) % 60);
  const h = Math.floor(sec / 3600);
  if (h > 0) return h + ':' + pad2(m) + ':' + pad2(s);
  return pad2(m) + ':' + pad2(s);
}

export function runtimeText(minutes: number): string {
  const h = Math.floor(minutes / 60);
  const m = minutes % 60;
  if (h > 0) return h + t('unit.hour') + ' ' + m + t('unit.min');
  return m + t('unit.min');
}

// Human-readable byte size (files come with raw bytes; torrents carry a
// pre-formatted size_human from the backend). Ported from screens/sources.

// Quality/resolution pill. Blue accent for HD+; muted for SD (<=720). Ported
// look from the redesign artifact — one badge style across online + torrent rows.
export function qBadge(text: string): HTMLElement {
  const up = (text || '').toUpperCase();
  const sd = up.indexOf('720') !== -1 || up.indexOf('480') !== -1 || up.indexOf('360') !== -1 || up === 'SD';
  return el('span', 'q-badge' + (sd ? ' q-badge--sd' : ''), up);
}

// Seed-count colour bucket (green = many seeds, red = few).
export function seedClass(seeds: number | undefined): string {
  const s = seeds || 0;
  if (s >= 30) return 'torrent-badge--seeds-high';
  if (s >= 8) return 'torrent-badge--seeds-mid';
  return 'torrent-badge--seeds-low';
}

// Release-name parsing lives in core/release (pure, shared with the play path
// and unit-tested); re-exported here so the title screen keeps one import.
export { parseMeta } from '../core/release';
export type { TorrentMeta } from '../core/release';
