// Live TV screen (docs/tv.md): three columns. Left — sections (favourites,
// recent, all, countries, categories); middle — the channels of the focused
// section with what is on now; right — the focused channel's full programme,
// browsable before opening the channel. OK on a channel or on any programme
// row opens the live player. Long-press OK on a channel toggles favourite.
// Controller modes: 'menu' (rail), 'tv_side', 'tv_list', 'tv_prog'.

import Controller, { on } from '../../core/controller';
import { Scroll } from '../../core/scroll';
import { t } from '../../core/i18n';
import { ScreenInstance } from '../../core/activity';
import { getTvMeta, getTvChannels, getTvNow, tvFavorite, TvChannel, TvCategory, TvCountry, ApiError } from '../../core/api';
import { openLivePlayer } from '../../core/player/live';
import { el, empty } from '../../ui/dom';
import { Background } from '../../ui/background';
import { buildMenu, Menu } from '../../ui/menu';
import { buildHead, Head } from '../../ui/head';
import { buildFooter } from '../../ui/shell';
import { buildState } from '../../ui/state';
import { toast } from '../../ui/toast';
import { ChannelList, ProgramPane, GuideChannel, loadNowNext } from './guide';

export type TvSection = 'fav' | 'recent' | 'all' | 'country' | 'category';

export interface TvParams {
  section: TvSection;
  id?: string; // country code or category id
}

const SIDE_CATEGORIES = 10;

interface SectionDef {
  section: TvSection;
  id?: string;
  label: string;
}

function toGuide(list: TvChannel[]): GuideChannel[] {
  const out: GuideChannel[] = [];
  for (let i = 0; i < list.length; i++) {
    const c = list[i];
    out.push({ id: c.id, name: c.name, logo: c.logo, quality: c.quality, favorite: c.favorite });
  }
  return out;
}

export function mountTv(container: HTMLElement, params: TvParams): ScreenInstance {
  container.className += ' yt-screen tv-screen';

  const background = new Background();
  container.appendChild(background.render());
  const menu: Menu = buildMenu('tv', 'tv_side');
  container.appendChild(menu.el);
  const head: Head = buildHead();
  container.appendChild(head.el);
  container.appendChild(buildFooter());

  const panel = el('div', 'yt-panel tvs');
  container.appendChild(panel);
  const side = el('div', 'yt-side tv-side');
  panel.appendChild(side);
  const sideScroll = new Scroll({ mask: true, over: true });
  side.appendChild(sideScroll.render());
  const sideBody = sideScroll.body();
  const listCol = el('div', 'tvs__list');
  panel.appendChild(listCol);
  const progCol = el('div', 'tvs__prog');
  panel.appendChild(progCol);

  let destroyed = false;
  let paused = false;
  let lastSide: HTMLElement | false = false;
  let current: SectionDef = { section: params.section, id: params.id, label: '' };
  let seq = 0;
  let sideTimer = 0;

  // ---- channels + programme ----
  const list = new ChannelList({
    onFocus: function (i) {
      prog.show(list.channels[i] || null);
    },
    onEnter: function (i) {
      play(i);
    },
    onLong: function (i) {
      toggleFavorite(i);
    },
  });
  listCol.appendChild(list.render());
  const listState = el('div', 'tvs__state hide');
  listCol.appendChild(listState);
  const prog = new ProgramPane({
    onEnter: function (ch) {
      const i = list.indexOfId(ch.id);
      if (i >= 0) play(i);
    },
  });
  progCol.appendChild(prog.el);

  function showListState(kind: 'loading' | 'empty' | 'error', text?: string, onRetry?: () => void): void {
    empty(listState);
    listState.appendChild(buildState({ kind: kind, text: text, onRetry: onRetry }));
    listState.classList.remove('hide');
    list.render().classList.add('hide');
  }
  function hideListState(): void {
    listState.classList.add('hide');
    list.render().classList.remove('hide');
  }

  function loadSection(def: SectionDef): void {
    const my = ++seq;
    current = def;
    prog.show(null);
    showListState('loading');
    const req =
      def.section === 'fav'
        ? getTvChannels({ fav: '1' })
        : def.section === 'recent'
          ? getTvChannels({ recent: '1' })
          : def.section === 'all'
            ? getTvChannels({})
            : def.section === 'country'
              ? getTvChannels({ country: def.id || '' })
              : getTvChannels({ category: def.id || '' });
    req.then(
      function (r) {
        if (destroyed || my !== seq) return;
        const items = toGuide(r.items || []);
        if (!items.length) {
          list.setChannels([]);
          showListState('empty', def.section === 'fav' ? t('tv.no_favorites') : def.section === 'recent' ? t('tv.no_recent') : t('tv.empty'));
          return;
        }
        hideListState();
        list.setChannels(items);
        prog.show(items[0], true);
        loadNowNext(getTvNow).then(function (nn) {
          if (!destroyed && my === seq) list.setNow(nn);
        });
        if (Controller.enabled().name === 'tv_list') Controller.toggle('tv_list');
      },
      function (e: ApiError) {
        if (destroyed || my !== seq) return;
        showListState('error', e && e.code === 'tv_disabled' ? t('tv.unavailable') : t('error.load'), function () {
          loadSection(def);
        });
      }
    );
  }

  // ---- sidebar ----
  function sideItem(def: SectionDef, isCurrent: boolean): HTMLElement {
    const item = el('div', 'yt-side__item selector' + (isCurrent ? ' is-current' : ''), def.label);
    on(item, 'hover:focus', function () {
      lastSide = item;
      // Browsing the sections previews their channels after a short pause.
      if (sideTimer) clearTimeout(sideTimer);
      sideTimer = window.setTimeout(function () {
        sideTimer = 0;
        if (def.section !== current.section || (def.id || '') !== (current.id || '')) selectSection(def, item);
      }, 350);
    });
    on(item, 'hover:enter', function () {
      if (sideTimer) {
        clearTimeout(sideTimer);
        sideTimer = 0;
      }
      if (def.section !== current.section || (def.id || '') !== (current.id || '')) selectSection(def, item);
      if (list.count()) Controller.toggle('tv_list');
    });
    sideBody.appendChild(item);
    if (isCurrent) lastSide = item;
    return item;
  }

  function selectSection(def: SectionDef, item: HTMLElement): void {
    const items = sideBody.querySelectorAll('.yt-side__item');
    for (let i = 0; i < items.length; i++) items[i].classList.remove('is-current');
    item.classList.add('is-current');
    loadSection(def);
  }

  function renderSide(countries: TvCountry[], categories: TvCategory[]): void {
    empty(sideBody);
    const is = function (s: TvSection, id?: string) {
      return params.section === s && (id || '') === (params.id || '');
    };
    sideItem({ section: 'fav', label: t('tv.favorites') }, is('fav'));
    sideItem({ section: 'recent', label: t('tv.recent') }, is('recent'));
    sideItem({ section: 'all', label: t('tv.all') }, is('all'));
    sideBody.appendChild(el('div', 'tv-side__heading', t('tv.countries')));
    for (let i = 0; i < countries.length; i++) {
      const c = countries[i];
      sideItem({ section: 'country', id: c.code, label: (c.flag ? c.flag + ' ' : '') + c.name }, is('country', c.code));
    }
    sideBody.appendChild(el('div', 'tv-side__heading', t('tv.categories')));
    for (let i = 0; i < Math.min(SIDE_CATEGORIES, categories.length); i++) {
      const g = categories[i];
      sideItem({ section: 'category', id: g.id, label: g.name }, is('category', g.id));
    }
    sideScroll.reset();
  }

  // ---- controllers ----
  Controller.add('tv_side', {
    toggle: function () {
      Controller.collectionSet(sideScroll.render());
      Controller.collectionFocus(lastSide || false, sideScroll.render());
    },
    left: function () {
      Controller.toggle('menu');
    },
    right: function () {
      if (list.count()) Controller.toggle('tv_list');
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
  });
  Controller.add('tv_list', {
    toggle: function () {
      Controller.collectionSet(list.render());
      Controller.collectionFocus(list.focusTarget(), list.render());
    },
    left: function () {
      Controller.toggle('tv_side');
    },
    right: function () {
      if (prog.hasRows()) Controller.toggle('tv_prog');
    },
    up: function () {
      Controller.moveOr('up');
    },
    down: function () {
      Controller.moveOr('down');
    },
    back: function () {
      Controller.toggle('tv_side');
    },
  });
  Controller.add('tv_prog', {
    toggle: function () {
      Controller.collectionSet(prog.scroll.render());
      Controller.collectionFocus(prog.focusTarget(), prog.scroll.render());
    },
    left: function () {
      Controller.toggle('tv_list');
    },
    up: function () {
      Controller.moveOr('up');
    },
    down: function () {
      Controller.moveOr('down');
    },
    back: function () {
      Controller.toggle('tv_list');
    },
  });

  // ---- actions ----
  function play(i: number): void {
    if (!list.channels[i]) return;
    openLivePlayer({ channels: list.channels.slice(0), index: i, sectionLabel: current.label });
  }

  function toggleFavorite(i: number): void {
    const ch = list.channels[i];
    if (!ch) return;
    const next = !ch.favorite;
    tvFavorite(ch.id, next).then(
      function () {
        if (destroyed) return;
        list.setFavorite(i, next);
        toast({ kind: 'success', icon: '★', text: t(next ? 'tv.fav_added' : 'tv.fav_removed') });
        if (current.section === 'fav' && !next) {
          list.removeAt(i);
          if (!list.count()) {
            showListState('empty', t('tv.no_favorites'));
            prog.show(null);
            Controller.toggle('tv_side');
          } else if (Controller.enabled().name === 'tv_list') {
            Controller.toggle('tv_list');
          }
        }
      },
      function () {
        toast({ kind: 'error', text: t('error.load') });
      }
    );
  }

  // ---- boot ----
  Controller.toggle('tv_side');
  getTvMeta().then(
    function (m) {
      if (destroyed) return;
      renderSide(m.countries || [], m.categories || []);
      if (!paused) Controller.toggle('tv_side');
    },
    function (e: ApiError) {
      if (destroyed) return;
      renderSide([], []);
      showListState('error', e && e.code === 'tv_disabled' ? t('tv.unavailable') : t('error.load'));
      if (!paused) Controller.toggle('tv_side');
    }
  );
  loadSection({ section: params.section, id: params.id, label: '' });

  return {
    destroy: function () {
      destroyed = true;
      if (sideTimer) clearTimeout(sideTimer);
      head.destroy();
      list.destroy();
      prog.destroy();
      Controller.remove('tv_side');
      Controller.remove('tv_list');
      Controller.remove('tv_prog');
    },
    pause: function () {
      paused = true;
    },
    resume: function () {
      paused = false;
      menu.activate();
      // Back from the player: "recent" may have changed, favourites too — but
      // the list itself is still valid; refresh now/next which surely moved.
      loadNowNext(getTvNow).then(function (nn) {
        if (!destroyed) list.setNow(nn);
      });
      if (list.count()) Controller.toggle('tv_list');
      else Controller.toggle('tv_side');
    },
  };
}
