// YouTube video page: big thumbnail, title, channel, views and date, a row of
// actions (Resume / Watch at the best quality, other qualities, Channel) and
// the related shelves below. Watch hands the player /api/v1/yt/play/<id>,
// which the player treats like a torrent /remux JSON endpoint: it appends
// start=N for a resume, reads {playlist_url} and polls the HLS playlist.
// Progress goes to the device-local store and, every ~20 s, to the account's
// YouTube history through /api/v1/yt/watch.

import Controller, { on } from '../../core/controller';
import { Scroll } from '../../core/scroll';
import { t } from '../../core/i18n';
import * as router from '../../core/router';
import { ScreenInstance } from '../../core/activity';
import { getYtVideo, ytSegments, ytWatch, YtVideo, YtSegment, ApiError } from '../../core/api';
import { openPlayer, SkipSegment } from '../../core/player/index';
import { isResumable } from '../../core/progress';
import { getYtResume, setYtResume } from '../../core/ytresume';
import { el, empty } from '../../ui/dom';
import { Background } from '../../ui/background';
import { buildMenu, Menu } from '../../ui/menu';
import { buildHead, Head } from '../../ui/head';
import { buildFooter } from '../../ui/shell';
import { buildState } from '../../ui/state';
import { openYtChannel } from '../nav';
import { buildYtShelf } from './cards';

export interface YtVideoParams {
  id: string;
}

const PREFERRED = ['1080p', '720p', '480p', '360p'];
const WATCH_EVERY_MS = 20000;

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

function mmss(sec: number): string {
  sec = Math.max(0, Math.floor(sec));
  const h = Math.floor(sec / 3600);
  const m = Math.floor((sec % 3600) / 60);
  const s = sec % 60;
  const two = function (n: number): string {
    return (n < 10 ? '0' : '') + n;
  };
  return h > 0 ? h + ':' + two(m) + ':' + two(s) : m + ':' + two(s);
}

// The position to offer: this TV's exact spot if it has one, else what the
// account's history says (whole percents of the duration).
function resumePoint(video: YtVideo): number {
  const local = getYtResume(video.id);
  if (local && isResumable(local.position_sec, local.duration_sec)) return local.position_sec;
  if (video.resume_sec > 0 && video.duration_sec > 0 && isResumable(video.resume_sec, video.duration_sec)) return video.resume_sec;
  return 0;
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

  // The hero (thumb + actions) sits above the scroll, so focusing an action
  // never scrolls it away; only the related shelves scroll.
  const wrap = el('div', 'yt-panel yt-panel--video');
  container.appendChild(wrap);
  const heroSlot = el('div', 'yt-hero-slot');
  wrap.appendChild(heroSlot);
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
  let segments: YtSegment[] = [];

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
      const first = scroll.render().querySelector('.yt-card') as HTMLElement | null;
      Controller.collectionFocus(lastCard || first || false, scroll.render());
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

  function play(video: YtVideo, quality: string, resumeFrom: number): void {
    let lastSent = 0;
    let lastPos = -1;
    openPlayer({
      title: video.title,
      subtitle: video.channel.name,
      poster: video.thumbnail,
      media: {
        type: 'hls',
        streams: [{ url: '/api/v1/yt/play/' + encodeURIComponent(video.id) + '?quality=' + encodeURIComponent(quality), quality: quality }],
        subtitles: [],
        voices: [],
      },
      durationHint: video.duration_sec || undefined,
      resume: resumeFrom > 0 ? { position_sec: resumeFrom, duration_sec: video.duration_sec } : null,
      skipSegments: segmentsToSkips(segments),
      onProgress: function (pos, dur) {
        setYtResume(video.id, pos, dur);
        const now = Date.now();
        // Every ~20 s, or on a jump (seek): the account's history follows.
        if (now - lastSent < WATCH_EVERY_MS && lastPos >= 0 && Math.abs(pos - lastPos) < 60) return;
        lastSent = now;
        lastPos = pos;
        ytWatch(video.id, pos, dur)['catch'](function () {
          /* history is best effort */
        });
      },
    });
  }

  function actionBtn(label: string, cls: string, run: () => void): HTMLElement {
    const b = el('div', 'button selector' + (cls ? ' ' + cls : ''), label);
    on(b, 'hover:focus', function () {
      lastAction = b;
    });
    on(b, 'hover:enter', run);
    return b;
  }

  function render(video: YtVideo): void {
    empty(body);
    empty(heroSlot);
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
      best = best || '720p';
      const resumeAt = resumePoint(video);
      if (resumeAt > 0) {
        actions.appendChild(
          actionBtn(t('yt.resume', { time: mmss(resumeAt) }), 'button--primary', function () {
            play(video, best, resumeAt);
          })
        );
        actions.appendChild(
          actionBtn(t('yt.from_start') + ' · ' + best, '', function () {
            play(video, best, 0);
          })
        );
      } else {
        actions.appendChild(
          actionBtn(t('yt.watch') + ' · ' + best, 'button--primary', function () {
            play(video, best, 0);
          })
        );
      }
      for (let i = 0; i < PREFERRED.length; i++) {
        const q = PREFERRED[i];
        if (q === best || video.qualities.indexOf(q) < 0) continue;
        (function (qq: string) {
          actions.appendChild(
            actionBtn(qq, '', function () {
              play(video, qq, resumeAt);
            })
          );
        })(q);
      }
    } else {
      info.appendChild(el('div', 'yt-hero__unplayable', video.reason === 'live' ? t('yt.live_unsupported') : video.reason || t('yt.unplayable')));
    }
    if (video.channel && video.channel.id) {
      actions.appendChild(
        actionBtn(t('yt.channel'), '', function () {
          openYtChannel(video.channel.id as string);
        })
      );
    }
    info.appendChild(actions);
    hero.appendChild(info);
    heroSlot.appendChild(hero);

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
    if (actions.querySelector('.selector')) toggleMode('yt_actions');
    else if (body.querySelector('.yt-card')) toggleMode('yt_related');
    else Controller.toggle('menu');
  }

  function showError(text: string): void {
    empty(body);
    empty(heroSlot);
    const box = buildState({ kind: 'error', text: text, onRetry: load });
    body.appendChild(box);
    actions = box;
    toggleMode('yt_actions');
  }

  function load(): void {
    empty(body);
    empty(heroSlot);
    body.appendChild(buildState({ kind: 'loading' }));
    // SponsorBlock spans ride along; a failure there must not block the page.
    ytSegments(params.id).then(
      function (r) {
        segments = (r && r.segments) || [];
      },
      function () {
        segments = [];
      }
    );
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
      // Back from the player: the resume button must reflect the new position.
      if (actions && actions.parentNode && !actions.classList.contains('state')) {
        getYtVideo(params.id).then(
          function (v) {
            if (!destroyed && !paused) render(v);
          },
          function () {
            Controller.toggle(mode);
          }
        );
      } else {
        Controller.toggle(mode);
      }
    },
  };
}
