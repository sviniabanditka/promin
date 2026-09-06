// Search screen — virtual keyboard (remote-driven) on the left, results grid
// on the right. Keyboard has uk / latin letter layouts + a digits row and
// space / delete / clear / layout-toggle keys. Typing debounces 500ms then
// calls GET /api/v1/catalog/search; results reuse the catalog card grid.
//
// Two Lampa-parity extras (src/interaction/search):
//   - Empty query → a "Recent" list of past queries (core/searchHistory), each
//     focusable: Enter fills the field and searches; an ✕ deletes one.
//   - While typing → a "Suggestions" strip of the matched titles above the
//     card grid; picking one fills the field and searches.
// Both the history rows, suggestion chips and the cards live in the same
// results scroll body, so they all become `.selector`s in the 'results'
// controller collection and the geometric Navigator moves between them.

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
import { iconEl, ICON_SEARCH } from '../ui/icons';
import { openTitle } from './nav';

const MAX_SUGGESTIONS = 6;

export function mountSearch(container: HTMLElement): Screen {
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

  // ---- single search bar (full-width, at the top) ----
  // One control, not two: a real <input> so the TV's own on-screen keyboard
  // (Tizen/webOS IME) opens on OK, showing the typed value itself — no separate
  // query-display bar. The IME writes text and fires 'input' (bypassing the
  // D-pad keydown handler), which drives the same search flow.
  const inputWrap = el('div', 'search-bar selector');
  const searchIco = iconEl(ICON_SEARCH, 'search-bar__ico');
  inputWrap.appendChild(searchIco);
  const input = document.createElement('input');
  input.type = 'text';
  input.className = 'search-input';
  input.setAttribute('autocomplete', 'off');
  input.setAttribute('autocorrect', 'off');
  input.setAttribute('autocapitalize', 'none');
  input.setAttribute('placeholder', t('search.placeholder'));
  inputWrap.appendChild(input);
  const spinner = el('div', 'search-bar__spinner');
  inputWrap.appendChild(spinner);
  panel.appendChild(inputWrap);

  let destroyed = false;
  input.addEventListener('input', function () {
    query = input.value;
    scheduleSearch();
  });
  // Enter/OK inside the IME confirms — close it and drop to results if any.
  input.addEventListener('keydown', function (e: KeyboardEvent) {
    const code = e.keyCode || (e as unknown as { which: number }).which;
    if (code === 13) {
      input.blur();
      if (hasFocusable()) Controller.toggle('results');
    }
  });

  // IME closed by ANY means (confirm / dismiss / tap-away): hand control back to
  // the app. Tizen's IME often does NOT deliver a keydown-13 to the field on
  // confirm, so the keyboard→results transition can't rely on the keydown above
  // — without this the controller stays stuck on 'content' and the NEXT OK (on a
  // result the user thinks is focused) reopens the keyboard instead of opening
  // the title. Guard on name==='content' so we never stomp a pushed title
  // screen's controller when blur fires during navigation away.
  input.addEventListener('blur', function () {
    if (destroyed) return;
    if (Controller.enabled().name !== 'content') return;
    if (hasFocusable()) Controller.toggle('results');
  });

  // Focusing the <input> is what pops the system keyboard. Do it on OK, not on
  // mere D-pad focus, so arrowing onto the field doesn't trap the user in the IME.
  function openKeyboard(): void {
    try {
      input.focus();
    } catch (e) {
      /* ignore — no IME on this platform, field still shows typed value */
    }
  }

  const keyboardController = {
    toggle: function () {
      // The bar itself carries .selector (not a descendant), so collectionSet's
      // descendant-only querySelectorAll misses it — append the bar explicitly,
      // else it's never in the collection and never gets the .focus ring.
      Controller.collectionSet(inputWrap);
      Controller.collectionAppend(inputWrap);
      Controller.collectionFocus(inputWrap, inputWrap);
    },
    enter: function () {
      openKeyboard();
    },
    left: function () {
      Controller.toggle('menu');
    },
    right: function () {
      if (hasFocusable()) Controller.toggle('results');
    },
    down: function () {
      if (hasFocusable()) Controller.toggle('results');
    },
    back: function () {
      if (query.length) {
        applyQuery('');
      } else {
        router.back();
      }
    },
  };

  const keyboard = {
    register: function () {
      Controller.add('content', keyboardController);
    },
    focus: function () {
      Controller.toggle('content');
    },
    setValue: function (v: string) {
      input.value = v;
    },
  };

  // ---- results ----
  const resultsWrap = el('div', 'search-results');
  const resultsScroll = new Scroll({ mask: true, over: true });
  resultsWrap.appendChild(resultsScroll.render());
  panel.appendChild(resultsWrap);
  const resultsBody = resultsScroll.body();
  resultsBody.classList.add('search-results__body');

  let query = '';
  let lastResult: HTMLElement | false = false;
  let debounce = 0;
  let seq = 0;
  let cards: HTMLElement[] = [];

  // Any focusable element in the results column (card, history row, suggestion).
  function hasFocusable(): boolean {
    return !!resultsScroll.render().querySelector('.selector');
  }

  // When the results content is rebuilt while the user is inside the results
  // column (e.g. picking a history entry re-runs the search, deleting a history
  // row shortens the list), refresh the Navigator collection and re-focus so we
  // don't point at removed DOM. No-op while the user is on the keyboard.
  function refreshResultsFocus(): void {
    if (Controller.enabled().name !== 'results') return;
    // Clearing history / deleting the last recent / an empty result leaves only a
    // non-.selector state box in the results column — focusing it is a no-op that
    // strands the ring. Hop back up to the search bar instead.
    if (!hasFocusable()) {
      Controller.toggle('content');
      return;
    }
    Controller.collectionSet(resultsScroll.render());
    Controller.collectionFocus(lastResult || false, resultsScroll.render());
  }

  // ---- states ----
  function showResultsHint(): void {
    empty(resultsBody);
    cards = [];
    lastResult = false;
    resultsBody.appendChild(buildState({ kind: 'empty', text: t('search.hint') }));
  }

  function showResultsEmpty(): void {
    empty(resultsBody);
    cards = [];
    lastResult = false;
    resultsBody.appendChild(buildState({ kind: 'empty', text: t('search.empty') }));
    refreshResultsFocus();
  }

  // Empty field → recent queries (Lampa "history"). Falls back to the hint when
  // there is no history yet.
  function showHistory(): void {
    empty(resultsBody);
    cards = [];
    lastResult = false;

    const items = history.list();
    if (!items.length) {
      showResultsHint();
      return;
    }

    const wrap = el('div', 'search-history');

    const head = el('div', 'search-history__head');
    head.appendChild(el('div', 'search-history__title', t('search.recent')));
    const clearBtn = el('div', 'selector search-history__clear', t('search.clear_history'));
    on(clearBtn, 'hover:enter', function () {
      history.clear();
      showHistory();
      refreshResultsFocus();
    });
    head.appendChild(clearBtn);
    wrap.appendChild(head);

    const listBox = el('div', 'search-history__list');
    for (let i = 0; i < items.length; i++) {
      const q = items[i];
      const row = el('div', 'search-history__item');

      const label = el('div', 'selector search-history__label', q);
      on(
        label,
        'hover:enter',
        (function (qq: string) {
          return function () {
            applyQuery(qq);
          };
        })(q)
      );
      row.appendChild(label);

      const del = el('div', 'selector search-history__del', '✕');
      on(
        del,
        'hover:enter',
        (function (qq: string) {
          return function () {
            history.remove(qq);
            showHistory();
            refreshResultsFocus();
          };
        })(q)
      );
      row.appendChild(del);

      listBox.appendChild(row);
    }
    wrap.appendChild(listBox);
    resultsBody.appendChild(wrap);
    refreshResultsFocus();
  }

  // ---- results loading ----
  // Unique matched titles, top N — shown as tap-to-search suggestion chips.
  function buildSuggestions(items: Card[]): HTMLElement | false {
    const seen: { [k: string]: boolean } = {};
    const titles: string[] = [];
    for (let i = 0; i < items.length && titles.length < MAX_SUGGESTIONS; i++) {
      const title = items[i] && items[i].title ? String(items[i].title) : '';
      if (!title) continue;
      const key = title.toLowerCase();
      if (seen[key]) continue;
      seen[key] = true;
      titles.push(title);
    }
    if (titles.length < 2) return false; // nothing useful to suggest

    const wrap = el('div', 'search-suggest');
    wrap.appendChild(el('div', 'search-suggest__title', t('search.suggestions')));
    const chips = el('div', 'search-suggest__chips');
    for (let i = 0; i < titles.length; i++) {
      const title = titles[i];
      const chip = el('div', 'selector search-suggest__chip', title);
      on(
        chip,
        'hover:enter',
        (function (tt: string) {
          return function () {
            applyQuery(tt);
          };
        })(title)
      );
      chips.appendChild(chip);
    }
    wrap.appendChild(chips);
    return wrap;
  }

  function renderResults(items: Card[]): void {
    empty(resultsBody);
    cards = [];
    lastResult = false;
    if (!items.length) {
      showResultsEmpty();
      return;
    }

    const sugg = buildSuggestions(items);
    if (sugg) resultsBody.appendChild(sugg);

    for (let i = 0; i < items.length; i++) {
      const card = buildCard(items[i]);
      on(card, 'hover:focus', function () {
        lastResult = card;
      });
      on(card, 'hover:enter', function () {
        const tmdb = parseInt(card.getAttribute('data-tmdb') || '0', 10);
        const type = card.getAttribute('data-type') || 'movie';
        if (tmdb) openTitle(type as 'movie' | 'tv', tmdb);
      });
      cards.push(card);
      resultsBody.appendChild(card);
    }
    refreshResultsFocus();
  }

  // Fill the field from a history entry / suggestion and search right away.
  function applyQuery(q: string): void {
    keyboard.setValue(q);
    query = q;
    if (debounce) window.clearTimeout(debounce);
    if (query.trim().length < 1) {
      showHistory();
      return;
    }
    spinner.classList.add('is-active');
    runSearch();
  }

  function scheduleSearch(): void {
    if (debounce) window.clearTimeout(debounce);
    const q = query.trim();
    if (q.length < 1) {
      spinner.classList.remove('is-active');
      showHistory();
      return;
    }
    spinner.classList.add('is-active');
    debounce = window.setTimeout(runSearch, 500);
  }

  function runSearch(): void {
    const q = query.trim();
    const my = ++seq;
    apiSearch({ q: q }).then(
      function (res) {
        if (my !== seq) return;
        spinner.classList.remove('is-active');
        const items = res && res.items ? res.items : [];
        if (items.length) history.add(q);
        renderResults(items);
      },
      function () {
        if (my !== seq) return;
        spinner.classList.remove('is-active');
        showResultsEmpty();
      }
    );
  }

  // ---- controllers ----
  const resultsController = {
    toggle: function () {
      Controller.collectionSet(resultsScroll.render());
      Controller.collectionFocus(lastResult || false, resultsScroll.render());
    },
    left: function () {
      Controller.moveOr('left', function () {
        Controller.toggle('menu');
      }); // leftmost card → rail (up goes to the bar)
    },
    right: function () {
      Controller.moveOr('right');
    },
    up: function () {
      Controller.moveOr('up', function () {
        Controller.toggle('content');
      }); // top row → back up to the search bar
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
  showHistory();
  registerControllers();
  keyboard.focus();

  return {
    destroy: function () {
      destroyed = true;
      head.destroy();
      if (debounce) window.clearTimeout(debounce);
    },
    resume: function () {
      registerControllers();
      menu.activate();
      // Back from an opened result should land on the results grid, not jump
      // focus down to the keyboard.
      if (hasFocusable()) Controller.toggle('results');
      else Controller.toggle('content');
    },
  };
}
