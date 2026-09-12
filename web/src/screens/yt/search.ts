// YouTube search: the same on-screen keyboard as the main search on the left,
// YouTube result shelves on the right. Debounced; the query rides in the route.

import Controller from '../../core/controller';
import { Scroll } from '../../core/scroll';
import { t } from '../../core/i18n';
import * as router from '../../core/router';
import { ScreenInstance } from '../../core/activity';
import { ytSearch, YtFeed } from '../../core/api';
import { el, empty } from '../../ui/dom';
import { Background } from '../../ui/background';
import { buildMenu, Menu } from '../../ui/menu';
import { buildHead, Head } from '../../ui/head';
import { buildFooter } from '../../ui/shell';
import { buildState } from '../../ui/state';
import { buildKeyboard, Keyboard } from '../../ui/keyboard';
import { iconEl, ICON_SEARCH } from '../../ui/icons';
import { buildYtShelf } from './cards';

export interface YtSearchParams {
  q: string;
}

const DEBOUNCE_MS = 600;

export function mountYtSearch(container: HTMLElement, params: YtSearchParams): ScreenInstance {
  container.className += ' search-screen yt-screen yt-search-screen';

  const background = new Background();
  container.appendChild(background.render());
  const menu: Menu = buildMenu('youtube', 'content');
  container.appendChild(menu.el);
  const head: Head = buildHead();
  container.appendChild(head.el);
  container.appendChild(buildFooter());

  const panel = el('div', 'search-panel');
  container.appendChild(panel);
  const left = el('div', 'search-left');
  panel.appendChild(left);
  const bar = el('div', 'search-bar');
  bar.appendChild(iconEl(ICON_SEARCH, 'search-bar__ico'));
  const text = el('div', 'search-input search-input--text');
  bar.appendChild(text);
  const spinner = el('div', 'search-bar__spinner');
  bar.appendChild(spinner);
  left.appendChild(bar);

  const resultsWrap = el('div', 'search-results');
  const scroll = new Scroll({ mask: true, over: true });
  resultsWrap.appendChild(scroll.render());
  panel.appendChild(resultsWrap);
  const body = scroll.body();
  body.classList.add('yt-content__body');

  let query = '';
  let debounce = 0;
  let seq = 0;
  let destroyed = false;
  let paused = false;
  let lastCard: HTMLElement | false = false;

  function paintQuery(): void {
    empty(text);
    text.classList.toggle('search-input--empty', !query);
    text.appendChild(el('span', 'search-input__txt', query || t('search.placeholder')));
    text.appendChild(el('span', 'search-caret'));
  }

  function hasFocusable(): boolean {
    return !!scroll.render().querySelector('.selector');
  }

  function refresh(): void {
    if (paused || Controller.enabled().name !== 'results') return;
    if (!hasFocusable()) {
      Controller.toggle('content');
      return;
    }
    Controller.collectionSet(scroll.render());
    Controller.collectionFocus(lastCard || false, scroll.render());
  }

  function render(feed: YtFeed): void {
    empty(body);
    lastCard = false;
    const shelves = feed && feed.shelves ? feed.shelves : [];
    if (!shelves.length) body.appendChild(buildState({ kind: 'empty', text: t('search.empty') }));
    for (let i = 0; i < shelves.length; i++) {
      body.appendChild(
        buildYtShelf(shelves[i], {
          loadMore: function (cont) {
            return ytSearch('', cont);
          },
          onFocus: function (card) {
            lastCard = card;
          },
          onAppended: function (cards) {
            if (Controller.enabled().name === 'results') {
              for (let c = 0; c < cards.length; c++) Controller.collectionAppend(cards[c]);
              if (cards.length) Controller.focus(cards[0]);
            }
          },
        })
      );
    }
    scroll.reset();
    refresh();
  }

  function run(): void {
    const q = query.trim();
    if (!q) return;
    const my = ++seq;
    spinner.classList.add('is-active');
    ytSearch(q).then(
      function (feed) {
        if (destroyed || my !== seq) return;
        spinner.classList.remove('is-active');
        render(feed);
      },
      function () {
        if (destroyed || my !== seq) return;
        spinner.classList.remove('is-active');
        empty(body);
        lastCard = false;
        body.appendChild(buildState({ kind: 'error', text: t('error.load'), onRetry: run }));
        refresh();
      }
    );
  }

  function setQuery(v: string, now?: boolean): void {
    query = v || '';
    keyboard.setValue(query);
    paintQuery();
    router.setPath(container, '/yt/search' + (query.trim() ? '?q=' + encodeURIComponent(query.trim()) : ''));
    if (debounce) window.clearTimeout(debounce);
    debounce = 0;
    if (!query.trim()) {
      seq++;
      spinner.classList.remove('is-active');
      empty(body);
      lastCard = false;
      body.appendChild(buildState({ kind: 'empty', text: t('search.hint') }));
      refresh();
      return;
    }
    spinner.classList.add('is-active');
    if (now) run();
    else debounce = window.setTimeout(run, DEBOUNCE_MS);
  }

  const keyboard: Keyboard = buildKeyboard({
    controllerName: 'content',
    onChange: function (v) {
      setQuery(v);
    },
    onLeftEdge: function () {
      Controller.toggle('menu');
    },
    onRightEdge: function () {
      if (hasFocusable()) Controller.toggle('results');
    },
    onBackEmpty: function () {
      router.back();
    },
  });
  left.appendChild(keyboard.el);

  const resultsController = {
    toggle: function () {
      Controller.collectionSet(scroll.render());
      Controller.collectionFocus(lastCard || false, scroll.render());
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

  function register(): void {
    keyboard.register();
    Controller.add('results', resultsController);
  }

  register();
  paintQuery();
  body.appendChild(buildState({ kind: 'empty', text: t('search.hint') }));
  keyboard.focus();
  if (params.q) setQuery(params.q, true);

  return {
    destroy: function () {
      destroyed = true;
      head.destroy();
      if (debounce) window.clearTimeout(debounce);
    },
    pause: function () {
      paused = true;
    },
    resume: function () {
      paused = false;
      register();
      menu.activate();
      if (hasFocusable()) Controller.toggle('results');
      else Controller.toggle('content');
    },
  };
}
