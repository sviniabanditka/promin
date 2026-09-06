// Library — the unified "my stuff" screen behind a single rail item. A
// vertical stack of horizontal lanes (home-style nav machinery):
//   - "Продовжити"  continue-watching (GET /timecodes/continue), enriched per
//     item via getTitle for a poster + progress bar. Skipped when empty.
//   - "Обране"      favourites, read from the live sync cache (instant, already
//     enriched), with a trailing "More" tile → the full favourites grid.
//   - "Плейлисти"   one tile per playlist (open its items) + a "Керувати" tile
//     that opens the full playlists CRUD screen. Always present so the user can
//     create the first playlist.
//
// Replaces the separate bookmarks + playlists rail entries. The old
// mountBookmarks / mountPlaylists screens stay — reached from the lanes here.
//
// ES5 target (swc): plain functions, no async/await/for-of/spread/find/includes.

import Controller, { on } from '../core/controller';
import { Scroll } from '../core/scroll';
import { t } from '../core/i18n';
import { ScreenInstance } from '../core/activity';
import { getContinue, getPlaylists, getTitleCached, Card, Playlist, ContinueItem } from '../core/api';
import { el } from '../ui/dom';
import { Background } from '../ui/background';
import { buildMenu, Menu } from '../ui/menu';
import { buildHead, Head } from '../ui/head';
import { buildFooter } from '../ui/shell';
import { buildCard } from '../ui/card';
import { buildState } from '../ui/state';
import * as sync from '../core/sync';
import { openTitle, openPlaylist, openBookmarks, openPlaylistsManage, openTorrents } from './nav';

interface Lane {
  el: HTMLElement;
  toggle: () => void;
}

// One horizontal lane. `elements` are already-focusable nodes (cards/tiles) with
// their own hover:enter wired; this adds focus-driven horizontal scrolling and
// the shared 'items_line' controller (up/down between lanes, left → rail).
function buildLane(
  titleText: string,
  elements: HTMLElement[],
  onUp: () => void,
  onDown: () => void,
  onLeft: () => void,
  onToggle: () => void
): Lane {
  const outer = el('div', 'items-line');
  outer.appendChild(el('div', 'items-line__title', titleText));
  const bodyWrap = el('div', 'items-line__body');
  const scroll = new Scroll({ horizontal: true, step: 300 });
  bodyWrap.appendChild(scroll.render());
  outer.appendChild(bodyWrap);

  let last: HTMLElement | false = false;

  for (let i = 0; i < elements.length; i++) {
    (function (node: HTMLElement) {
      on(node, 'hover:focus', function () {
        last = node;
      });
      scroll.append(node);
    })(elements[i]);
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
        onLeft();
      });
    },
    down: onDown,
    up: onUp,
    back: onLeft,
  };

  function toggle(): void {
    onToggle();
    Controller.add('items_line', controller);
    Controller.toggle('items_line');
  }

  return { el: outer, toggle: toggle };
}

// Map a continue-watching row to a Card (progress bar comes from timecode).
function continueCard(rec: ContinueItem, card: Card): Card {
  card.timecode = { position_sec: rec.position_sec, duration_sec: rec.duration_sec, season: rec.season, episode: rec.episode };
  return card;
}

export function mountLibrary(container: HTMLElement): ScreenInstance {
  container.className += ' library-screen home-screen';

  const background = new Background();
  container.appendChild(background.render());

  const menu: Menu = buildMenu('library', 'content');
  container.appendChild(menu.el);

  const head: Head = buildHead();
  container.appendChild(head.el);

  const wrap = el('div', 'content');
  container.appendChild(wrap);
  container.appendChild(buildFooter());

  let scroll: Scroll | null = null;
  let lanes: Lane[] = [];
  let active = 0;
  let destroyed = false;
  // The user has moved focus themselves (rail up/down) since this load started —
  // buildLanes then must NOT yank focus into lane 0 when the data finally lands.
  let touched = false;
  // Lane index to restore after a resume() reload (Back from a pushed screen
  // must not dump the user back at lane 0).
  let restoreLane = 0;

  function focusLane(i: number): void {
    active = i;
    lanes[i].toggle();
  }
  function onDown(): void {
    touched = true;
    if (active < lanes.length - 1) focusLane(active + 1);
  }
  function onUp(): void {
    touched = true;
    if (active > 0) focusLane(active - 1);
  }
  function onLeft(): void {
    touched = true;
    Controller.toggle('menu');
  }

  const contentController = {
    toggle: function () {
      if (lanes.length) lanes[active].toggle();
    },
    left: function () {
      Controller.toggle('menu');
    },
    back: function () {
      Controller.toggle('menu');
    },
  };
  Controller.add('content', contentController);

  function showSpinner(): void {
    while (wrap.firstChild) wrap.removeChild(wrap.firstChild);
    wrap.appendChild(buildState({ kind: 'loading' }));
  }

  // Build a card lane's focusable nodes; each opens the title screen on Enter.
  function cardNodes(cards: Card[], resume?: boolean): HTMLElement[] {
    const nodes: HTMLElement[] = [];
    for (let i = 0; i < cards.length; i++) {
      (function (c: Card) {
        const node = buildCard(c);
        on(node, 'hover:enter', function () {
          openTitle(c.type === 'tv' ? 'tv' : 'movie', c.tmdb_id, resume);
        });
        nodes.push(node);
      })(cards[i]);
    }
    return nodes;
  }

  // A "More"/action tile that looks like the home lane's tail tile.
  function moreTile(label: string, onEnter: () => void): HTMLElement {
    const tile = el('div', 'card card--more selector');
    const view = el('div', 'card__view card__more-view');
    view.appendChild(el('div', 'card__more-label', label));
    tile.appendChild(view);
    on(tile, 'hover:enter', onEnter);
    return tile;
  }

  // A playlist tile: name + item count, opens the playlist's items.
  function playlistTile(pl: Playlist): HTMLElement {
    const tile = el('div', 'card card--playlist selector');
    const view = el('div', 'card__view card__more-view');
    view.appendChild(el('div', 'card__pl-name', pl.name || ''));
    const n = pl.items_count != null ? pl.items_count : 0;
    view.appendChild(el('div', 'card__pl-count', t('playlists.count', { n: String(n) })));
    tile.appendChild(view);
    on(tile, 'hover:enter', function () {
      openPlaylist(pl.id, pl.name || '');
    });
    return tile;
  }

  function buildLanes(continueCards: Card[], playlists: Playlist[]): void {
    const keepLane = active; // where the user IS, in case they moved during the load
    while (wrap.firstChild) wrap.removeChild(wrap.firstChild);
    lanes = [];
    active = 0;

    const vscroll = new Scroll({ mask: true, over: true });
    scroll = vscroll;
    wrap.appendChild(vscroll.render());

    function addLane(title: string, nodes: HTMLElement[]): void {
      const idx = lanes.length;
      const lane = buildLane(title, nodes, onUp, onDown, onLeft, function () {
        if (scroll) scroll.update(lane.el);
      });
      // keep `active` in sync when a card inside this lane takes focus
      for (let j = 0; j < nodes.length; j++) {
        on(nodes[j], 'hover:focus', function () {
          active = idx;
        });
      }
      lanes.push(lane);
      vscroll.append(lane.el);
    }

    // OK on a continue card = resume playback (the most frequent action was
    // 5-6 presses deep: title → watch → source → episode → play).
    if (continueCards.length) addLane(t('library.continue'), cardNodes(continueCards, true));

    const favs = sync.getBookmarksList();
    if (favs.length) {
      const favCards: Card[] = [];
      for (let i = 0; i < favs.length; i++) {
        const b = favs[i];
        favCards.push({
          tmdb_id: b.tmdb_id,
          type: b.media_type === 'tv' ? 'tv' : 'movie',
          title: b.title || '',
          poster: b.poster || null,
          backdrop: b.backdrop || null,
          year: b.year != null ? b.year : null,
          rating: b.rating != null ? b.rating : null,
        });
      }
      const favNodes = cardNodes(favCards);
      favNodes.push(moreTile(t('catalog.more'), function () { openBookmarks(); }));
      addLane(t('library.favorites'), favNodes);
    }

    // Playlists lane: always present (manage tile lets the user create the first).
    const plNodes: HTMLElement[] = [];
    for (let i = 0; i < playlists.length; i++) plNodes.push(playlistTile(playlists[i]));
    plNodes.push(moreTile(t('library.manage'), function () { openPlaylistsManage(); }));
    addLane(t('library.playlists'), plNodes);

    // Downloads (torrents moved off the rail into the Library).
    addLane(t('menu.torrents'), [moreTile(t('catalog.more'), function () { openTorrents(); })]);

    Controller.add('content', contentController);
    if (!lanes.length) {
      Controller.toggle('menu'); // nothing focusable — park on the rail
      return;
    }
    // The old DOM is gone either way, so SOME focus must be set — returning
    // early on `touched` left no ring at all and Right/Enter hitting detached
    // nodes. Keep the lane the user moved to; else restore the pre-reload one.
    let target = touched ? keepLane : restoreLane;
    if (target > lanes.length - 1) target = lanes.length - 1;
    if (target < 0) target = 0;
    restoreLane = 0;
    focusLane(target);
    Controller.toggle('content');
  }

  function load(silent?: boolean): void {
    // silent (resume path): keep the current lanes painted instead of blanking
    // to a full-screen spinner — buildLanes swaps them out when data arrives.
    if (!silent) showSpinner();
    // Kicked off together with getContinue: it used to wait for the whole
    // continue enrichment (up to 20 title fetches) before even starting.
    const plP = getPlaylists();
    // Continue needs per-item enrichment; playlists are one call. Favourites are
    // read synchronously from the sync cache inside buildLanes.
    getContinue(20).then(
      function (res) {
        const recs = res && res.items ? res.items : [];
        const jobs: Promise<Card | null>[] = [];
        for (let i = 0; i < recs.length; i++) {
          (function (rec: ContinueItem) {
            const type = rec.media_type === 'tv' ? 'tv' : 'movie';
            jobs.push(
              getTitleCached(type, rec.tmdb_id).then(
                function (card) { return card ? continueCard(rec, card) : null; },
                function () { return null; }
              )
            );
          })(recs[i]);
        }
        Promise.all(jobs).then(function (cards) {
          if (destroyed) return;
          const clean: Card[] = [];
          for (let i = 0; i < cards.length; i++) if (cards[i]) clean.push(cards[i] as Card);
          plP.then(
            function (plres) {
              if (destroyed) return;
              buildLanes(clean, plres && plres.playlists ? plres.playlists : []);
            },
            function () {
              if (destroyed) return;
              buildLanes(clean, []);
            }
          );
        });
      },
      function () {
        if (destroyed) return;
        // Continue failed → still show favourites + playlists.
        plP.then(
          function (plres) {
            if (destroyed) return;
            buildLanes([], plres && plres.playlists ? plres.playlists : []);
          },
          function () {
            if (destroyed) return;
            buildLanes([], []);
          }
        );
      }
    );
  }

  load();
  Controller.toggle('menu');

  return {
    destroy: function () {
      destroyed = true;
      head.destroy();
    },
    resume: function () {
      // 'content' was overwritten by a pushed screen (title/playlist items);
      // rebuild from fresh data so a just-watched item / new playlist shows.
      menu.activate();
      Controller.add('content', contentController);
      restoreLane = active; // land back on the lane the user left from
      touched = false; // a fresh reload may re-focus; user hasn't moved yet
      // Own input while load() is in flight — without a live controller a d-pad
      // press during the (multi-second) reload dispatches to the destroyed
      // screen's closures over detached DOM. buildLanes takes over on arrival.
      Controller.toggle('content');
      load(true); // silent: keep the old lanes visible while refetching
    },
  };
}
