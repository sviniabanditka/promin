// Search screen — query bar + on-screen keyboard (ui/keyboard) on the left,
// results grid on the right. Typing debounces 500 ms, then GET
// /api/v1/catalog/search; results reuse the catalog card.
//
// Why an own keyboard and not the host IME: the system keyboard behaved
// differently on every host (Tizen never delivered the confirm key, blur fired
// before the results existed, a stray OK reopened it), so the transition
// "typed → results" was never deterministic. Every key here is a `.selector`
// the Controller owns; nothing is native. A physical keyboard (desktop, USB
// on a TV) still types through the document keydown listener below; a phone
// (`html.is-phone`) gets a native <input> instead of the key grid because it
// has its own touch keyboard.
//
// Empty query → the recent queries (core/searchHistory, synced across the
// profile's devices) as chips in the results column: OK re-runs one, long OK
// deletes it; under them the home "trending" row, so a first-time user sees
// content, not a blank column. A query is remembered when a title is opened
// from its results, not on every keystroke.
//
// With a query: a filter row (all / movies / series → the typed TMDB search),
// a "People" row of actors and directors (untyped search only; OK opens the
// person's filmography), then the card grid. Further pages load when focus
// nears the last card, like the catalog.
//
// Controller modes: 'content' = the keyboard, 'results' = chips or cards.
// Right past the last key → results (when there are any); Left from the first
// column → results' Left at the leftmost card → keyboard; Back in results →
// keyboard; Back on the keyboard clears the query, Back on an empty query
// leaves the screen.

import Controller, { on } from '../core/controller';
import { Scroll } from '../core/scroll';
import { t } from '../core/i18n';
import * as router from '../core/router';
import { ScreenInstance as Screen } from '../core/activity';
import { search as apiSearch, getHome, Card, PersonHit, imgSize } from '../core/api';
import * as history from '../core/searchHistory';
import { el, empty } from '../ui/dom';
import { Background } from '../ui/background';
import { buildMenu, Menu } from '../ui/menu';
import { buildHead, Head } from '../ui/head';
import { buildFooter } from '../ui/shell';
import { buildCard } from '../ui/card';
import { buildState } from '../ui/state';
import { buildKeyboard, Keyboard } from '../ui/keyboard';
import { iconEl, ICON_SEARCH } from '../ui/icons';
import { openTitle, openPerson, searchPath } from './nav';
import { deptLabel } from './person';

const DEBOUNCE_MS = 500;
const PAGE_SIZE_HINT = 6; // prefetch the next page when focus is within N cards of the end
const TRENDING_MAX = 20;

type MediaFilter = '' | 'movie' | 'tv';

export interface SearchParams {
  q?: string; // deep link: run this query on mount
  type?: MediaFilter;
}

export function mountSearch(container: HTMLElement, params?: SearchParams): Screen {
  container.className += ' search-screen';

  const background = new Background();
  container.appendChild(background.render());

  const menu: Menu = buildMenu('search', 'content');
  container.appendChild(menu.el);

  const head: Head = buildHead();
  container.appendChild(head.el);
  container.appendChild(buildFooter());

  const panel = el('div', 'search-panel');
  container.appendChild(panel);
  const left = el('div', 'search-left');
  panel.appendChild(left);

  const phone = document.documentElement.classList.contains('is-phone');

  // ---- query bar (display only on TV; a real field on a phone) ----
  const bar = el('div', 'search-bar');
  bar.appendChild(iconEl(ICON_SEARCH, 'search-bar__ico'));
  let input: HTMLInputElement | null = null;
  let text: HTMLElement | null = null;
  if (phone) {
    input = document.createElement('input');
    input.type = 'text';
    input.className = 'search-input';
    input.setAttribute('autocomplete', 'off');
    input.setAttribute('autocorrect', 'off');
    input.setAttribute('autocapitalize', 'none');
    input.setAttribute('placeholder', t('search.placeholder'));
    bar.appendChild(input);
  } else {
    text = el('div', 'search-input search-input--text');
    bar.appendChild(text);
  }
  const spinner = el('div', 'search-bar__spinner');
  bar.appendChild(spinner);
  left.appendChild(bar);

  // ---- results column ----
  const resultsWrap = el('div', 'search-results');
  const resultsScroll = new Scroll({ mask: true, over: true });
  resultsWrap.appendChild(resultsScroll.render());
  panel.appendChild(resultsWrap);
  const resultsBody = resultsScroll.body();
  resultsBody.classList.add('search-results__body');

  // ---- state ----
  let query = '';
  let filter: MediaFilter = params && params.type ? params.type : '';
  let lastResult: HTMLElement | false = false;
  let debounce = 0;
  let seq = 0; // request generation; a late answer to an older query is dropped
  let destroyed = false;
  // Hidden under a pushed title: leave the Controller alone until resume().
  let paused = false;
  // Paging of the current query.
  let cards: HTMLElement[] = [];
  let currentPage = 0;
  let totalPages = 1;
  let loadingMore = false;
  let loadMoreEl: HTMLElement | null = null;

  function paintQuery(): void {
    if (input) {
      if (input.value !== query) input.value = query;
      return;
    }
    if (!text) return;
    empty(text);
    // The text sits in its own shrinkable flex item aligned to the end, so a
    // query longer than the bar shows its tail (what is being typed) and the
    // caret, not its head.
    if (query) {
      text.classList.remove('search-input--empty');
      text.appendChild(el('span', 'search-input__txt', query));
    } else {
      text.classList.add('search-input--empty');
      text.appendChild(el('span', 'search-input__txt', t('search.placeholder')));
    }
    text.appendChild(el('span', 'search-caret'));
  }

  function hasFocusable(): boolean {
    return !!resultsScroll.render().querySelector('.selector');
  }

  // Results content was rebuilt. If the user is inside the results column,
  // point the Navigator at the new DOM; if nothing is focusable there any more,
  // hand focus back to the keyboard. No-op while the user is on the keyboard.
  function refreshResultsFocus(): void {
    if (paused || Controller.enabled().name !== 'results') return;
    if (!hasFocusable()) {
      Controller.toggle('content');
      return;
    }
    Controller.collectionSet(resultsScroll.render());
    Controller.collectionFocus(lastResult || false, resultsScroll.render());
  }

  function toResults(): void {
    if (hasFocusable()) Controller.toggle('results');
  }

  // A mouse hover on a card/chip (pointer.ts) moves the Navigator collection
  // to the results but leaves the mode on the keyboard, whose index-based
  // moves then go nowhere and whose Back would wipe the query. Switch the mode
  // to match what the ring is on. No-op for D-pad focus (mode already results).
  function hoverIntoResults(): void {
    if (!paused && Controller.enabled().name === 'content') Controller.toggle('results');
  }

  // ---- results column states ----
  function reset(): void {
    empty(resultsBody);
    lastResult = false;
    cards = [];
    loadMoreEl = null;
    loadingMore = false;
    resultsScroll.reset();
  }

  function showHint(): void {
    reset();
    resultsBody.appendChild(buildState({ kind: 'empty', text: t('search.hint') }));
    refreshResultsFocus();
  }

  function showError(): void {
    reset();
    resultsBody.appendChild(
      buildState({
        kind: 'error',
        text: t('error.load'),
        onRetry: function () {
          run();
        },
      })
    );
    refreshResultsFocus();
  }

  // Idle column: recent queries as chips (Enter re-runs one and moves it to
  // the top, long Enter deletes it, the last chip clears the history), then
  // the home trending row as a grid. Both from cache when possible.
  let trendingCards: Card[] | null = null;
  function showHistory(): void {
    reset();
    const items = history.list();
    if (items.length) resultsBody.appendChild(buildHistory(items));
    if (trendingCards) {
      appendTrending(trendingCards);
      refreshResultsFocus();
      return;
    }
    if (!items.length) resultsBody.appendChild(buildState({ kind: 'loading' }));
    refreshResultsFocus();
    const my = ++seq;
    getHome().then(
      function (res) {
        if (destroyed || my !== seq) return;
        let row: Card[] = [];
        const rows = res && res.rows ? res.rows : [];
        for (let i = 0; i < rows.length && !row.length; i++) {
          if (rows[i].id === 'trending' && rows[i].items && rows[i].items.length) row = rows[i].items;
        }
        for (let i = 0; i < rows.length && !row.length; i++) {
          if (rows[i].id !== 'continue_watching' && rows[i].items && rows[i].items.length) row = rows[i].items;
        }
        trendingCards = row.slice(0, TRENDING_MAX);
        if (!query.trim()) showHistory(); // repaint the idle column with the row in place
      },
      function () {
        if (destroyed || my !== seq) return;
        trendingCards = [];
        if (!query.trim() && !history.list().length) showHint();
      }
    );
  }

  function appendTrending(items: Card[]): void {
    if (!items.length) {
      if (!history.list().length) resultsBody.appendChild(buildState({ kind: 'empty', text: t('search.hint') }));
      return;
    }
    resultsBody.appendChild(el('div', 'search-section', t('search.trending')));
    for (let i = 0; i < items.length; i++) resultsBody.appendChild(attachCard(items[i]));
  }

  function buildHistory(items: string[]): HTMLElement {
    const wrap = el('div', 'search-history');
    wrap.appendChild(el('div', 'search-history__title', t('search.recent')));
    wrap.appendChild(el('div', 'search-history__hint', t('search.history_hint')));
    const chips = el('div', 'search-history__chips');
    for (let i = 0; i < items.length; i++) {
      (function (q: string) {
        const chip = el('div', 'selector search-history__chip', q);
        on(chip, 'hover:focus', function () {
          lastResult = chip;
          hoverIntoResults();
        });
        on(chip, 'hover:enter', function () {
          history.add(q); // most recent first
          applyQuery(q);
        });
        on(chip, 'hover:long', function () {
          history.remove(q);
          lastResult = false;
          showHistory();
        });
        chips.appendChild(chip);
      })(items[i]);
    }
    const clear = el('div', 'selector search-history__chip search-history__chip--clear', t('search.clear_history'));
    on(clear, 'hover:enter', function () {
      history.clear();
      lastResult = false;
      showHistory();
    });
    chips.appendChild(clear);
    wrap.appendChild(chips);
    return wrap;
  }

  // A result card: remembers the query when opened, prefetches the next page
  // when focus nears the end of what is loaded.
  function attachCard(item: Card): HTMLElement {
    const card = buildCard(item);
    (card as unknown as { pidx: number }).pidx = cards.length;
    cards.push(card);
    on(card, 'hover:focus', function () {
      lastResult = card;
      hoverIntoResults();
      const idx = (card as unknown as { pidx: number }).pidx;
      if (query.trim() && idx >= cards.length - PAGE_SIZE_HINT && currentPage < totalPages && !loadingMore) {
        loadPage(currentPage + 1);
      }
    });
    on(card, 'hover:enter', function () {
      const tmdb = parseInt(card.getAttribute('data-tmdb') || '0', 10);
      const type = card.getAttribute('data-type') || 'movie';
      if (!tmdb) return;
      if (query.trim()) history.add(query); // a query that led somewhere is worth remembering
      openTitle(type as 'movie' | 'tv', tmdb);
    });
    return card;
  }

  // all / movies / series. Enter re-runs the query through the typed search.
  function buildFilters(): HTMLElement {
    const row = el('div', 'search-filters');
    const defs: { v: MediaFilter; k: string }[] = [
      { v: '', k: 'search.all' },
      { v: 'movie', k: 'search.movies' },
      { v: 'tv', k: 'search.series' },
    ];
    for (let i = 0; i < defs.length; i++) {
      (function (d: { v: MediaFilter; k: string }) {
        const chip = el('div', 'selector search-chip search-filter' + (d.v === filter ? ' is-on' : ''), t(d.k));
        on(chip, 'hover:focus', function () {
          lastResult = chip;
          hoverIntoResults();
        });
        on(chip, 'hover:enter', function () {
          if (d.v === filter) return;
          filter = d.v;
          router.setPath(container, searchPath(query, filter));
          lastResult = false; // the row is rebuilt; land on the chip again by class
          refocusFilter = d.v;
          run();
        });
        row.appendChild(chip);
      })(defs[i]);
    }
    return row;
  }
  let refocusFilter: MediaFilter | null = null;

  // Actors / directors matching the query: round photo + name. Enter → person page.
  function buildPeople(people: PersonHit[]): HTMLElement {
    const wrap = el('div', 'search-people');
    wrap.appendChild(el('div', 'search-section', t('search.people')));
    const row = el('div', 'search-people__row');
    for (let i = 0; i < people.length && i < 8; i++) {
      (function (p: PersonHit) {
        const item = el('div', 'selector search-person');
        if (p.photo) {
          const img = document.createElement('img');
          img.className = 'search-person__photo';
          img.src = imgSize(p.photo, 'w185');
          img.alt = p.name;
          item.appendChild(img);
        } else {
          item.appendChild(el('div', 'search-person__photo search-person__photo--empty', (p.name || '?').charAt(0)));
        }
        item.appendChild(el('div', 'search-person__name', p.name));
        if (p.department) item.appendChild(el('div', 'search-person__dept', deptLabel(p.department)));
        on(item, 'hover:focus', function () {
          lastResult = item;
          hoverIntoResults();
        });
        on(item, 'hover:enter', function () {
          history.add(query);
          openPerson(p.id);
        });
        row.appendChild(item);
      })(people[i]);
    }
    wrap.appendChild(row);
    return wrap;
  }

  function renderResults(items: Card[], people: PersonHit[]): void {
    reset();
    resultsBody.appendChild(buildFilters());
    if (people.length) resultsBody.appendChild(buildPeople(people));
    if (!items.length && !people.length) {
      resultsBody.appendChild(buildState({ kind: 'empty', text: t('search.empty') }));
    }
    for (let i = 0; i < items.length; i++) resultsBody.appendChild(attachCard(items[i]));
    if (refocusFilter !== null) {
      const chips = resultsBody.querySelectorAll('.search-filter');
      const idx = refocusFilter === '' ? 0 : refocusFilter === 'movie' ? 1 : 2;
      lastResult = (chips[idx] as HTMLElement) || false;
      refocusFilter = null;
    }
    refreshResultsFocus();
  }

  function showLoadingMore(): void {
    if (loadMoreEl) return;
    loadMoreEl = el('div', 'catalog-loading-more');
    loadMoreEl.appendChild(el('div', 'state__spinner'));
    loadMoreEl.appendChild(el('div', 'catalog-loading-more__text', t('catalog.loading_more')));
    resultsBody.appendChild(loadMoreEl);
  }
  function hideLoadingMore(): void {
    if (loadMoreEl && loadMoreEl.parentNode) loadMoreEl.parentNode.removeChild(loadMoreEl);
    loadMoreEl = null;
  }

  function appendPage(items: Card[]): void {
    for (let i = 0; i < items.length; i++) {
      const card = attachCard(items[i]);
      resultsBody.appendChild(card);
      if (Controller.enabled().name === 'results') Controller.collectionAppend(card);
    }
  }

  // ---- searching ----
  function searchParams(page: number): { [k: string]: string | number } {
    const p: { [k: string]: string | number } = { q: query.trim(), page: page };
    if (filter) p.type = filter;
    return p;
  }

  // Page 1 of the current query + filter.
  function run(): void {
    loadPage(1);
  }

  function loadPage(page: number): void {
    const q = query.trim();
    if (!q) return;
    if (page > 1) {
      loadingMore = true;
      showLoadingMore();
    } else {
      spinner.classList.add('is-active');
    }
    const my = page <= 1 ? ++seq : seq;
    apiSearch(searchParams(page)).then(
      function (res) {
        if (destroyed || my !== seq) return;
        spinner.classList.remove('is-active');
        loadingMore = false;
        hideLoadingMore();
        totalPages = (res && res.total_pages) || 1;
        currentPage = (res && res.page) || page;
        const items = res && res.items ? res.items : [];
        if (page <= 1) renderResults(items, (res && res.people) || []);
        else appendPage(items);
      },
      function () {
        if (destroyed || my !== seq) return;
        spinner.classList.remove('is-active');
        loadingMore = false;
        hideLoadingMore();
        if (page <= 1) showError();
      }
    );
  }

  // The single entry point for a query change: keyboard, physical keys, phone
  // field, history chip, deep link. Keeps the field, the keyboard's own value
  // and the route in step, then searches (debounced unless `now`).
  function setQuery(v: string, now?: boolean): void {
    query = v || '';
    keyboard.setValue(query);
    paintQuery();
    router.setPath(container, searchPath(query, filter));
    if (debounce) window.clearTimeout(debounce);
    debounce = 0;
    if (!query.trim()) {
      seq++; // an in-flight search for the old text must not repaint over the history
      spinner.classList.remove('is-active');
      showHistory();
      return;
    }
    spinner.classList.add('is-active');
    if (now) run();
    else debounce = window.setTimeout(run, DEBOUNCE_MS);
  }

  function applyQuery(q: string): void {
    setQuery(q, true);
  }

  // ---- keyboard (Controller mode 'content') ----
  const keyboard: Keyboard = buildKeyboard({
    controllerName: 'content',
    onChange: function (v) {
      setQuery(v);
    },
    onLeftEdge: function () {
      Controller.toggle('menu');
    },
    onRightEdge: toResults,
    onBackEmpty: function () {
      router.back();
    },
  });
  left.appendChild(keyboard.el);

  if (input) {
    input.addEventListener('input', function () {
      setQuery(input ? input.value : '');
    });
    // Enter on the phone keyboard: close it and drop onto the results. Deferred
    // a tick: this element listener runs before the Controller's window
    // keydown, and if the field were already blurred the Controller would
    // treat the press as a plain OK and its keyup would "enter" whatever
    // toResults() focused — opening the first card or applying a history chip.
    // With the field still focused it arms swallowEnterUp instead.
    input.addEventListener('keydown', function (e: KeyboardEvent) {
      const code = e.keyCode || (e as unknown as { which: number }).which;
      if (code !== 13) return;
      window.setTimeout(function () {
        if (destroyed) return;
        if (input) input.blur();
        toResults();
      }, 0);
    });
  }

  // Physical keyboard passthrough (desktop browser, USB keyboard on a TV, the
  // digit keys of a remote): printable characters type, Backspace deletes one
  // character. Only while this screen owns input and outside a phone field.
  function onDocKey(e: KeyboardEvent): void {
    if (destroyed || paused || input) return;
    if (e.ctrlKey || e.altKey || e.metaKey) return;
    const mode = Controller.enabled().name;
    if (mode !== 'content' && mode !== 'results') return;
    // A real Backspace key (KeyboardEvent.code, Chrome 48+). Some remotes send
    // keyCode 8 for Back with no `code`: that stays Back (clears the query).
    if (e.code === 'Backspace') {
      if (!query.length) return; // nothing to delete: let Back do its job
      e.preventDefault();
      e.stopPropagation();
      setQuery(query.slice(0, query.length - 1));
      return;
    }
    // KeyboardEvent.key is Chrome 51+; the target TV webviews (Chromium 38–47)
    // only have the legacy keyIdentifier ("U+0041"). Decode that so a USB
    // keyboard or the remote's digit keys type on old sets too.
    let k: string | undefined = (e as { key?: string }).key;
    if (k === undefined) {
      const id = (e as unknown as { keyIdentifier?: string }).keyIdentifier || '';
      k = /^U\+[0-9A-Fa-f]{4}$/.test(id) ? String.fromCharCode(parseInt(id.slice(2), 16)) : '';
    }
    // Exactly one printable character; remotes send names ("ColorF0Red").
    if (k.length !== 1 || k < ' ') return;
    e.preventDefault();
    e.stopPropagation();
    setQuery(query + k);
  }
  document.addEventListener('keydown', onDocKey);

  // ---- results (Controller mode 'results') ----
  const resultsController = {
    toggle: function () {
      Controller.collectionSet(resultsScroll.render());
      Controller.collectionFocus(lastResult || false, resultsScroll.render());
    },
    left: function () {
      Controller.moveOr('left', function () {
        Controller.toggle('content');
      });
    },
    right: function () {
      Controller.moveOr('right');
    },
    up: function () {
      Controller.moveOr('up');
    },
    down: function () {
      Controller.moveOr('down');
    },
    back: function () {
      Controller.toggle('content');
    },
  };

  function registerControllers(): void {
    keyboard.register();
    Controller.add('results', resultsController);
  }

  // History edited on another device (or the boot sync landed): repaint the
  // idle column. While a query is typed the chips are not on screen anyway.
  const unsubHistory = history.subscribe(function () {
    if (destroyed || paused || query.trim()) return;
    showHistory();
  });

  // ---- init ----
  registerControllers();
  paintQuery();
  showHistory();
  keyboard.focus();
  if (params && params.q && params.q.trim()) applyQuery(params.q.trim());

  return {
    destroy: function () {
      destroyed = true;
      head.destroy();
      unsubHistory();
      document.removeEventListener('keydown', onDocKey);
      if (debounce) window.clearTimeout(debounce);
    },
    pause: function () {
      paused = true;
    },
    resume: function () {
      paused = false;
      registerControllers();
      menu.activate();
      // Back from an opened result lands on the results grid, not the keyboard.
      if (hasFocusable()) Controller.toggle('results');
      else Controller.toggle('content');
    },
  };
}
