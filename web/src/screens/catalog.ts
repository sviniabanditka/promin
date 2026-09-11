// Catalog screen — filtered vertical grid. Filter bar (type / genre / year /
// sort) with a remote-controlled modal select; genres come from
// GET /api/v1/catalog/genres. Items come paged (20/page) from
// GET /api/v1/catalog/list; the next page loads when focus nears the last
// rendered card (simple paging virtualization — pages are appended, never
// unmounted). Grid is flexbox rows (wrap), never CSS grid (docs/frontend.md).

import Controller, { on } from '../core/controller';
import { Scroll } from '../core/scroll';
import { t } from '../core/i18n';
import { ScreenInstance } from '../core/activity';
import { getList, getGenres, Card, Genre, QueryParams } from '../core/api';
import { el, empty } from '../ui/dom';
import { buildMenu, Menu } from '../ui/menu';
import { buildFooter } from '../ui/shell';
import { buildState } from '../ui/state';
import { buildCard, buildSkeletonCard } from '../ui/card';
import { openTitle } from './nav';

interface Option {
  label: string;
  value: string;
}

const PAGE_SIZE_HINT = 6; // prefetch next page when within N cards of the end

export interface CatalogParams {
  // Home-lane id (trending/new_releases/popular_movies/popular_tv/cartoons/
  // anime). Maps to an initial type/genre/sort so a lane's "More" tile opens
  // the closest full list backed by /catalog/list (TMDB /discover). trending
  // has no /discover equivalent, so it falls back to popular movies — the
  // nearest available list (noted in the report).
  category?: string;
}

interface CategoryPreset {
  type: string;
  genre: string;
  sort: string;
}

// Row ids the catalog can actually show. Home renders its "more" tile only for
// these — a recs:*/provider:* lane's tile used to open "popular movies".
export function hasCategoryPreset(category: string): boolean {
  switch (category) {
    case 'popular_tv':
    case 'new_releases':
    case 'cartoons':
    case 'anime':
    case 'popular_movies':
    case 'trending':
      return true;
    default:
      return false;
  }
}

function categoryPreset(category: string): CategoryPreset {
  switch (category) {
    case 'popular_tv':
      return { type: 'tv', genre: '', sort: 'popularity' };
    case 'new_releases':
      return { type: 'movie', genre: '', sort: 'year' };
    case 'cartoons':
      return { type: 'movie', genre: '16', sort: 'popularity' }; // TMDB Animation
    case 'anime':
      return { type: 'tv', genre: '16', sort: 'popularity' }; // Animation (origin JP not filterable via /discover here)
    case 'popular_movies':
    case 'trending': // no /discover trending endpoint — nearest is popular movies
    default:
      return { type: 'movie', genre: '', sort: 'popularity' };
  }
}

export function mountCatalog(container: HTMLElement, params?: CatalogParams): ScreenInstance {
  container.className += ' catalog-screen';

  const menu: Menu = buildMenu('catalog', 'content');
  container.appendChild(menu.el);

  // No separate head bar here — catalog's own filter-bar is the top bar (both
  // are fixed at top:0 and would overlap). Just the footer hints for parity.
  container.appendChild(buildFooter());

  // ---- filter state ----
  const preset = params && params.category ? categoryPreset(params.category) : null;
  const state = {
    type: preset ? preset.type : 'movie',
    genre: preset ? preset.genre : '', // genre id as string, '' = all
    year: '', // single year as string, '' = all
    rating: '', // min vote average as string, '' = any
    sort: preset ? preset.sort : 'popularity',
  };

  let genres: Genre[] = [];

  // ---- filter bar ----
  const bar = el('div', 'filter-bar');
  let lastFilter: HTMLElement | false = false;

  const fType = buildFilterButton('filter.type');
  const fGenre = buildFilterButton('filter.genre');
  const fYear = buildFilterButton('filter.year');
  const fRating = buildFilterButton('filter.rating');
  const fSort = buildFilterButton('filter.sort');
  bar.appendChild(fType.el);
  bar.appendChild(fGenre.el);
  bar.appendChild(fYear.el);
  bar.appendChild(fRating.el);
  bar.appendChild(fSort.el);
  container.appendChild(bar);

  function buildFilterButton(labelKey: string): { el: HTMLElement; value: HTMLElement } {
    const btn = el('div', 'filter-btn selector');
    btn.appendChild(el('div', 'filter-btn__label', t(labelKey)));
    const value = el('div', 'filter-btn__value', '');
    btn.appendChild(value);
    on(btn, 'hover:focus', function () {
      lastFilter = btn;
    });
    return { el: btn, value: value };
  }

  function typeOptions(): Option[] {
    return [
      { label: t('filter.movie'), value: 'movie' },
      { label: t('filter.tv'), value: 'tv' },
    ];
  }

  function genreOptions(): Option[] {
    const opts: Option[] = [{ label: t('filter.all'), value: '' }];
    for (let i = 0; i < genres.length; i++) {
      opts.push({ label: genres[i].name, value: String(genres[i].id) });
    }
    return opts;
  }

  function yearOptions(): Option[] {
    const opts: Option[] = [{ label: t('filter.all'), value: '' }];
    const now = new Date().getFullYear();
    for (let y = now; y >= now - 30; y--) {
      opts.push({ label: String(y), value: String(y) });
    }
    return opts;
  }

  function ratingOptions(): Option[] {
    const opts: Option[] = [{ label: t('filter.all'), value: '' }];
    const marks = ['5', '6', '7', '8', '9'];
    for (let i = 0; i < marks.length; i++) opts.push({ label: '★ ' + marks[i] + '+', value: marks[i] });
    return opts;
  }

  function sortOptions(): Option[] {
    return [
      { label: t('sort.popularity'), value: 'popularity' },
      { label: t('sort.rating'), value: 'rating' },
      { label: t('sort.year'), value: 'year' },
    ];
  }

  function refreshFilterLabels(): void {
    fType.value.textContent = state.type === 'tv' ? t('filter.tv') : t('filter.movie');
    let genreLabel = t('filter.all');
    for (let i = 0; i < genres.length; i++) {
      if (String(genres[i].id) === state.genre) genreLabel = genres[i].name;
    }
    fGenre.value.textContent = genreLabel;
    fYear.value.textContent = state.year ? state.year : t('filter.all');
    fRating.value.textContent = state.rating ? '★ ' + state.rating + '+' : t('filter.all');
    fSort.value.textContent = t('sort.' + state.sort);
  }

  // ---- grid ----
  const gridWrap = el('div', 'catalog-grid');
  const gridScroll = new Scroll({ mask: true, over: true });
  gridWrap.appendChild(gridScroll.render());
  container.appendChild(gridWrap);

  const gridBody = gridScroll.body();
  gridBody.classList.add('catalog-grid__body');

  let cards: HTMLElement[] = [];
  let currentPage = 0;
  let totalPages = 1;
  let loading = false;
  let loadSeq = 0; // generation token: a page-1 load bumps it; stale page results bail
  let lastCard: HTMLElement | false = false;
  let destroyed = false;
  let paused = false; // hidden under a pushed title: never toggle from a late page load

  function queryParams(page: number): QueryParams {
    const p: QueryParams = {
      type: state.type,
      sort: state.sort,
      page: page,
    };
    if (state.genre) p.genre = state.genre;
    if (state.year) {
      p.year_from = state.year;
      p.year_to = state.year;
    }
    if (state.rating) p.rating_from = state.rating;
    return p;
  }

  function attachCard(item: Card): HTMLElement {
    const card = buildCard(item);
    on(card, 'hover:focus', function () {
      lastCard = card;
      maybePrefetch(card);
      pruneFarImages(card);
    });
    on(card, 'hover:enter', function () {
      const tmdb = parseInt(card.getAttribute('data-tmdb') || '0', 10);
      const type = card.getAttribute('data-type') || 'movie';
      if (tmdb) openTitle(type as 'movie' | 'tv', tmdb);
    });
    return card;
  }

  // The grid keeps every loaded page in the DOM (instant Back, no re-fetch).
  // 30 pages = 600 decoded posters resident — an OOM tab-kill on Tizen. Keep
  // decoded images only within ±KEEP_PAGES of the focused page; the rest go
  // back to data-src and reload when the user scrolls near them again.
  const KEEP_CARDS = PAGE_SIZE_HINT * 3;
  let lastPrunedPage = -1;
  function pruneFarImages(card: HTMLElement): void {
    const idx = (card as unknown as { pidx?: number }).pidx;
    if (idx == null) return;
    const page = Math.floor(idx / PAGE_SIZE_HINT);
    if (page === lastPrunedPage) return; // once per page change, not per keypress
    lastPrunedPage = page;
    for (let i = 0; i < cards.length; i++) {
      const img = cards[i].querySelector('img.card__img') as HTMLImageElement | null;
      if (!img) continue;
      const far = Math.abs(i - idx) > KEEP_CARDS;
      if (far && img.src) {
        img.setAttribute('data-src', img.src);
        img.removeAttribute('src');
      } else if (!far && !img.src) {
        const src = img.getAttribute('data-src');
        if (src) img.src = src;
      }
    }
  }

  function maybePrefetch(card: HTMLElement): void {
    // O(1): index stamped at push time (cards only grow/reset, never reorder),
    // instead of cards.indexOf over an unbounded array every hover.
    const idx = (card as unknown as { pidx?: number }).pidx;
    if (idx != null && idx >= cards.length - PAGE_SIZE_HINT && currentPage < totalPages && !loading) {
      loadPage(currentPage + 1);
    }
  }

  function showGridSkeleton(): void {
    hideLoadingMore(); // else a stale loadMoreEl var stays non-null → strip never reappears (CAT-1)
    gridScroll.reset(); // clear the wheel/scroll transform so the grid isn't rendered off-screen (CAT-2)
    empty(gridBody);
    cards = []; // a fresh page-1 load invalidates the old grid (prefetch indices etc.)
    const box = el('div', 'catalog-grid__skeleton');
    for (let i = 0; i < 12; i++) box.appendChild(buildSkeletonCard());
    gridBody.appendChild(box);
  }

  function showEmpty(): void {
    empty(gridBody);
    cards = [];
    const box = buildState({
      kind: 'empty',
      text: t('catalog.empty'),
      actionLabel: t('catalog.reset'),
      onRetry: function () {
        state.genre = '';
        state.year = '';
        state.rating = '';
        refreshFilterLabels();
        reload();
      },
    });
    gridBody.appendChild(box);
    // Retry/Reset from a state box → reload → skeleton removed the focused
    // button; re-toggle so the fresh box's button gets the ring.
    if (Controller.enabled().name === 'content') Controller.toggle('content');
  }

  function showGridError(): void {
    empty(gridBody);
    cards = [];
    const box = buildState({
      kind: 'error',
      text: t('error.load'),
      onRetry: function () {
        reload();
      },
    });
    gridBody.appendChild(box);
    if (!paused && Controller.enabled().name === 'content') Controller.toggle('content');
  }

  function renderFirstPage(items: Card[]): void {
    empty(gridBody);
    cards = [];
    if (!items.length) {
      showEmpty();
      return;
    }
    for (let i = 0; i < items.length; i++) {
      const card = attachCard(items[i]);
      (card as unknown as { pidx: number }).pidx = cards.length;
      cards.push(card);
      gridBody.appendChild(card);
    }
    // Only pull focus into the grid if the user is already there (e.g. after
    // a filter-change reload while browsing). On first open focus stays on
    // the filter bar until the user presses Down.
    if (!paused && Controller.enabled().name === 'content') {
      Controller.toggle('content');
    }
  }

  function appendPage(items: Card[]): void {
    for (let i = 0; i < items.length; i++) {
      const card = attachCard(items[i]);
      (card as unknown as { pidx: number }).pidx = cards.length;
      cards.push(card);
      gridBody.appendChild(card);
      // Only while the grid owns the collection: a prefetch landing with a filter
      // modal open used to append cards into the MODAL's collection, so Down from
      // its last option walked focus under the overlay.
      if (Controller.enabled().name === 'content') Controller.collectionAppend(card);
    }
  }

  // Inline "loading more" strip appended to the grid tail while the next page
  // fetches — the pagination load used to be completely silent (audit #10).
  let loadMoreEl: HTMLElement | null = null;
  function showLoadingMore(): void {
    if (loadMoreEl) return;
    loadMoreEl = el('div', 'catalog-loading-more');
    loadMoreEl.appendChild(el('div', 'state__spinner'));
    loadMoreEl.appendChild(el('div', 'catalog-loading-more__text', t('catalog.loading_more')));
    gridBody.appendChild(loadMoreEl);
  }
  function hideLoadingMore(): void {
    if (loadMoreEl && loadMoreEl.parentNode) loadMoreEl.parentNode.removeChild(loadMoreEl);
    loadMoreEl = null;
  }

  function loadPage(page: number): void {
    // Only block a concurrent PREFETCH (page>1); a fresh page-1 (reload / filter
    // change) must always supersede an in-flight prefetch, not be dropped by it.
    if (page > 1 && loading) return;
    if (page <= 1) loadSeq++; // new generation — stale in-flight results bail below
    const my = loadSeq;
    loading = true;
    if (page <= 1) showGridSkeleton();
    else showLoadingMore();

    getList(queryParams(page)).then(
      function (res) {
        if (destroyed || my !== loadSeq) return; // superseded by a newer reload
        loading = false;
        hideLoadingMore();
        if (!res) {
          if (page <= 1) showGridError();
          return;
        }
        totalPages = res.total_pages || 1;
        currentPage = res.page || page;
        if (page <= 1) renderFirstPage(res.items || []);
        else appendPage(res.items || []);
      },
      function () {
        if (destroyed || my !== loadSeq) return;
        loading = false;
        hideLoadingMore();
        if (page <= 1) showGridError();
      }
    );
  }

  function reload(): void {
    currentPage = 0;
    totalPages = 1;
    lastCard = false;
    loadPage(1);
  }

  // ---- controllers ----
  const filtersController = {
    toggle: function () {
      Controller.collectionSet(bar);
      Controller.collectionFocus(lastFilter || false, bar);
    },
    left: function () {
      Controller.moveOr('left', function () {
        Controller.toggle('menu');
      });
    },
    right: function () {
      Controller.moveOr('right');
    },
    down: function () {
      Controller.toggle('content');
    },
    back: function () {
      Controller.toggle('menu');
    },
  };

  const gridController = {
    toggle: function () {
      if (!cards.length) {
        // Empty/error state box has a Reset/Retry .selector — focus it instead
        // of bouncing straight back to the filter bar (which stranded it).
        if (gridScroll.render().querySelector('.selector')) {
          Controller.collectionSet(gridScroll.render());
          Controller.collectionFocus(false, gridScroll.render());
          return;
        }
        Controller.toggle('filters');
        return;
      }
      Controller.collectionSet(gridScroll.render());
      Controller.collectionFocus(lastCard || false, gridScroll.render());
    },
    left: function () {
      Controller.moveOr('left', function () {
        Controller.toggle('menu');
      });
    },
    right: function () {
      Controller.moveOr('right');
    },
    up: function () {
      Controller.moveOr('up', function () {
        Controller.toggle('filters');
      });
    },
    down: function () {
      Controller.moveOr('down');
    },
    back: function () {
      Controller.toggle('menu');
    },
  };

  function registerControllers(): void {
    Controller.add('filters', filtersController);
    Controller.add('content', gridController);
  }

  // ---- modal select ----
  function openModal(
    options: Option[],
    current: string,
    restoreMode: string,
    onPick: (v: string) => void,
    title?: string
  ): void {
    const overlay = el('div', 'modal-overlay');
    const panel = el('div', 'modal');
    if (title) panel.appendChild(el('div', 'modal__title', title));
    const list = el('div', 'modal__list');
    // Wrap the options in a vertical translate-Scroll so a long list (year ~30,
    // genres ~20) scrolls with focus: Controller.autoScrollTo walks up to the
    // enclosing .scroll__body and brings the focused row into view.
    const listScroll = new Scroll({ mask: true, over: true });
    list.appendChild(listScroll.render());
    const listBody = listScroll.body();

    let focusItem: HTMLElement | false = false;
    for (let i = 0; i < options.length; i++) {
      const opt = options[i];
      const item = el('div', 'modal__item selector', opt.label);
      if (opt.value === current) {
        item.classList.add('modal__item--current');
        focusItem = item;
      }
      on(item, 'hover:enter', function () {
        close();
        onPick(opt.value);
      });
      listBody.appendChild(item);
    }
    panel.appendChild(list);
    overlay.appendChild(panel);
    container.appendChild(overlay);

    function close(): void {
      if (overlay.parentNode) overlay.parentNode.removeChild(overlay);
      registerControllers();
      Controller.toggle(restoreMode);
    }

    Controller.add('modal', {
      toggle: function () {
        Controller.collectionSet(listScroll.render());
        Controller.collectionFocus(focusItem || false, listScroll.render());
      },
      up: function () {
        Controller.moveOr('up');
      },
      down: function () {
        Controller.moveOr('down');
      },
      back: function () {
        close();
      },
    });
    Controller.toggle('modal');
  }

  // Wire the filter buttons to their modals.
  on(fType.el, 'hover:enter', function () {
    openModal(typeOptions(), state.type, 'filters', function (v) {
      if (v === state.type) return;
      state.type = v;
      state.genre = '';
      loadGenres();
      refreshFilterLabels();
      reload();
    }, t('filter.type'));
  });
  on(fGenre.el, 'hover:enter', function () {
    openModal(genreOptions(), state.genre, 'filters', function (v) {
      if (v === state.genre) return;
      state.genre = v;
      refreshFilterLabels();
      reload();
    }, t('filter.genre'));
  });
  on(fYear.el, 'hover:enter', function () {
    openModal(yearOptions(), state.year, 'filters', function (v) {
      if (v === state.year) return;
      state.year = v;
      refreshFilterLabels();
      reload();
    }, t('filter.year'));
  });
  on(fRating.el, 'hover:enter', function () {
    openModal(ratingOptions(), state.rating, 'filters', function (v) {
      if (v === state.rating) return;
      state.rating = v;
      refreshFilterLabels();
      reload();
    }, t('filter.rating'));
  });

  on(fSort.el, 'hover:enter', function () {
    openModal(sortOptions(), state.sort, 'filters', function (v) {
      if (v === state.sort) return;
      state.sort = v;
      refreshFilterLabels();
      reload();
    }, t('filter.sort'));
  });

  function loadGenres(): void {
    getGenres(state.type).then(
      function (res) {
        if (destroyed) return;
        genres = res && res.genres ? res.genres : [];
        refreshFilterLabels();
      },
      function () {
        genres = [];
      }
    );
  }

  // ---- init ----
  registerControllers();
  refreshFilterLabels();
  loadGenres();
  reload();
  Controller.toggle('filters');

  return {
    destroy: function () {
      destroyed = true;
    },
    pause: function () {
      paused = true;
    },
    resume: function () {
      paused = false;
      registerControllers();
      menu.activate();
      // Back from a title returns to the card you left (home/search already did);
      // the filter bar only when the grid was never entered.
      Controller.toggle(lastCard ? 'content' : 'filters');
    },
  };
}
