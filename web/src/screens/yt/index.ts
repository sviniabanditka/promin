// YouTube section (docs/youtube.md): the rail's "YouTube" item opens this
// screen. Left of the content a narrow sidebar with the section's pages
// (home, subscriptions, history, playlists, liked, watch later, search,
// account); the content is the page's shelves as 16:9 grids in one vertical
// scroll. Channel and playlist pages reuse the same screen, pushed.
//
// Controller modes: 'menu' (the rail), 'yt_side' (sidebar), 'yt_content'.
// Nothing here touches the film/series screens; if the sidecar is down the
// content shows an error state and the rest of Promin is unaffected.

import Controller, { on } from '../../core/controller';
import { Scroll } from '../../core/scroll';
import { t } from '../../core/i18n';
import * as router from '../../core/router';
import { ScreenInstance } from '../../core/activity';
import { ytBrowse, getYtAccount, ytLogin, ytUnlink, YtFeed, YtAccount, ApiError } from '../../core/api';
import { el, empty } from '../../ui/dom';
import { Background } from '../../ui/background';
import { buildMenu, Menu } from '../../ui/menu';
import { buildHead, Head } from '../../ui/head';
import { buildFooter } from '../../ui/shell';
import { buildState } from '../../ui/state';
import { toast } from '../../ui/toast';
import { openYt, openYtSearch } from '../nav';
import { buildYtShelf } from './cards';

export type YtPage = 'home' | 'subscriptions' | 'history' | 'playlists' | 'liked' | 'watch_later' | 'account' | 'channel' | 'playlist';

export interface YtParams {
  page: YtPage;
  browseId?: string; // channel (UC…) or playlist (PL…) id for page = channel | playlist
}

const SIDE: { page: YtPage | 'search'; key: string }[] = [
  { page: 'home', key: 'yt.home' },
  { page: 'subscriptions', key: 'yt.subscriptions' },
  { page: 'history', key: 'yt.history' },
  { page: 'playlists', key: 'yt.playlists' },
  { page: 'liked', key: 'yt.liked' },
  { page: 'watch_later', key: 'yt.watch_later' },
  { page: 'search', key: 'yt.search' },
  { page: 'account', key: 'yt.account' },
];

export function mountYouTube(container: HTMLElement, params: YtParams): ScreenInstance {
  container.className += ' yt-screen';

  const background = new Background();
  container.appendChild(background.render());
  const menu: Menu = buildMenu('youtube', 'yt_side');
  container.appendChild(menu.el);
  const head: Head = buildHead();
  container.appendChild(head.el);
  container.appendChild(buildFooter());

  const panel = el('div', 'yt-panel');
  container.appendChild(panel);

  // ---- sidebar ----
  const side = el('div', 'yt-side');
  panel.appendChild(side);
  let lastSide: HTMLElement | false = false;
  for (let i = 0; i < SIDE.length; i++) {
    (function (def: { page: YtPage | 'search'; key: string }) {
      const item = el('div', 'yt-side__item selector' + (def.page === params.page ? ' is-current' : ''), t(def.key));
      on(item, 'hover:focus', function () {
        lastSide = item;
      });
      on(item, 'hover:enter', function () {
        if (def.page === 'search') openYtSearch();
        else if (def.page !== params.page) openYt(def.page as YtPage);
        else Controller.toggle('yt_content');
      });
      side.appendChild(item);
      if (def.page === params.page) lastSide = item;
    })(SIDE[i]);
  }

  // ---- content ----
  const content = el('div', 'yt-content');
  panel.appendChild(content);
  const scroll = new Scroll({ mask: true, over: true });
  content.appendChild(scroll.render());
  const body = scroll.body();
  body.classList.add('yt-content__body');

  let destroyed = false;
  let paused = false;
  let lastCard: HTMLElement | false = false;
  let pollTimer = 0;

  const sideController = {
    toggle: function () {
      Controller.collectionSet(side);
      Controller.collectionFocus(lastSide || false, side);
    },
    left: function () {
      Controller.toggle('menu');
    },
    right: function () {
      if (body.querySelector('.selector')) Controller.toggle('yt_content');
    },
    up: function () {
      Controller.moveOr('up');
    },
    down: function () {
      Controller.moveOr('down');
    },
    back: function () {
      if (params.page === 'channel' || params.page === 'playlist') router.back();
      else Controller.toggle('menu');
    },
  };

  const contentController = {
    toggle: function () {
      Controller.collectionSet(scroll.render());
      Controller.collectionFocus(lastCard || false, scroll.render());
    },
    left: function () {
      Controller.moveOr('left', function () {
        Controller.toggle('yt_side');
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
      if (params.page === 'channel' || params.page === 'playlist') router.back();
      else Controller.toggle('yt_side');
    },
  };

  function register(): void {
    Controller.add('yt_side', sideController);
    Controller.add('yt_content', contentController);
  }

  function focusContentOrSide(): void {
    if (paused) return;
    if (body.querySelector('.selector')) Controller.toggle('yt_content');
    else Controller.toggle('yt_side');
  }

  function refreshCollection(): void {
    if (paused || Controller.enabled().name !== 'yt_content') return;
    Controller.collectionSet(scroll.render());
    Controller.collectionFocus(lastCard || false, scroll.render());
  }

  // ---- states ----
  function showLoading(): void {
    empty(body);
    lastCard = false;
    body.appendChild(buildState({ kind: 'loading' }));
  }

  function showError(text: string, retry: () => void): void {
    empty(body);
    lastCard = false;
    body.appendChild(buildState({ kind: 'error', text: text, onRetry: retry }));
    scroll.reset();
    focusContentOrSide();
  }

  function renderFeed(feed: YtFeed): void {
    empty(body);
    lastCard = false;
    const shelves = feed && feed.shelves ? feed.shelves : [];
    if (!shelves.length) {
      body.appendChild(buildState({ kind: 'empty', text: t('yt.empty') }));
    }
    for (let i = 0; i < shelves.length; i++) {
      body.appendChild(
        buildYtShelf(shelves[i], {
          loadMore: function (cont) {
            return ytBrowse(params.browseId || params.page, cont);
          },
          onFocus: function (card) {
            lastCard = card;
          },
          onAppended: function (cards) {
            if (Controller.enabled().name === 'yt_content') {
              for (let c = 0; c < cards.length; c++) Controller.collectionAppend(cards[c]);
              if (cards.length) Controller.focus(cards[0]);
            }
          },
        })
      );
    }
    scroll.reset();
    focusContentOrSide();
  }

  // ---- account panel (sign-in / status) ----
  function renderAccount(acc: YtAccount | null, notLinkedIntro: boolean): void {
    empty(body);
    lastCard = false;
    const box = el('div', 'yt-account');
    if (acc && acc.linked) {
      box.appendChild(el('div', 'yt-account__title', t('yt.linked')));
      box.appendChild(el('div', 'yt-account__text', t('yt.linked_text')));
      const out = el('div', 'button selector', t('yt.signout'));
      on(out, 'hover:enter', function () {
        ytUnlink().then(
          function () {
            toast({ kind: 'info', text: t('yt.signed_out') });
            openYt('account');
          },
          function () {
            toast({ kind: 'error', text: t('error.load') });
          }
        );
      });
      box.appendChild(out);
    } else if (acc && acc.pending && acc.user_code) {
      box.appendChild(el('div', 'yt-account__title', t('yt.signin_title')));
      box.appendChild(el('div', 'yt-account__text', t('yt.signin_hint', { url: acc.verification_url || 'google.com/device' })));
      box.appendChild(el('div', 'yt-account__code', acc.user_code));
      box.appendChild(el('div', 'yt-account__wait', t('yt.signin_wait')));
      // Poll until the phone approves; then land on the section's home.
      if (pollTimer) window.clearInterval(pollTimer);
      pollTimer = window.setInterval(function () {
        getYtAccount().then(
          function (a) {
            if (destroyed) return;
            if (a.linked) {
              window.clearInterval(pollTimer);
              pollTimer = 0;
              toast({ kind: 'success', icon: '✓', text: t('yt.linked') });
              openYt('home');
            } else if (!a.pending) {
              window.clearInterval(pollTimer);
              pollTimer = 0;
              renderAccount(a, false);
            }
          },
          function () {}
        );
      }, 3000);
    } else {
      box.appendChild(el('div', 'yt-account__title', t(notLinkedIntro ? 'yt.not_linked_title' : 'yt.account')));
      box.appendChild(el('div', 'yt-account__text', t('yt.not_linked_text')));
      if (acc && acc.error) box.appendChild(el('div', 'yt-account__error', acc.error));
      const btn = el('div', 'button selector', t('yt.signin'));
      on(btn, 'hover:enter', function () {
        btn.classList.add('is-loading');
        ytLogin().then(
          function (a) {
            if (destroyed) return;
            renderAccount(a, false);
          },
          function (e: ApiError) {
            btn.classList.remove('is-loading');
            toast({ kind: 'error', title: t('yt.unavailable'), text: (e && e.message) || '' });
          }
        );
      });
      box.appendChild(btn);
    }
    body.appendChild(box);
    scroll.reset();
    focusContentOrSide();
  }

  // ---- load ----
  function load(): void {
    showLoading();
    if (params.page === 'account') {
      getYtAccount().then(
        function (a) {
          if (destroyed) return;
          renderAccount(a, false);
        },
        function (e: ApiError) {
          if (destroyed) return;
          showError(e && e.code === 'youtube_disabled' ? t('yt.unavailable') : t('error.load'), load);
        }
      );
      return;
    }
    ytBrowse(params.browseId || params.page).then(
      function (feed) {
        if (destroyed) return;
        renderFeed(feed);
      },
      function (e: ApiError) {
        if (destroyed) return;
        if (e && e.code === 'not_linked') {
          renderAccount({ linked: false, pending: false }, true);
        } else if (e && e.code === 'youtube_disabled') {
          showError(t('yt.unavailable'), load);
        } else {
          showError(t('error.load'), load);
        }
      }
    );
  }

  register();
  Controller.toggle('yt_side');
  load();

  return {
    destroy: function () {
      destroyed = true;
      head.destroy();
      if (pollTimer) window.clearInterval(pollTimer);
    },
    pause: function () {
      paused = true;
    },
    resume: function () {
      paused = false;
      register();
      menu.activate();
      refreshCollection();
      focusContentOrSide();
    },
  };
}
