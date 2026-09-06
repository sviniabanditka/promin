// Home screen — vertical stack of horizontal card lanes fed by
// GET /api/v1/catalog/home. Skeleton placeholders while loading, a retry
// banner on error, focus-driven backdrop from the card DTO, Enter opens the
// title screen. Interaction machinery is the ported Lampa model
// (nav geometry + translate3d Scroll + Controller modes), unchanged from
// Phase 0; only the data source and card DTO changed.

import { Navigator } from '../core/nav';
import Controller, { on } from '../core/controller';
import { Scroll } from '../core/scroll';
import { t } from '../core/i18n';
import { ScreenInstance } from '../core/activity';
import { getHome, HomeRow, Card } from '../core/api';
import { el } from '../ui/dom';
import { Background } from '../ui/background';
import { buildHead, Head } from '../ui/head';
import { buildMenu, Menu } from '../ui/menu';
import { buildCard, buildSkeletonCard, revealCards } from '../ui/card';
import { buildFooter } from '../ui/shell';
import { buildState } from '../ui/state';
import { openTitle, openCatalog } from './nav';
import { hasCategoryPreset } from './catalog';

// ---- one lane (items_line controller) ----------------------------------

interface LineHandlers {
  onDown: () => void;
  onUp: () => void;
  onLeft: () => void;
  onToggle: () => void;
  onActive: (lineIndex: number) => void;
  onEnter: (type: string, tmdb: number) => void;
  onMore: (rowId: string) => void;
  onFocus: (backdrop: string) => void;
}

interface Line {
  el: HTMLElement;
  toggle: () => void;
}

function buildLine(row: HomeRow, lineIndex: number, handlers: LineHandlers): Line {
  const outer = el('div', 'items-line');
  outer.appendChild(el('div', 'items-line__title', row.title));

  const bodyWrap = el('div', 'items-line__body');
  const scroll = new Scroll({ horizontal: true, step: 300 });
  bodyWrap.appendChild(scroll.render());
  outer.appendChild(bodyWrap);

  let last: HTMLElement | false = false;
  // Posters beyond the first screenful load when the lane is first entered.
  let revealed = false;
  const EAGER = 8;

  for (let i = 0; i < row.items.length; i++) {
    const item: Card = row.items[i];
    const card = buildCard(item, i >= EAGER);

    on(card, 'hover:focus', function () {
      if (!revealed) {
        revealed = true;
        revealCards(scroll.render());
      }
      last = card;
      handlers.onActive(lineIndex);
      handlers.onFocus(card.getAttribute('data-backdrop') || '');
    });

    on(card, 'hover:enter', function () {
      last = card;
      const tmdb = parseInt(card.getAttribute('data-tmdb') || '0', 10);
      const type = card.getAttribute('data-type') || 'movie';
      if (tmdb) handlers.onEnter(type, tmdb);
    });

    scroll.append(card);
  }

  // "More" tile at the tail of the lane — opens the full catalog for this
  // category with infinite vertical scroll (parity with Lampa's load-more).
  if (hasCategoryPreset(row.id)) {
    const more = el('div', 'card card--more selector');
    const moreView = el('div', 'card__view card__more-view');
    moreView.appendChild(el('div', 'card__more-label', t('catalog.more')));
    more.appendChild(moreView);
    on(more, 'hover:focus', function () {
      last = more;
          handlers.onActive(lineIndex);
    });
    on(more, 'hover:enter', function () {
      handlers.onMore(row.id);
    });
    scroll.append(more);
  }

  const controller = {
    toggle: function () {
      Controller.collectionSet(scroll.render());
      Controller.collectionFocus(last || false, scroll.render());
    },
    right: function () {
      Controller.moveOr('right');
    },
    left: function () {
      Controller.moveOr('left', function () {
        handlers.onLeft();
      });
    },
    down: function () {
      handlers.onDown();
    },
    up: function () {
      handlers.onUp();
    },
    back: function () {
      handlers.onLeft();
    },
  };

  function toggle(): void {
    handlers.onToggle();
    Controller.add('items_line', controller);
    Controller.toggle('items_line');
  }

  return { el: outer, toggle: toggle };
}

// ---- content area (content controller) ---------------------------------

interface Content {
  el: HTMLElement;
  load: () => void;
  activate: () => void;
  destroy: () => void;
}

function buildContent(onBackdrop: (backdrop: string) => void): Content {
  const wrap = el('div', 'content');
  let scroll: Scroll | null = null;
  let lines: Line[] = [];
  let active = 0;
  let destroyed = false;

  function focusLine(index: number): void {
    lines[index].toggle();
  }

  function onDown(): void {
    active = Math.min(active + 1, lines.length - 1);
    focusLine(active);
  }

  function onUp(): void {
    if (active <= 0) {
      active = 0;
    } else {
      active--;
      focusLine(active);
    }
  }

  function onLeft(): void {
    Controller.toggle('menu');
  }

  const contentController = {
    toggle: function () {
      if (lines.length) lines[active].toggle();
    },
    left: function () {
      Controller.moveOr('left', function () {
        Controller.toggle('menu');
      });
    },
    right: function () {
      Navigator.move('right');
    },
    up: function () {
      Controller.moveOr('up');
    },
    down: function () {
      Controller.moveOr('down');
    },
    back: function () {
      Controller.toggle('menu');
    },
  };

  function activate(): void {
    Controller.add('content', contentController);
    Controller.toggle('content');
  }

  function buildLines(rows: HomeRow[]): void {
    while (wrap.firstChild) wrap.removeChild(wrap.firstChild);
    lines = [];
    active = 0;

    const vscroll = new Scroll({ mask: true, over: true });
    scroll = vscroll;
    wrap.appendChild(vscroll.render());

    for (let i = 0; i < rows.length; i++) {
      if (!rows[i].items || !rows[i].items.length) continue;
      const lineIndex = lines.length;
      const line = buildLine(rows[i], lineIndex, {
        onDown: onDown,
        onUp: onUp,
        onLeft: onLeft,
        onActive: function (idx: number) {
          active = idx;
        },
        onToggle: function () {
          if (scroll) scroll.update(line.el);
        },
        onFocus: onBackdrop,
        onEnter: function (type: string, tmdb: number) {
          openTitle(type as 'movie' | 'tv', tmdb);
        },
        onMore: function (rowId: string) {
          openCatalog(rowId);
        },
      });
      lines.push(line);
      vscroll.append(line.el);
    }

    if (lines.length) {
      activate();
    } else {
      // Rows came back but every one was empty (all items filtered out) → no
      // lane, no controller, no focus. Show the error/retry state instead of a
      // blank focusless screen.
      showError();
    }
  }

  function showSkeleton(): void {
    while (wrap.firstChild) wrap.removeChild(wrap.firstChild);
    const box = el('div', 'content__skeleton');
    for (let r = 0; r < 3; r++) {
      const lane = el('div', 'items-line');
      lane.appendChild(el('div', 'items-line__title skeleton-line'));
      const strip = el('div', 'skeleton-strip');
      for (let c = 0; c < 8; c++) strip.appendChild(buildSkeletonCard());
      lane.appendChild(strip);
      box.appendChild(lane);
    }
    wrap.appendChild(box);
  }

  function showError(): void {
    while (wrap.firstChild) wrap.removeChild(wrap.firstChild);
    const center = el('div', 'content__center');
    const box = buildState({
      kind: 'error',
      text: t('error.load'),
      onRetry: function () {
        load();
      },
    });
    center.appendChild(box);
    wrap.appendChild(center);

    Controller.add('content', {
      toggle: function () {
        Controller.collectionSet(box);
        Controller.collectionFocus(false, box);
      },
      left: function () {
        Controller.toggle('menu');
      },
      back: function () {
        Controller.toggle('menu');
      },
    });
    Controller.toggle('content');
  }

  function load(): void {
    showSkeleton();
    getHome().then(
      function (res) {
        if (destroyed) return;
        if (res && res.rows && res.rows.length) {
          buildLines(res.rows);
        } else {
          showError();
        }
      },
      function () {
        if (destroyed) return;
        showError();
      }
    );
  }

  return {
    el: wrap,
    load: load,
    activate: activate,
    destroy: function () {
      destroyed = true;
    },
  };
}

// ---- entry -------------------------------------------------------------

export function mountHome(container: HTMLElement): ScreenInstance {
  container.className += ' home-screen';

  const background = new Background();
  container.appendChild(background.render());

  const menu: Menu = buildMenu('home', 'content');
  container.appendChild(menu.el);

  const head: Head = buildHead();
  container.appendChild(head.el);

  const content = buildContent(function (_backdrop: string) {
    /* backdrop only on the title page — no dynamic backdrop while browsing */
  });
  container.appendChild(content.el);

  container.appendChild(buildFooter());

  content.load();

  return {
    destroy: function () {
      head.destroy();
      content.destroy();
    },
    resume: function () {
      menu.activate();
      content.activate();
    },
  };
}
