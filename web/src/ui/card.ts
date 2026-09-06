// Card component built from the normalized catalog DTO (docs/api.md).
// Posters arrive as ready relative paths ("/img/w500/x.jpg"), used as-is.
// The card stores its tmdb id/type/backdrop in data-* attributes so the
// owning screen can open the title screen on Enter and tint the backdrop
// on focus, without holding a JS reference per card.

import { el } from './dom';
import { on } from '../core/controller';
import { iconEl, ICON_STAR, ICON_BOOKMARK } from './icons';
import { Card } from '../core/api';
import { isBookmarked, subscribe, toggleBookmark } from '../core/sync';
import { isLogged } from '../core/auth';
import { toast } from './toast';
import { t } from '../core/i18n';

// Cards are built once and kept in the DOM across push/back (home, catalog,
// search survive a title visit), so a marker computed at build time went stale
// the moment the user toggled the bookmark on the title screen. One module-wide
// subscription repaints every card's marker from the live cache instead.
let markerSyncArmed = false;
function armMarkerSync(): void {
  if (markerSyncArmed) return;
  markerSyncArmed = true;
  subscribe('bookmarks', function () {
    const cards = document.querySelectorAll('.card[data-tmdb]');
    for (let i = 0; i < cards.length; i++) {
      const c = cards[i] as HTMLElement;
      const view = c.querySelector('.card__view');
      if (!view) continue;
      const want = isBookmarked(Number(c.getAttribute('data-tmdb')), c.getAttribute('data-type') || '');
      const mark = view.querySelector('.card__bookmark');
      if (want && !mark) view.insertBefore(iconEl(ICON_BOOKMARK, 'card__bookmark'), view.firstChild);
      else if (!want && mark && mark.parentNode) mark.parentNode.removeChild(mark);
    }
  });
}

// Load every deferred poster under `root` (a lane the user just entered).
export function revealCards(root: HTMLElement): void {
  const imgs = root.querySelectorAll('img[data-src]');
  for (let i = 0; i < imgs.length; i++) {
    const img = imgs[i] as HTMLImageElement;
    const src = img.getAttribute('data-src');
    if (src) img.src = src;
    img.removeAttribute('data-src');
  }
}

// lazy: defer the poster (data-src) until revealCards() — Chromium 47 has no
// loading=lazy, and Home mounting 100+ posters at once starved the first
// screenful on TV networks.
export function buildCard(item: Card, lazy?: boolean): HTMLElement {
  armMarkerSync();
  const card = el('div', 'card selector');
  card.setAttribute('data-tmdb', String(item.tmdb_id));
  card.setAttribute('data-type', item.type);
  card.setAttribute('data-title', item.title || '');
  card.setAttribute('data-backdrop', item.backdrop || '');

  const view = el('div', 'card__view');

  // Bookmark marker: from the DTO's in_bookmarks flag or the live sync cache.
  if (item.in_bookmarks || isBookmarked(item.tmdb_id, item.type)) {
    view.appendChild(iconEl(ICON_BOOKMARK, 'card__bookmark'));
  }

  if (item.poster) {
    const img = el('img', 'card__img');
    if (lazy) img.setAttribute('data-src', item.poster);
    else img.src = item.poster;
    img.alt = item.title || '';
    view.appendChild(img);
  } else {
    view.appendChild(el('div', 'card__noimg', item.title || ''));
  }

  if (item.rating) {
    const vote = el('div', 'card__vote');
    vote.appendChild(iconEl(ICON_STAR, 'card__vote-icon'));
    vote.appendChild(el('span', undefined, item.rating.toFixed(1)));
    view.appendChild(vote);
  }

  // Continue-watching progress bar (timecode is user-specific, docs/api.md).
  if (item.timecode && item.timecode.duration_sec > 0) {
    const ratio = Math.max(0, Math.min(1, item.timecode.position_sec / item.timecode.duration_sec));
    const track = el('div', 'card__progress');
    const fill = el('div', 'card__progress-fill');
    fill.style.width = Math.round(ratio * 100) + '%';
    track.appendChild(fill);
    view.appendChild(track);
  }

  card.appendChild(view);
  card.appendChild(el('div', 'card__title', item.title || ''));
  card.appendChild(el('div', 'card__year', item.year ? String(item.year) : ''));

  // Long-press OK: toggle the bookmark without opening the title (the marker
  // repaints via the subscription above).
  on(card, 'hover:long', function () {
    if (!isLogged()) return;
    toggleBookmark(item.tmdb_id, item.type, {
      title: item.title,
      poster: item.poster,
      backdrop: item.backdrop,
      year: item.year,
      rating: item.rating,
    }).then(function (nowOn) {
      toast({ kind: nowOn ? 'success' : 'info', icon: nowOn ? '★' : '☆', text: t(nowOn ? 'title.bookmark_added_toast' : 'title.bookmark_removed_toast') });
    });
  });

  return card;
}


export function buildSkeletonCard(): HTMLElement {
  const card = el('div', 'card card--skeleton');
  const view = el('div', 'card__view skeleton-box');
  card.appendChild(view);
  card.appendChild(el('div', 'card__title skeleton-line'));
  card.appendChild(el('div', 'card__year skeleton-line skeleton-line--short'));
  return card;
}
