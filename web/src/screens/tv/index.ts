// Live TV (docs/tv.md): the rail's "TV" item. Left, a sidebar of sections —
// favourites, recent, the configured countries, the biggest categories;
// right, a grid of channel tiles (logo, name, best quality). OK plays the
// channel in the ordinary player as a live HLS stream; the player's
// prev/next buttons zap through the current list. Long-press OK toggles the
// favourite. Controller modes: 'menu' (rail), 'tv_side', 'tv_content'.

import Controller, { on } from '../../core/controller';
import { Scroll } from '../../core/scroll';
import { t } from '../../core/i18n';
import * as router from '../../core/router';
import { ScreenInstance } from '../../core/activity';
import { getTvMeta, getTvChannels, getTvEpg, getTvNow, tvPlay, tvFail, tvFavorite, mediaUrl, TvChannel, TvCategory, TvCountry, ApiError } from '../../core/api';
import { openPlayer, PlayerMedia, EpisodeMeta } from '../../core/player/index';
import { PlayerTv } from '../../core/player/tvguide';
import { el, empty } from '../../ui/dom';
import { Background } from '../../ui/background';
import { buildMenu, Menu } from '../../ui/menu';
import { buildHead, Head } from '../../ui/head';
import { buildFooter } from '../../ui/shell';
import { buildState } from '../../ui/state';
import { toast } from '../../ui/toast';

export type TvSection = 'fav' | 'recent' | 'all' | 'country' | 'category';

export interface TvParams {
  section: TvSection;
  id?: string; // country code or category id
}

const SIDE_CATEGORIES = 8;

function sectionPath(section: TvSection, id?: string): string {
  if (section === 'country') return '/tv/c/' + encodeURIComponent(id || '');
  if (section === 'category') return '/tv/g/' + encodeURIComponent(id || '');
  return '/tv/' + section;
}

function liveMedia(url: string, direct: boolean): PlayerMedia {
  return { type: 'hls', streams: [{ url: direct ? url : mediaUrl(url) }], subtitles: [], voices: [] };
}

// Logos load lazily (data-logo → loadTvLogo): "all channels" is ~2k tiles and
// the first paint must not fire 2k image requests at the logo proxy.
export function loadTvLogo(card: HTMLElement): void {
  const src = card.getAttribute('data-logo');
  if (!src) return;
  card.removeAttribute('data-logo');
  const box = card.querySelector('.tv-card__logo') as HTMLElement | null;
  if (!box) return;
  const img = document.createElement('img');
  img.alt = '';
  img.onerror = function () {
    img.onerror = null;
    img.style.display = 'none';
    box.appendChild(el('div', 'tv-card__initial', (card.getAttribute('data-name') || '?').slice(0, 2).toUpperCase()));
  };
  img.src = src;
  box.insertBefore(img, box.firstChild);
}

export function buildTvCard(ch: TvChannel, lazy?: boolean): HTMLElement {
  const card = el('div', 'tv-card selector' + (ch.favorite ? ' is-fav' : ''));
  card.setAttribute('data-id', ch.id);
  card.setAttribute('data-name', ch.name || '');
  const box = el('div', 'tv-card__logo');
  card.appendChild(box);
  if (ch.logo) {
    card.setAttribute('data-logo', ch.logo);
    if (!lazy) loadTvLogo(card);
  } else {
    box.appendChild(el('div', 'tv-card__initial', (ch.name || '?').slice(0, 2).toUpperCase()));
  }
  if (ch.quality) box.appendChild(el('div', 'tv-card__badge', ch.quality));
  box.appendChild(el('div', 'tv-card__star', '★'));
  card.appendChild(el('div', 'tv-card__name', ch.name));
  return card;
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

  const panel = el('div', 'yt-panel');
  container.appendChild(panel);
  const side = el('div', 'yt-side tv-side');
  panel.appendChild(side);
  const sideScroll = new Scroll({ mask: true, over: true });
  side.appendChild(sideScroll.render());
  const sideBody = sideScroll.body();
  const content = el('div', 'yt-content');
  panel.appendChild(content);
  const scroll = new Scroll({ mask: true, over: true });
  content.appendChild(scroll.render());
  const body = scroll.body();
  body.classList.add('yt-content__body');

  let destroyed = false;
  let paused = false;
  let lastSide: HTMLElement | false = false;
  let lastCard: HTMLElement | false = false;
  let channels: TvChannel[] = [];
  let seq = 0;

  // ---- sidebar ----
  function sideItem(label: string, current: boolean, run: () => void): HTMLElement {
    const item = el('div', 'yt-side__item selector' + (current ? ' is-current' : ''), label);
    on(item, 'hover:focus', function () {
      lastSide = item;
    });
    on(item, 'hover:enter', run);
    sideBody.appendChild(item);
    if (current) lastSide = item;
    return item;
  }

  function renderSide(countries: TvCountry[], categories: TvCategory[]): void {
    empty(sideBody);
    const go = function (section: TvSection, id?: string) {
      return function () {
        if (section === params.section && (id || '') === (params.id || '')) {
          if (body.querySelector('.selector')) Controller.toggle('tv_content');
          return;
        }
        router.replaceRoot(function (c) {
          return mountTv(c, { section: section, id: id });
        }, sectionPath(section, id));
      };
    };
    sideItem(t('tv.favorites'), params.section === 'fav', go('fav'));
    sideItem(t('tv.recent'), params.section === 'recent', go('recent'));
    sideItem(t('tv.all'), params.section === 'all', go('all'));
    sideBody.appendChild(el('div', 'tv-side__heading', t('tv.countries')));
    for (let i = 0; i < countries.length; i++) {
      const c = countries[i];
      sideItem((c.flag ? c.flag + ' ' : '') + c.name, params.section === 'country' && params.id === c.code, go('country', c.code));
    }
    sideBody.appendChild(el('div', 'tv-side__heading', t('tv.categories')));
    for (let i = 0; i < Math.min(SIDE_CATEGORIES, categories.length); i++) {
      const g = categories[i];
      sideItem(g.name, params.section === 'category' && params.id === g.id, go('category', g.id));
    }
  }

  const sideController = {
    toggle: function () {
      Controller.collectionSet(sideScroll.render());
      Controller.collectionFocus(lastSide || false, sideScroll.render());
    },
    left: function () {
      Controller.toggle('menu');
    },
    right: function () {
      if (body.querySelector('.selector')) Controller.toggle('tv_content');
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

  const contentController = {
    toggle: function () {
      Controller.collectionSet(scroll.render());
      Controller.collectionFocus(lastCard || false, scroll.render());
    },
    left: function () {
      Controller.moveOr('left', function () {
        Controller.toggle('tv_side');
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
      Controller.toggle('tv_side');
    },
  };

  function register(): void {
    Controller.add('tv_side', sideController);
    Controller.add('tv_content', contentController);
  }

  function focusContentOrSide(): void {
    if (paused) return;
    if (body.querySelector('.selector')) Controller.toggle('tv_content');
    else Controller.toggle('tv_side');
  }

  // ---- playback ----
  // Zapping: the player asks for the next/previous channel of this list.
  function zap(from: number, dir: 1 | -1, done: (m: PlayerMedia | null, meta?: EpisodeMeta) => void): void {
    if (!channels.length) {
      done(null);
      return;
    }
    const idx = (from + dir + channels.length) % channels.length;
    const ch = channels[idx];
    tvPlay(ch.id).then(
      function (p) {
        currentIndex = idx;
        done(liveMedia(p.url, p.direct), { title: ch.name, subtitle: t('tv.live') + (p.quality ? ' · ' + p.quality : '') });
      },
      function () {
        tvFail(ch.id)['catch'](function () {});
        done(null);
      }
    );
  }
  let currentIndex = -1;

  // The player's guide overlay: this list as channels 1..N, plus EPG hooks.
  function playerTv(idx: number): PlayerTv {
    const list: PlayerTv['channels'] = [];
    for (let i = 0; i < channels.length; i++) {
      const c = channels[i];
      list.push({ id: c.id, name: c.name, logo: c.logo, quality: c.quality });
    }
    return {
      channels: list,
      index: idx,
      play: function (i, done) {
        const c = channels[i];
        if (!c) {
          done(null);
          return;
        }
        tvPlay(c.id).then(
          function (p) {
            currentIndex = i;
            done(liveMedia(p.url, p.direct), { title: c.name, subtitle: t('tv.live') + (p.quality ? ' · ' + p.quality : '') });
          },
          function () {
            tvFail(c.id)['catch'](function () {});
            done(null);
          }
        );
      },
      epg: function (id, done) {
        getTvEpg(id).then(
          function (r) {
            done(r.items || []);
          },
          function () {
            done([]);
          }
        );
      },
      nowNext: function (done) {
        getTvNow().then(
          function (r) {
            done(r.items || {});
          },
          function () {
            done({});
          }
        );
      },
    };
  }

  function play(ch: TvChannel, idx: number, btn: HTMLElement): void {
    btn.classList.add('is-loading');
    tvPlay(ch.id).then(
      function (p) {
        btn.classList.remove('is-loading');
        if (destroyed) return;
        currentIndex = idx;
        openPlayer({
          title: ch.name,
          subtitle: t('tv.live') + (p.quality ? ' · ' + p.quality : ''),
          poster: null,
          media: liveMedia(p.url, p.direct),
          live: true,
          tv: playerTv(idx),
          onNext: function (done) {
            zap(currentIndex, 1, done);
          },
          onPrev: function (done) {
            zap(currentIndex, -1, done);
          },
        });
      },
      function (e: ApiError) {
        btn.classList.remove('is-loading');
        if (destroyed) return;
        toast({ kind: 'error', title: t('tv.no_stream'), text: (e && e.message) || '' });
      }
    );
  }

  function toggleFavorite(ch: TvChannel, card: HTMLElement): void {
    const next = !ch.favorite;
    tvFavorite(ch.id, next).then(
      function () {
        ch.favorite = next;
        card.classList.toggle('is-fav', next);
        toast({ kind: 'success', icon: '★', text: t(next ? 'tv.fav_added' : 'tv.fav_removed') });
        if (params.section === 'fav' && !next) {
          const i = channels.indexOf(ch);
          if (i >= 0) channels.splice(i, 1);
          const parent = card.parentNode;
          if (parent) parent.removeChild(card);
          if (lastCard === card) lastCard = false;
          if (Controller.enabled().name === 'tv_content') {
            Controller.collectionSet(scroll.render());
            Controller.collectionFocus(false, scroll.render());
          }
          if (!channels.length) renderEmpty(t('tv.no_favorites'));
        }
      },
      function () {
        toast({ kind: 'error', text: t('error.load') });
      }
    );
  }

  // ---- content ----
  function renderEmpty(text: string): void {
    empty(body);
    lastCard = false;
    body.appendChild(buildState({ kind: 'empty', text: text }));
    scroll.reset();
    focusContentOrSide();
  }

  function renderChannels(list: TvChannel[]): void {
    channels = list;
    empty(body);
    lastCard = false;
    if (!list.length) {
      renderEmpty(params.section === 'fav' ? t('tv.no_favorites') : params.section === 'recent' ? t('tv.no_recent') : t('tv.empty'));
      return;
    }
    const grid = el('div', 'tv-grid');
    const cards: HTMLElement[] = [];
    // Logos for the rows around the focus (5 per row): a window of ~8 rows.
    function reveal(center: number): void {
      const from = Math.max(0, center - 10);
      const to = Math.min(list.length - 1, center + 30);
      for (let i = from; i <= to; i++) loadTvLogo(cards[i]);
    }
    for (let i = 0; i < list.length; i++) {
      (function (ch: TvChannel, idx: number) {
        const card = buildTvCard(ch, true);
        cards.push(card);
        on(card, 'hover:focus', function () {
          lastCard = card;
          reveal(idx);
        });
        on(card, 'hover:enter', function () {
          play(ch, idx, card);
        });
        on(card, 'hover:long', function () {
          toggleFavorite(ch, card);
        });
        grid.appendChild(card);
      })(list[i], i);
    }
    body.appendChild(grid);
    reveal(0);
    scroll.reset();
    focusContentOrSide();
  }

  function load(): void {
    const my = ++seq;
    empty(body);
    lastCard = false;
    body.appendChild(buildState({ kind: 'loading' }));
    const listReq =
      params.section === 'fav'
        ? getTvChannels({ fav: '1' })
        : params.section === 'recent'
          ? getTvChannels({ recent: '1' })
          : params.section === 'all'
            ? getTvChannels({})
            : params.section === 'country'
              ? getTvChannels({ country: params.id || '' })
              : getTvChannels({ category: params.id || '' });
    getTvMeta().then(
      function (m) {
        if (destroyed || my !== seq) return;
        renderSide(m.countries || [], m.categories || []);
        if (!paused && Controller.enabled().name !== 'tv_content') Controller.toggle('tv_side');
      },
      function () {
        if (destroyed || my !== seq) return;
        renderSide([], []);
      }
    );
    listReq.then(
      function (r) {
        if (destroyed || my !== seq) return;
        renderChannels(r.items || []);
      },
      function (e: ApiError) {
        if (destroyed || my !== seq) return;
        empty(body);
        lastCard = false;
        body.appendChild(buildState({ kind: 'error', text: e && e.code === 'tv_disabled' ? t('tv.unavailable') : t('error.load'), onRetry: load }));
        focusContentOrSide();
      }
    );
  }

  register();
  Controller.toggle('tv_side');
  load();

  return {
    destroy: function () {
      destroyed = true;
      head.destroy();
    },
    pause: function () {
      paused = true;
    },
    resume: function () {
      paused = false;
      register();
      menu.activate();
      // Back from the player: the favourite flags may have changed elsewhere;
      // the list itself is still valid.
      focusContentOrSide();
    },
  };
}
