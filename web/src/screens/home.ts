// Home screen — vertical stack of horizontal card lanes fed by
// GET /api/v1/catalog/home. Skeleton placeholders while loading, a retry
// banner on error, focus-driven backdrop from the card DTO, Enter opens the
// title screen. Interaction machinery is the ported Lampa model
// (nav geometry + translate3d Scroll + Controller modes), unchanged from
// Phase 0; only the data source and card DTO changed.

import { Navigator } from '../core/nav';
import Controller, { ControllerCalls, on } from '../core/controller';
import { Scroll } from '../core/scroll';
import { t } from '../core/i18n';
import { ScreenInstance } from '../core/activity';
import { getHome, HomeRow, Card, ytBrowse, YtItem } from '../core/api';
import { youtubeEnabled } from '../core/features';
import { el } from '../ui/dom';
import { Background } from '../ui/background';
import { buildHead, Head } from '../ui/head';
import { buildMenu, Menu } from '../ui/menu';
import { buildCard, buildSkeletonCard, revealCards } from '../ui/card';
import { buildFooter } from '../ui/shell';
import { buildState } from '../ui/state';
import { openTitle, openCatalog, openYt, YtPageName } from './nav';
import { buildYtCard } from './yt/cards';
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

// ---- YouTube lanes on Home -------------------------------------------------
// Two lanes from the linked account, placed under the first film row:
// "continue watching" (history tiles with a partial progress) and "new in
// subscriptions". Fetched alongside the catalog rows and dropped after a short
// wait or on any error — YouTube must never delay or break the film home.

interface YtLane {
  title: string;
  page: YtPageName;
  items: YtItem[];
}

const YT_LANE_WAIT_MS = 4000;
const YT_LANE_ITEMS = 12;

function ytLanes(): Promise<YtLane[]> {
  if (!youtubeEnabled()) return Promise.resolve([]);
  const fetchLanes = Promise.all([
    ytBrowse('history')['catch'](function () {
      return null;
    }),
    ytBrowse('subscriptions')['catch'](function () {
      return null;
    }),
  ]).then(function (res) {
    const lanes: YtLane[] = [];
    const hist = res[0] && res[0].shelves ? res[0].shelves : [];
    const cont: YtItem[] = [];
    for (let i = 0; i < hist.length; i++) {
      for (let j = 0; j < hist[i].items.length && cont.length < YT_LANE_ITEMS; j++) {
        const it = hist[i].items[j];
        if (it.kind === 'video' && it.progress_pct >= 3 && it.progress_pct <= 95) cont.push(it);
      }
    }
    if (cont.length) lanes.push({ title: t('home.yt_continue'), page: 'history', items: cont });
    const subs = res[1] && res[1].shelves ? res[1].shelves : [];
    const fresh: YtItem[] = [];
    for (let i = 0; i < subs.length; i++) {
      for (let j = 0; j < subs[i].items.length && fresh.length < YT_LANE_ITEMS; j++) {
        if (subs[i].items[j].kind === 'video') fresh.push(subs[i].items[j]);
      }
    }
    if (fresh.length) lanes.push({ title: t('home.yt_new'), page: 'subscriptions', items: fresh });
    return lanes;
  });
  const timeout = new Promise<YtLane[]>(function (resolve) {
    window.setTimeout(function () {
      resolve([]);
    }, YT_LANE_WAIT_MS);
  });
  return Promise.race([fetchLanes, timeout]);
}

function buildYtLine(lane: YtLane, lineIndex: number, handlers: LineHandlers): Line {
  const outer = el('div', 'items-line items-line--yt');
  outer.appendChild(el('div', 'items-line__title', lane.title));
  const bodyWrap = el('div', 'items-line__body');
  const scroll = new Scroll({ horizontal: true, step: 300 });
  bodyWrap.appendChild(scroll.render());
  outer.appendChild(bodyWrap);

  let last: HTMLElement | false = false;
  for (let i = 0; i < lane.items.length; i++) {
    const card = buildYtCard(lane.items[i]);
    on(card, 'hover:focus', function () {
      last = card;
      handlers.onActive(lineIndex);
    });
    scroll.append(card);
  }
  const more = el('div', 'yt-card yt-card--more selector');
  const box = el('div', 'yt-card__thumb');
  box.appendChild(el('div', 'yt-card__more-label', t('yt.more')));
  more.appendChild(box);
  on(more, 'hover:focus', function () {
    last = more;
    handlers.onActive(lineIndex);
  });
  on(more, 'hover:enter', function () {
    openYt(lane.page);
  });
  scroll.append(more);

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
  pause: () => void;
  resume: () => void;
  destroy: () => void;
}

function buildContent(onBackdrop: (backdrop: string) => void): Content {
  const wrap = el('div', 'content');
  let scroll: Scroll | null = null;
  let lines: Line[] = [];
  let active = 0;
  let destroyed = false;
  // Hidden under a pushed screen (a title opened from the bot / a deep link
  // lands before getHome() resolves). The late render must not register or
  // toggle 'content' then — it would steal focus from the visible title and
  // clobber its controller names. resume() does it once we're on top again.
  let paused = false;
  // Controller the visible content wants: the lanes, or the error/retry box.
  let calls: ControllerCalls | null = null;

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

  // Until data arrives the lane controller stands in (no lanes → no focus,
  // but Left still reaches the rail), so a resume() mid-load owns input.
  calls = contentController;

  function activate(): void {
    if (!calls) return;
    Controller.add('content', calls);
    Controller.toggle('content');
  }

  function buildLines(rows: HomeRow[], yt: YtLane[]): void {
    while (wrap.firstChild) wrap.removeChild(wrap.firstChild);
    lines = [];
    active = 0;

    const vscroll = new Scroll({ mask: true, over: true });
    scroll = vscroll;
    wrap.appendChild(vscroll.render());

    function handlersFor(getLine: () => Line | null): LineHandlers {
      return {
        onDown: onDown,
        onUp: onUp,
        onLeft: onLeft,
        onActive: function (idx: number) {
          active = idx;
        },
        onToggle: function () {
          const line = getLine();
          if (scroll && line) scroll.update(line.el);
        },
        onFocus: onBackdrop,
        onEnter: function (type: string, tmdb: number) {
          openTitle(type as 'movie' | 'tv', tmdb);
        },
        onMore: function (rowId: string) {
          openCatalog(rowId);
        },
      };
    }
    function add(build: (lineIndex: number, handlers: LineHandlers) => Line): void {
      let line: Line | null = null;
      line = build(lines.length, handlersFor(function () {
        return line;
      }));
      lines.push(line);
      vscroll.append(line.el);
    }

    let ytPlaced = yt.length === 0;
    for (let i = 0; i < rows.length; i++) {
      if (!rows[i].items || !rows[i].items.length) continue;
      const row = rows[i];
      add(function (lineIndex, handlers) {
        return buildLine(row, lineIndex, handlers);
      });
      if (!ytPlaced) {
        // YouTube lanes right under the first film row.
        ytPlaced = true;
        for (let k = 0; k < yt.length; k++) {
          const lane = yt[k];
          add(function (lineIndex, handlers) {
            return buildYtLine(lane, lineIndex, handlers);
          });
        }
      }
    }

    if (lines.length) {
      calls = contentController;
      if (!paused) activate();
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

    calls = {
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
    };
    if (!paused) activate();
  }

  function load(): void {
    showSkeleton();
    Promise.all([getHome(), ytLanes()]).then(
      function (all) {
        if (destroyed) return;
        const res = all[0];
        if (res && res.rows && res.rows.length) {
          buildLines(res.rows, all[1] || []);
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
    pause: function () {
      paused = true;
    },
    resume: function () {
      paused = false;
      activate();
    },
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
    pause: function () {
      content.pause();
    },
    resume: function () {
      menu.activate();
      content.resume();
    },
  };
}
