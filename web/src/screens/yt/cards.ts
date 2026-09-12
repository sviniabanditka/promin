// YouTube section building blocks: a 16:9 tile for a video / channel /
// playlist and a shelf (title + wrapping grid) with an optional "more" tile
// that appends the next page in place. Shared by the browse, video and search
// screens. ES5 target: plain loops, no spread.

import { on } from '../../core/controller';
import { t } from '../../core/i18n';
import { YtItem, YtShelf, YtFeed } from '../../core/api';
import { el } from '../../ui/dom';
import { openYtVideo, openYtChannel, openYtPlaylist } from '../nav';

export function openYtItem(item: YtItem): void {
  if (item.kind === 'video') openYtVideo(item.id);
  else if (item.kind === 'channel') openYtChannel(item.id);
  else if (item.kind === 'playlist') openYtPlaylist(item.id);
}

export function buildYtCard(item: YtItem): HTMLElement {
  const card = el('div', 'yt-card selector yt-card--' + item.kind);
  card.setAttribute('data-id', item.id);
  card.setAttribute('data-kind', item.kind);
  const thumb = el('div', 'yt-card__thumb');
  if (item.thumbnail) {
    const img = document.createElement('img');
    img.className = 'yt-card__img';
    img.src = item.thumbnail;
    img.alt = '';
    thumb.appendChild(img);
  }
  if (item.live) thumb.appendChild(el('div', 'yt-card__badge yt-card__badge--live', 'LIVE'));
  else if (item.duration_text) thumb.appendChild(el('div', 'yt-card__badge', item.duration_text));
  if (item.kind === 'playlist') thumb.appendChild(el('div', 'yt-card__stack'));
  if (item.progress_pct > 0) {
    const bar = el('div', 'yt-card__progress');
    const fill = el('div', 'yt-card__progress-fill');
    fill.style.width = Math.min(100, item.progress_pct) + '%';
    bar.appendChild(fill);
    thumb.appendChild(bar);
  }
  card.appendChild(thumb);
  card.appendChild(el('div', 'yt-card__title', item.title || ''));
  const sub: string[] = [];
  if (item.channel && item.channel.name) sub.push(item.channel.name);
  if (item.meta && item.meta.length) sub.push(item.meta[0]);
  if (sub.length) card.appendChild(el('div', 'yt-card__sub', sub.join(' • ')));
  on(card, 'hover:enter', function () {
    openYtItem(item);
  });
  return card;
}

export interface YtShelfOptions {
  // Fetch the next page of this shelf; resolves with the feed whose first
  // shelf's items are appended to the grid.
  loadMore?: (cont: string) => Promise<YtFeed>;
  onFocus?: (card: HTMLElement) => void;
  // Called after cards were appended so the owner can refresh the Navigator.
  onAppended?: (cards: HTMLElement[]) => void;
}

export function buildYtShelf(shelf: YtShelf, opts: YtShelfOptions): HTMLElement {
  const section = el('div', 'yt-shelf');
  if (shelf.title) section.appendChild(el('div', 'yt-shelf__title', shelf.title));
  const grid = el('div', 'yt-grid');
  section.appendChild(grid);
  let cont: string | null = shelf.cont;
  let more: HTMLElement | null = null;
  let loading = false;

  function add(items: YtItem[]): HTMLElement[] {
    const cards: HTMLElement[] = [];
    for (let i = 0; i < items.length; i++) {
      const card = buildYtCard(items[i]);
      if (opts.onFocus) {
        on(card, 'hover:focus', function () {
          if (opts.onFocus) opts.onFocus(card);
        });
      }
      if (more) grid.insertBefore(card, more);
      else grid.appendChild(card);
      cards.push(card);
    }
    return cards;
  }

  function placeMore(): void {
    if (more) {
      if (!cont) {
        if (more.parentNode) more.parentNode.removeChild(more);
        more = null;
      }
      return;
    }
    if (!cont || !opts.loadMore) return;
    more = el('div', 'yt-card yt-card--more selector');
    const box = el('div', 'yt-card__thumb');
    box.appendChild(el('div', 'yt-card__more-label', t('yt.more')));
    more.appendChild(box);
    if (opts.onFocus) {
      const m = more;
      on(m, 'hover:focus', function () {
        if (opts.onFocus) opts.onFocus(m);
      });
    }
    on(more, 'hover:enter', function () {
      if (loading || !cont || !opts.loadMore) return;
      loading = true;
      if (more) more.classList.add('is-loading');
      opts.loadMore(cont).then(
        function (feed) {
          loading = false;
          const first = feed && feed.shelves && feed.shelves.length ? feed.shelves[0] : null;
          cont = (first && first.cont) || (feed && feed.cont) || null;
          const cards = first ? add(first.items) : [];
          placeMore();
          if (opts.onAppended) opts.onAppended(cards);
        },
        function () {
          loading = false;
          if (more) more.classList.remove('is-loading');
        }
      );
    });
    grid.appendChild(more);
  }

  add(shelf.items);
  placeMore();
  return section;
}
