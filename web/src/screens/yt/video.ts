// YouTube video page: big thumbnail, title, channel, views and date, a row of
// actions (Watch at the best quality, other qualities, Channel) and the
// related shelves below. Watch asks the server for a mux2 job and opens the
// ordinary player on the resulting HLS playlist with the SponsorBlock spans.

import Controller, { on } from '../../core/controller';
import { Scroll } from '../../core/scroll';
import { t } from '../../core/i18n';
import * as router from '../../core/router';
import { ScreenInstance } from '../../core/activity';
import { getYtVideo, ytPlay, mediaUrl, YtVideo, YtSegment, ApiError } from '../../core/api';
import { openPlayer, SkipSegment } from '../../core/player/index';
import { el, empty } from '../../ui/dom';
import { Background } from '../../ui/background';
import { buildMenu, Menu } from '../../ui/menu';
import { buildHead, Head } from '../../ui/head';
import { buildFooter } from '../../ui/shell';
import { buildState } from '../../ui/state';
import { toast } from '../../ui/toast';
import { openYtChannel } from '../nav';
import { buildYtShelf } from './cards';

export interface YtVideoParams {
  id: string;
}

const PREFERRED = ['1080p', '720p', '480p', '360p'];

function segmentsToSkips(segs: YtSegment[] | null | undefined): SkipSegment[] {
  const out: SkipSegment[] = [];
  for (let i = 0; segs && i < segs.length; i++) {
    const s = segs[i];
    const key = 'yt.cat.' + s.category;
    const label = t(key);
    out.push({ start: s.start, end: s.end, label: label === key ? s.category : label });
  }
  return out;
}

export function mountYtVideo(container: HTMLElement, params: YtVideoParams): ScreenInstance {
  container.className += ' yt-screen yt-video-screen';

  const background = new Background();
  container.appendChild(background.render());
  const menu: Menu = buildMenu('youtube', 'yt_actions');
  container.appendChild(menu.el);
  const head: Head = buildHead();
  container.appendChild(head.el);
  container.appendChild(buildFooter());

  const wrap = el('div', 'yt-panel yt-panel--video');
  container.appendChild(wrap);
  const scroll = new Scroll({ mask: true, over: true });
  wrap.appendChild(scroll.render());
  const body = scroll.body();
  body.classList.add('yt-video__body');

  let destroyed = false;
  let paused = false;
  let lastAction: HTMLElement | false = false;
  let lastCard: HTMLElement | false = false;
  let actions: HTMLElement | null = null;
  let mode = 'yt_actions';

  function toggleMode(name: string): void {
    mode = name;
    if (!paused) Controller.toggle(name);
  }

  const actionsController = {
    toggle: function () {
      if (!actions) return;
      Controller.collectionSet(actions);
      Controller.collectionFocus(lastAction || false, actions);
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
      if (body.querySelector('.yt-card')) toggleMode('yt_related');
    },
    back: function () {
      router.back();
    },
  };

  const relatedController = {
    toggle: function () {
      Controller.collectionSet(scroll.render());
      // Only the cards: the actions row has its own mode.
      const cards = scroll.render().querySelectorAll('.yt-card');
      const list: HTMLElement[] = [];
      for (let i = 0; i < cards.length; i++) list.push(cards[i] as HTMLElement);
      Controller.clear();
      Controller.collectionSet(scroll.render());
      Controller.collectionFocus(lastCard || (list[0] as HTMLElement) || false, scroll.render());
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
        toggleMode('yt_actions');
      });
    },
    down: function () {
      Controller.moveOr('down');
    },
    back: function () {
      toggleMode('yt_actions');
    },
  };

  function register(): void {
    Controller.add('yt_actions', actionsController);
    Controller.add('yt_related', relatedController);
  }

  function play(video: YtVideo, quality: string, btn: HTMLElement): void {
    btn.classList.add('is-loading');
    ytPlay(video.id, quality).then(
      function (res) {
        btn.classList.remove('is-loading');
        if (destroyed) return;
        openPlayer({
          title: video.title,
          subtitle: video.channel.name,
          poster: video.thumbnail,
          media: { type: 'hls', streams: [{ url: mediaUrl(res.playlist_url), quality: res.quality }], subtitles: [], voices: [] },
          durationHint: video.duration_sec || undefined,
          skipSegments: segmentsToSkips(res.segments),
        });
      },
      function (e: ApiError) {
        btn.classList.remove('is-loading');
        if (destroyed) return;
        toast({ kind: 'error', title: t('yt.play_failed'), text: (e && e.message) || '' });
      }
    );
  }

  function render(video: YtVideo): void {
    empty(body);
    const hero = el('div', 'yt-hero');
    const thumb = el('div', 'yt-hero__thumb');
    if (video.thumbnail) {
      const img = document.createElement('img');
      img.src = video.thumbnail.replace('hqdefault', 'maxresdefault');
      img.alt = '';
      img.onerror = function () {
        img.onerror = null;
        img.src = video.thumbnail || '';
      };
      thumb.appendChild(img);
    }
    hero.appendChild(thumb);
    const info = el('div', 'yt-hero__info');
    info.appendChild(el('div', 'yt-hero__title', video.title));
    const meta: string[] = [];
    if (video.channel && video.channel.name) meta.push(video.channel.name);
    if (video.views_text) meta.push(video.views_text);
    if (video.published_text) meta.push(video.published_text);
    if (meta.length) info.appendChild(el('div', 'yt-hero__meta', meta.join(' • ')));
    if (video.description) info.appendChild(el('div', 'yt-hero__desc', video.description));

    actions = el('div', 'yt-actions');
    if (video.playable) {
      let best = '';
      for (let i = 0; i < PREFERRED.length && !best; i++) {
        if (video.qualities.indexOf(PREFERRED[i]) >= 0) best = PREFERRED[i];
      }
      if (!best && video.qualities.length) best = video.qualities[video.qualities.length - 1];
      const watch = el('div', 'button button--primary selector', t('yt.watch') + (best ? ' · ' + best : ''));
      on(watch, 'hover:focus', function () {
        lastAction = watch;
      });
      on(watch, 'hover:enter', function () {
        play(video, best || '720p', watch);
      });
      actions.appendChild(watch);
      for (let i = 0; i < PREFERRED.length; i++) {
        const q = PREFERRED[i];
        if (q === best || video.qualities.indexOf(q) < 0) continue;
        (function (qq: string) {
          const b = el('div', 'button selector', qq);
          on(b, 'hover:focus', function () {
            lastAction = b;
          });
          on(b, 'hover:enter', function () {
            play(video, qq, b);
          });
          actions.appendChild(b);
        })(q);
      }
    } else {
      info.appendChild(el('div', 'yt-hero__unplayable', video.reason === 'live' ? t('yt.live_unsupported') : video.reason || t('yt.unplayable')));
    }
    if (video.channel && video.channel.id) {
      const ch = el('div', 'button selector', t('yt.channel'));
      on(ch, 'hover:focus', function () {
        lastAction = ch;
      });
      on(ch, 'hover:enter', function () {
        openYtChannel(video.channel.id as string);
      });
      actions.appendChild(ch);
    }
    info.appendChild(actions);
    hero.appendChild(info);
    body.appendChild(hero);

    const related = video.related || [];
    for (let i = 0; i < related.length; i++) {
      if (!related[i].items.length) continue;
      const shelf = related[i];
      if (!shelf.title) shelf.title = i === 0 ? t('yt.related') : '';
      body.appendChild(
        buildYtShelf(shelf, {
          onFocus: function (card) {
            lastCard = card;
          },
        })
      );
    }
    scroll.reset();
    toggleMode(actions.querySelector('.selector') ? 'yt_actions' : body.querySelector('.yt-card') ? 'yt_related' : 'yt_actions');
    if (!actions.querySelector('.selector') && !body.querySelector('.yt-card')) Controller.toggle('menu');
  }

  function showError(text: string): void {
    empty(body);
    const box = buildState({ kind: 'error', text: text, onRetry: load });
    body.appendChild(box);
    actions = box;
    toggleMode('yt_actions');
  }

  function load(): void {
    empty(body);
    body.appendChild(buildState({ kind: 'loading' }));
    getYtVideo(params.id).then(
      function (v) {
        if (destroyed) return;
        render(v);
      },
      function (e: ApiError) {
        if (destroyed) return;
        showError(e && e.code === 'not_linked' ? t('yt.not_linked_title') : e && e.code === 'youtube_disabled' ? t('yt.unavailable') : t('error.load'));
      }
    );
  }

  register();
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
      Controller.toggle(mode);
    },
  };
}
