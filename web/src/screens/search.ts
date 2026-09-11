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
// Empty query → the recent queries (core/searchHistory) as chips in the
// results column: OK re-runs one, long OK deletes it. A query is remembered
// when a title is opened from its results, not on every keystroke.
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
import { search as apiSearch, Card } from '../core/api';
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
import { openTitle, searchPath } from './nav';

const DEBOUNCE_MS = 500;

export interface SearchParams {
  q?: string; // deep link: run this query on mount
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
  let lastResult: HTMLElement | false = false;
  let debounce = 0;
  let seq = 0; // request generation; a late answer to an older query is dropped
  let destroyed = false;
  // Hidden under a pushed title: leave the Controller alone until resume().
  let paused = false;

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
    resultsScroll.reset();
  }

  function showHint(): void {
    reset();
    resultsBody.appendChild(buildState({ kind: 'empty', text: t('search.hint') }));
    refreshResultsFocus();
  }

  function showEmpty(): void {
    reset();
    resultsBody.appendChild(buildState({ kind: 'empty', text: t('search.empty') }));
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

  // Recent queries as chips. Enter re-runs one (and moves it to the top),
  // long Enter deletes it; the last chip clears the whole history.
  function showHistory(): void {
    reset();
    const items = history.list();
    if (!items.length) {
      showHint();
      return;
    }
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
    resultsBody.appendChild(wrap);
    refreshResultsFocus();
  }

  function renderResults(items: Card[]): void {
    if (!items.length) {
      showEmpty();
      return;
    }
    reset();
    for (let i = 0; i < items.length; i++) {
      const card = buildCard(items[i]);
      on(card, 'hover:focus', function () {
        lastResult = card;
        hoverIntoResults();
      });
      on(card, 'hover:enter', function () {
        const tmdb = parseInt(card.getAttribute('data-tmdb') || '0', 10);
        const type = card.getAttribute('data-type') || 'movie';
        if (!tmdb) return;
        history.add(query); // a query that led somewhere is worth remembering
        openTitle(type as 'movie' | 'tv', tmdb);
      });
      resultsBody.appendChild(card);
    }
    refreshResultsFocus();
  }

  // ---- searching ----
  function run(): void {
    const q = query.trim();
    if (!q) return;
    const my = ++seq;
    spinner.classList.add('is-active');
    apiSearch({ q: q }).then(
      function (res) {
        if (destroyed || my !== seq) return;
        spinner.classList.remove('is-active');
        renderResults(res && res.items ? res.items : []);
      },
      function () {
        if (destroyed || my !== seq) return;
        spinner.classList.remove('is-active');
        showError();
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
    router.setPath(container, searchPath(query));
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
