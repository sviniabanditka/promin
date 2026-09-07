// Title screen — the Lampa full-start card (backdrop, poster, meta, overview,
// action buttons) sized to 100vh, which stays mounted while an in-place
// watch/torrents modal (openWatchModal) opens over it. The two action buttons
// are a tab selector for that modal: "Дивитись" → online tab, "Торренти" →
// torrents tab. The modal is built on the action-sheet template (same as the
// trailers modal) and carries both the online-source resolution and the torrent
// add/stream flows that used to live in screens/sources.
//
// All data/integration logic is unchanged: getTitle, lazy getTitleSeason per
// season, sync.toggleBookmark, online resolve→openPlayer, torrent add→openPlayer,
// playlists, resume timecodes.

import Controller, { on, trigger } from '../core/controller';
import { Scroll } from '../core/scroll';
import { t } from '../core/i18n';
import * as router from '../core/router';
import { ScreenInstance } from '../core/activity';
import {
  getTitleSeason,
  getPlaylists,
  addPlaylistItem,
  getOnlineSources,
  resolveOnline,
  getTorrents,
  addTorrent,
  getTorrentAudio,
  Card,
  Season,
  Episode,
  Playlist,
  OnlineSource,
  ResolveResponse,
  ResolveParams,
  Voice,
  Trailer,
  Torrent,
  TorrentFile,
  getPlaylistItems,
  createPlaylist,
  getTitleCached,
} from '../core/api';
import { el, empty, fmtBytes } from '../ui/dom';
import { buildState } from '../ui/state';
import { Background } from '../ui/background';
import { buildMenu, Menu } from '../ui/menu';
import { buildHead, Head } from '../ui/head';
import { buildFooter } from '../ui/shell';
import { iconEl, ICON_PLAY, ICON_TORRENT, ICON_BOOKMARK, ICON_PLAYLIST, ICON_CHECK, ICON_TRAILER, ICON_FOLDER } from '../ui/icons';
import { openLogin, openTitle } from './nav';
import { toast } from '../ui/toast';
import { isLogged } from '../core/auth';
import * as sync from '../core/sync';
import { fmtClock, runtimeText, qBadge, seedClass, parseMeta } from './titleMeta';
import { openNameModal } from './playlists';
import { torrentMedia, parseEpisode } from '../core/torrentPlay';
import { openPlayer, PlayerMedia, EpisodeMeta, PlayerEpisode } from '../core/player';
import { isFinished, isResumable } from '../core/progress';

export interface TitleParams {
  type: 'movie' | 'tv';
  id: number;
  // Start playback from the saved position as soon as the title renders.
  resume?: boolean;
  // Start this episode as soon as the title renders (Telegram "▶ on TV").
  season?: number;
  episode?: number;
  // Start the movie from the top as soon as the title renders (watch queue).
  autoplay?: boolean;
}

type NextDone = (m: PlayerMedia | null, meta?: EpisodeMeta) => void;

// ---- watch queue fallback (docs/miniapp.md) --------------------------------
// When what plays has no next episode (movie, last episode of the show, last
// file of a pack) the player's "next" falls through to the queue head: pop it
// and open that title with the deep-link autoplay. false = queue empty, the
// caller keeps its own dead-end handling.
function playQueueHead(): boolean {
  const head = sync.queueHead();
  if (!head) return false;
  sync.popQueue();
  const type: 'movie' | 'tv' = head.media_type === 'tv' ? 'tv' : 'movie';
  getTitleCached(type, head.tmdb_id).then(
    function (c) {
      toast({ kind: 'info', icon: '⏭', title: t('queue.opened'), text: c.title });
    },
    function () {
      /* label only */
    }
  );
  router.back(); // leave the player; its title stays beneath the new one
  openTitle(type, head.tmdb_id, false, head.season, head.episode, type === 'movie');
  return true;
}
// Player onNext for something that has no next episode of its own.
function queueNext(done: NextDone): void {
  if (!playQueueHead()) done(null);
}
// Append a synthetic trailing episode naming the queue head, so the player's
// "Next: …" caption and the end-of-episode countdown say what really follows
// (the player only knows episodes). Picking it jumps past the season's last
// episode → crossSeason → playQueueHead.
function withQueueTail(eps: PlayerEpisode[], then: () => void): void {
  const head = sync.queueHead();
  if (!head || !eps.length) {
    then();
    return;
  }
  let last = 0;
  for (let i = 0; i < eps.length; i++) if (eps[i].episode > last) last = eps[i].episode;
  function push(name: string): void {
    eps.push({ episode: last + 1, name: t('queue.next_from', { n: name }) });
    then();
  }
  getTitleCached(head.media_type, head.tmdb_id).then(
    function (c) {
      push(c.title);
    },
    function () {
      push('');
    }
  );
}

export function mountTitle(container: HTMLElement, params: TitleParams): ScreenInstance {
  container.className += ' title-screen';

  const background = new Background();
  container.appendChild(background.render());

  // Rail + clock on the title screen too (every screen but the player carries
  // them). No menu item is active here — 'none' keeps them all outline.
  const menu: Menu = buildMenu('none', 'title');
  container.appendChild(menu.el);
  const head: Head = buildHead();
  container.appendChild(head.el);
  container.appendChild(buildFooter());

  // PAGE 1 — the full-start card (the watch/torrents modal opens over it,
  // in-place, while this stays mounted).
  const page1 = el('div', 'title-page title-page--info');
  const page1Inner = el('div', 'title-page__inner');
  page1.appendChild(page1Inner);
  container.appendChild(page1);

  // Loading skeleton (page 1) — info-only, matching the loaded poster-less
  // bottom-anchored hero (no poster column; it's display:none when loaded).
  const skeleton = el('div', 'title-skeleton');
  const skelInfo = el('div', 'title-skeleton__info');
  for (let i = 0; i < 5; i++) skelInfo.appendChild(el('div', 'skeleton-line'));
  skeleton.appendChild(skelInfo);
  page1Inner.appendChild(skeleton);

  // Track which mode to restore on resume.
  let lastMode = 'title';
  let unsubBookmarks: (() => void) | null = null;
  let destroyed = false;

  // Full-screen spinner shown while a source resolves to concrete streams
  // (same pattern as screens/sources). Hidden by default.
  const overlay = el('div', 'sources-overlay hide');
  overlay.appendChild(el('div', 'player__spinner-ring'));
  const overlayText = el('div', 'sources-overlay__text', t('sources.resolving'));
  overlay.appendChild(overlayText);
  container.appendChild(overlay);
  function showOverlay(text: string): void {
    overlayText.textContent = text;
    overlay.classList.remove('hide');
  }
  function hideOverlay(): void {
    overlay.classList.add('hide');
  }

  // Run by the screen's resume() before re-toggling the mode (set by the
  // sources modal while it is open).
  let resumeHook: (() => void) | null = null;
  // The "Continue" action button (null when there is nothing to continue).
  let continueBtn: HTMLElement | null = null;
  // Set by render(): opens the watch modal on a given episode (deep links).
  let openEpisode: ((season: number | null, episode: number | null) => void) | null = null;

  // Pre-resolve: while the user reads the page, resolve the remembered source
  // for the episode "Продовжити"/"Дивитись" would start with. The watch modal's
  // resolveMemo picks the promise up when its params match, so the first
  // "play" costs no round-trip. Params are built in the SAME key order as
  // resolveParamsFor — the memo key is JSON.stringify.
  let prefetch: { key: string; p: Promise<ResolveResponse> } | null = null;
  function prefetchResolve(card: Card, rp: ResumePoint | null): void {
    let last: { balanser?: string; voice?: string | null } | null = null;
    try {
      const raw = window.localStorage.getItem('promin:lastsrc:' + card.type + ':' + card.tmdb_id);
      last = raw ? (JSON.parse(raw) as { balanser?: string; voice?: string | null }) : null;
    } catch (e) {
      last = null;
    }
    if (!last || !last.balanser || last.balanser === 'torrent') return;
    const isSeries = card.type === 'tv';
    const firstSeason = card.seasons && card.seasons.length ? card.seasons[0].season : 1;
    const params: ResolveParams = {
      balanser: last.balanser,
      tmdb_id: card.tmdb_id,
      type: card.type,
      title: card.title,
      original_title: card.original_title,
      year: card.year != null ? card.year : undefined,
      imdb_id: card.external_ids ? card.external_ids.imdb_id : undefined,
      season: isSeries ? (rp && rp.season != null ? rp.season : firstSeason) : undefined,
      episode: isSeries ? (rp && rp.episode != null ? rp.episode : 1) : undefined,
      voice: last.voice != null ? last.voice : undefined,
    };
    const key = JSON.stringify(params);
    if (prefetch && prefetch.key === key) return;
    prefetch = { key: key, p: resolveOnline(params) };
    prefetch.p.then(
      function () {},
      function () {
        prefetch = null; // a failed prefetch must not shadow a real attempt
      }
    );
  }

  // Where the user left off, for the "Continue" button. Series: the most
  // recently touched episode; if that one is finished, the next episode.
  interface ResumePoint {
    season: number | null;
    episode: number | null;
    position: number; // 0 = start the episode from the beginning
  }
  function resumePoint(card: Card): ResumePoint | null {
    if (!isLogged()) return null;
    if (card.type !== 'tv') {
      // Local cache first; the shelf card's own timecode when the cache has
      // evicted it (MAX_TIMECODES) — the server still remembers.
      const tc = sync.getTimecodeCached(card.tmdb_id, card.type, null, null) || card.timecode;
      if (tc && isResumable(tc.position_sec, tc.duration_sec)) {
        return { season: null, episode: null, position: tc.position_sec };
      }
      return null;
    }
    const all = sync.getTimecodesFor(card.tmdb_id, card.type);
    interface Spot {
      season: number | null;
      episode: number | null;
      position_sec: number;
      duration_sec: number;
      updated_at?: number;
    }
    let best: Spot | null = null;
    for (let i = 0; i < all.length; i++) {
      if (all[i].episode == null) continue;
      if (!best || (all[i].updated_at || 0) > (best.updated_at || 0)) best = all[i];
    }
    if (!best && card.timecode && card.timecode.episode != null) {
      const ct = card.timecode;
      best = { season: ct.season != null ? ct.season : null, episode: ct.episode as number, position_sec: ct.position_sec, duration_sec: ct.duration_sec };
    }
    if (!best || best.episode == null) return null;
    if (!isFinished(best.position_sec, best.duration_sec)) {
      return { season: best.season, episode: best.episode, position: best.position_sec };
    }
    // Finished → the next episode; past the season's last one → next season's
    // first (the old +1 asked the source for an episode that doesn't exist).
    const seasons = card.seasons || [];
    const sn = best.season != null ? best.season : 1;
    let count = 0;
    let idx = -1;
    for (let i = 0; i < seasons.length; i++) {
      if (seasons[i].season === sn) {
        idx = i;
        count = seasons[i].episode_count || (seasons[i].episodes ? (seasons[i].episodes as Episode[]).length : 0);
      }
    }
    if (count > 0 && best.episode >= count) {
      for (let i = idx + 1; i < seasons.length; i++) {
        if (seasons[i].season >= 1) return { season: seasons[i].season, episode: 1, position: 0 };
      }
      return null; // finale of the last season — nothing to continue
    }
    return { season: best.season, episode: best.episode + 1, position: 0 };
  }

  function toggleMode(name: string): void {
    lastMode = name;
    Controller.toggle(name);
  }

  // Modal (mode 'title_playlist') listing the user's playlists; OK adds this
  // title to the chosen one. Returns focus to whatever mode was active.
  function openPlaylistPicker(card: Card): void {
    const returnMode = lastMode;
    const overlay = el('div', 'settings-modal');
    const box = el('div', 'settings-modal__box');
    box.appendChild(el('div', 'settings-modal__title', t('title.playlist_choose')));
    const list = el('div', 'settings-modal__list');
    box.appendChild(list);
    overlay.appendChild(box);
    container.appendChild(overlay);

    function close(): void {
      if (overlay.parentNode) overlay.parentNode.removeChild(overlay);
      Controller.toggle(returnMode);
      Controller.remove('title_playlist');
    }

    Controller.add('title_playlist', {
      toggle: function () {
        Controller.collectionSet(overlay);
        Controller.collectionFocus(false, overlay);
      },
      up: function () {
        Controller.moveOr('up');
      },
      down: function () {
        Controller.moveOr('down');
      },
      back: function () {
        close();
      },
    });

    function addTo(pl: Playlist): void {
      addPlaylistItem(pl.id, card.tmdb_id, card.type).then(
        function () {
          toast({ kind: 'success', icon: '✓', title: t('title.playlist_added'), text: pl.name || '' });
        },
        function () {
          toast({ kind: 'error', title: t('title.playlist_add_failed'), text: t('toast.try_again') });
        }
      );
    }

    // has: playlists that already contain this title (✓, no duplicate add).
    function renderPlaylists(pls: Playlist[], has: { [id: number]: boolean }): void {
      empty(list);
      for (let i = 0; i < pls.length; i++) {
        (function (pl: Playlist) {
          const inPl = !!has[pl.id];
          const item = el('div', 'settings-opt selector' + (inPl ? ' is-active' : ''), pl.name || '');
          on(item, 'hover:enter', function () {
            if (inPl) toast({ kind: 'info', title: t('title.playlist_exists'), text: pl.name || '' });
            else addTo(pl);
            close();
          });
          list.appendChild(item);
        })(pls[i]);
      }
      // "Create playlist" right here: the first playlist used to cost a trip
      // Library → Playlists → Manage → Create → Back → Back → title → Playlist.
      const create = el('div', 'settings-opt selector', '+ ' + t('playlists.create'));
      on(create, 'hover:enter', function () {
        if (overlay.parentNode) overlay.parentNode.removeChild(overlay);
        openNameModal(container, t('playlists.create'), '', returnMode, function (name) {
          createPlaylist(name).then(
            function (pl) {
              if (pl && pl.id) addTo(pl);
              Controller.toggle(returnMode);
            },
            function () {
              toast({ kind: 'error', title: t('playlists.create_failed'), text: t('toast.try_again') });
              Controller.toggle(returnMode);
            }
          );
        });
      });
      list.appendChild(create);
      Controller.toggle('title_playlist');
    }

    list.appendChild(el('div', 'settings-modal__empty', t('common.loading')));
    Controller.toggle('title_playlist');

    getPlaylists().then(
      function (res) {
        const pls = res && res.playlists ? res.playlists : [];
        // One small request per playlist (a household has a handful) to mark
        // where the title already is; a failed lookup just shows no ✓.
        const has: { [id: number]: boolean } = {};
        const jobs: Promise<void>[] = [];
        for (let i = 0; i < pls.length; i++) {
          (function (pl: Playlist) {
            jobs.push(
              getPlaylistItems(pl.id).then(
                function (r) {
                  const items = (r && r.items) || [];
                  for (let j = 0; j < items.length; j++) {
                    if (items[j].tmdb_id === card.tmdb_id && items[j].media_type === card.type) has[pl.id] = true;
                  }
                },
                function () {
                  /* no ✓ for this one */
                }
              )
            );
          })(pls[i]);
        }
        Promise.all(jobs).then(function () {
          if (destroyed) return;
          renderPlaylists(pls, has);
        });
      },
      function () {
        empty(list);
        list.appendChild(el('div', 'settings-modal__empty', t('error.load')));
        Controller.toggle('title_playlist');
      }
    );
  }

  // Full-screen trailer overlay. MVP: a YouTube iframe embed on top of a dark
  // backdrop. Some TV webviews block youtube.com/embed (CSP / device policy),
  // so a load timeout + iframe onerror fall back to a "trailer unavailable"
  // message plus an "open on YouTube" link (Android TV hands youtube.com off to
  // the YouTube app). Back / the close button remove the overlay and restore
  // focus to whatever mode was active (usually the action buttons).
  // Trailers modal (redesign B): a language row + a list of the title's YouTube
  // trailers; picking one opens the embedded player (openTrailer). Reuses the
  // unified settings-modal look. Languages come from each trailer's `lang`
  // (backend fetched uk/ru/en in one call).
  function langLabel(code: string): string {
    if (code === 'uk') return 'Українська';
    if (code === 'ru') return 'Русский';
    if (code === 'en') return 'English';
    return code ? code.toUpperCase() : t('common.all');
  }
  function trailerTypeLabel(type: string | undefined): string {
    if (type === 'Teaser') return t('title.trailer_teaser');
    if (type === 'Trailer') return t('title.trailer_official');
    return type || '';
  }
  function openTrailersModal(card: Card, trailers: Trailer[]): void {
    const returnMode = lastMode;

    // Distinct languages present, stable order.
    const langs: string[] = [];
    for (let i = 0; i < trailers.length; i++) {
      const l = trailers[i].lang || '';
      if (langs.indexOf(l) === -1) langs.push(l);
    }
    let curLang = langs.length ? langs[0] : '';
    const multiLang = langs.length > 1;

    const overlay = el('div', 'action-sheet-overlay');
    const box = el('div', 'action-sheet');
    const head = el('div', 'action-sheet__head');
    head.appendChild(el('div', 'action-sheet__title', t('title.trailer')));
    head.appendChild(el('div', 'action-sheet__sub', (card.title ? card.title + ' · ' : '') + 'YouTube'));
    const filters = el('div', 'watch-filters');
    head.appendChild(filters);
    box.appendChild(head);

    // Scrollable list (D-pad focus-follow via the app Scroll, like every list).
    const listScroll = new Scroll({ mask: true, over: true });
    const listWrap = el('div', 'action-sheet__list');
    listWrap.appendChild(listScroll.render());
    box.appendChild(listWrap);
    const listBody = listScroll.body();
    overlay.appendChild(box);
    container.appendChild(overlay);

    let lastItem: HTMLElement | false = false;
    let lastFilter: HTMLElement | false = false;

    function close(): void {
      if (overlay.parentNode) overlay.parentNode.removeChild(overlay);
      Controller.toggle(returnMode);
    }

    // Unified language dropdown (matches the catalog/watch filter style). Only
    // when the title actually has trailers in more than one language.
    let langValue: HTMLElement | null = null;
    if (multiLang) {
      const btn = el('div', 'filter-btn selector');
      btn.appendChild(el('div', 'filter-btn__label', t('title.trailer_lang')));
      langValue = el('div', 'filter-btn__value', langLabel(curLang));
      btn.appendChild(langValue);
      on(btn, 'hover:focus', function () {
        lastFilter = btn;
      });
      on(btn, 'hover:enter', function () {
        openLangChoice();
      });
      filters.appendChild(btn);
    } else {
      filters.classList.add('hide');
    }

    function openLangChoice(): void {
      const ov = el('div', 'modal-overlay modal-overlay--top');
      const panel = el('div', 'modal');
      const list = el('div', 'modal__list');
      const choiceScroll = new Scroll({ mask: true, over: true });
      list.appendChild(choiceScroll.render());
      const choiceBody = choiceScroll.body();
      let focusItem: HTMLElement | false = false;
      for (let i = 0; i < langs.length; i++) {
        (function (code: string) {
          const item = el('div', 'modal__item selector', langLabel(code));
          if (code === curLang) {
            item.classList.add('modal__item--current');
            focusItem = item;
          }
          on(item, 'hover:enter', function () {
            closeChoice();
            if (code !== curLang) {
              curLang = code;
              if (langValue) langValue.textContent = langLabel(code);
              renderList();
              // closeChoice returned focus to the filters mode; renderList moved
              // the ring onto a trailer card — sync the mode to the list so Up
              // doesn't close the whole modal (filters.up → close).
              Controller.toggle('title_trailers_list');
            }
          });
          choiceBody.appendChild(item);
        })(langs[i]);
      }
      panel.appendChild(list);
      ov.appendChild(panel);
      container.appendChild(ov);

      function closeChoice(): void {
        if (ov.parentNode) ov.parentNode.removeChild(ov);
        Controller.toggle('title_trailers_filters');
      }

      Controller.add('title_trailers_choice', {
        toggle: function () {
          Controller.collectionSet(choiceScroll.render());
          Controller.collectionFocus(focusItem || false, choiceScroll.render());
        },
        up: function () {
          Controller.moveOr('up');
        },
        down: function () {
          Controller.moveOr('down');
        },
        back: function () {
          closeChoice();
        },
      });
      Controller.toggle('title_trailers_choice');
    }

    function renderList(): void {
      empty(listBody);
      lastItem = false;
      for (let i = 0; i < trailers.length; i++) {
        if (multiLang && (trailers[i].lang || '') !== curLang) continue;
        (function (tr: Trailer) {
          const item = el('div', 'trailer-card selector');
          const thumb = el('div', 'trailer-card__thumb');
          if (tr.key) {
            const img = document.createElement('img');
            img.src = 'https://img.youtube.com/vi/' + encodeURIComponent(tr.key) + '/mqdefault.jpg';
            img.className = 'trailer-card__img';
            img.onerror = function () {
              if (img.parentNode) img.parentNode.removeChild(img); // fall back to the ▶ placeholder
            };
            thumb.appendChild(img);
          }
          thumb.appendChild(el('div', 'trailer-card__play', '▶'));
          item.appendChild(thumb);
          const info = el('div', 'trailer-card__info');
          info.appendChild(el('div', 'trailer-card__name', tr.name || t('title.trailer')));
          const meta = trailerTypeLabel(tr.type);
          if (meta) info.appendChild(el('div', 'trailer-card__meta', meta));
          item.appendChild(info);
          on(item, 'hover:focus', function () {
            lastItem = item;
          });
          on(item, 'hover:enter', function () {
            close();
            openTrailer(tr);
          });
          listBody.appendChild(item);
        })(trailers[i]);
      }
      Controller.collectionSet(listBody);
      Controller.collectionFocus(lastItem || false, listBody);
    }

    Controller.add('title_trailers_filters', {
      toggle: function () {
        Controller.collectionSet(filters);
        Controller.collectionFocus(lastFilter || false, filters);
      },
      left: function () {
        Controller.moveOr('left');
      },
      right: function () {
        Controller.moveOr('right');
      },
      up: function () {
        /* top edge — no-op; Back dismisses the modal */
      },
      down: function () {
        if (listBody.querySelector('.selector')) Controller.toggle('title_trailers_list');
      },
      back: function () {
        close();
      },
    });

    Controller.add('title_trailers_list', {
      toggle: function () {
        Controller.collectionSet(listBody);
        Controller.collectionFocus(lastItem || false, listBody);
      },
      left: function () {
        Controller.moveOr('left');
      },
      right: function () {
        Controller.moveOr('right');
      },
      up: function () {
        Controller.moveOr('up', function () {
          if (multiLang) Controller.toggle('title_trailers_filters');
        });
      },
      down: function () {
        Controller.moveOr('down');
      },
      back: function () {
        if (multiLang) Controller.toggle('title_trailers_filters');
        else close();
      },
    });

    renderList();
    Controller.toggle('title_trailers_list');
    lastMode = returnMode;
  }

  function openTrailer(trailer: Trailer): void {
    const returnMode = lastMode;
    const key = trailer.key || '';
    const watchUrl = trailer.youtube_url || 'https://www.youtube.com/watch?v=' + encodeURIComponent(key);

    const ov = el('div', 'trailer-overlay');
    const frameWrap = el('div', 'trailer-overlay__frame');
    ov.appendChild(frameWrap);

    let closed = false;
    let timer = 0;

    function close(): void {
      if (closed) return;
      closed = true;
      if (timer) window.clearTimeout(timer);
      window.removeEventListener('blur', refocus);
      if (ov.parentNode) ov.parentNode.removeChild(ov);
      Controller.toggle(returnMode);
      Controller.remove('trailer');
    }

    // Fallback panel: message + "open on YouTube" link (a real .selector so the
    // controller can focus and Enter it).
    function showFallback(): void {
      if (closed) return;
      if (timer) {
        window.clearTimeout(timer);
        timer = 0;
      }
      empty(frameWrap);
      const box = el('div', 'trailer-overlay__fallback');
      box.appendChild(el('div', 'trailer-overlay__message', t('title.trailer_unavailable')));
      const link = el('a', 'button selector', t('title.trailer_youtube')) as HTMLAnchorElement;
      link.href = watchUrl;
      link.target = '_blank';
      // Also handle Enter (some webviews don't act on <a> focus+enter reliably).
      on(link, 'hover:enter', function () {
        try {
          window.open(watchUrl, '_blank');
        } catch (e) {
          /* webview may ignore window.open; the href click still fires */
        }
      });
      box.appendChild(link);
      frameWrap.appendChild(box);
      // Re-arm the controller so the fallback link becomes focusable.
      Controller.toggle('trailer');
    }

    const iframe = document.createElement('iframe');
    iframe.className = 'trailer-overlay__iframe';
    // youtube-nocookie.com is the privacy-enhanced embed host; TV webviews that
    // block the main youtube.com embed frequently still allow this one.
    iframe.setAttribute(
      'src',
      'https://www.youtube-nocookie.com/embed/' +
        encodeURIComponent(key) +
        '?autoplay=1&playsinline=1&rel=0&modestbranding=1'
    );
    iframe.setAttribute('allow', 'autoplay; encrypted-media');
    iframe.setAttribute('frameborder', '0');
    iframe.setAttribute('allowfullscreen', 'true');
    // Keyboard focus must never land inside the cross-origin frame: once it does,
    // keydown stops reaching window and Back is dead — a trap only power-off
    // escapes on a TV. tabindex=-1 blocks Tab/D-pad, pointer-events blocks click
    // focus (autoplay=1 makes the embed's own controls unnecessary), and the blur
    // guard below yanks focus back if YouTube's script grabs it anyway.
    iframe.setAttribute('tabindex', '-1');
    iframe.style.pointerEvents = 'none';
    const refocus = function () {
      window.setTimeout(function () {
        const ae = document.activeElement;
        if (!closed && ae && ae.tagName === 'IFRAME') (ae as HTMLElement).blur();
      }, 0);
    };
    window.addEventListener('blur', refocus);
    let loaded = false;
    iframe.onload = function () {
      loaded = true;
    };
    iframe.onerror = function () {
      showFallback();
    };
    frameWrap.appendChild(iframe);

    // If the embed hasn't loaded within a few seconds assume it's blocked.
    timer = window.setTimeout(function () {
      timer = 0;
      if (!loaded) showFallback();
    }, 12000);

    container.appendChild(ov);

    Controller.add('trailer', {
      toggle: function () {
        Controller.collectionSet(ov);
        Controller.collectionFocus(false, ov);
      },
      up: function () {
        Controller.moveOr('up');
      },
      down: function () {
        Controller.moveOr('down');
      },
      left: function () {
        Controller.moveOr('left');
      },
      right: function () {
        Controller.moveOr('right');
      },
      back: function () {
        close();
      },
    });
    Controller.toggle('trailer');
    lastMode = returnMode; // keep resume() returning to the buttons, not 'trailer'
  }

  function showError(): void {
    empty(page1Inner);
    const box = buildState({
      kind: 'error',
      text: t('error.load'),
      onRetry: function () {
        load();
      },
    });
    page1Inner.appendChild(box);

    Controller.add('title', {
      toggle: function () {
        Controller.collectionSet(box);
        Controller.collectionFocus(false, box);
      },
      left: function () {
        Controller.toggle('menu');
      },
      back: function () {
        router.back();
      },
    });
    toggleMode('title');
  }

  function render(card: Card): void {
    empty(page1Inner);
    background.set(card.backdrop);

    // ---- full-start-new: poster (left) + info column (right) ----
    // DOM/classes ported 1:1 from Lampa src/templates/full/start_new + full.js.
    const start = el('div', 'full-start-new');
    const startBody = el('div', 'full-start-new__body');

    const right = el('div', 'full-start-new__right');

    // Head line: year · genres (Lampa full-start-new__head).
    const headBits: string[] = [];
    if (card.year) headBits.push(String(card.year));
    if (card.genres && card.genres.length) headBits.push(card.genres.join(', '));
    if (headBits.length) {
      right.appendChild(el('div', 'full-start-new__head', headBits.join(' · ')));
    }

    right.appendChild(el('div', 'full-start-new__title', card.title || ''));
    if (card.original_title && card.original_title !== card.title) {
      right.appendChild(el('div', 'full-start-new__tagline', card.original_title));
    }

    // Rate line: rating badge · certificate · runtime (Lampa full-start-new__rate-line).
    const rateLine = el('div', 'full-start-new__rate-line');
    if (card.rating) {
      const rate = el('div', 'full-start__rate');
      rate.appendChild(el('div', undefined, card.rating.toFixed(1)));
      rate.appendChild(el('div', undefined, 'TMDB'));
      rateLine.appendChild(rate);
    }
    // IMDB pill (from OMDb via the backend). Only when a real score is present:
    // the field is absent or 0 when there's no OMDb key or IMDb returned N/A.
    if (card.imdb_rating && card.imdb_rating > 0) {
      const imdb = el('div', 'full-start__rate full-start__rate--imdb');
      imdb.appendChild(el('div', undefined, card.imdb_rating.toFixed(1)));
      imdb.appendChild(el('div', undefined, 'IMDB'));
      rateLine.appendChild(imdb);
    }
    if (card.content_rating) {
      rateLine.appendChild(el('div', 'full-start__pg', card.content_rating));
    }
    if (card.runtime_minutes) {
      rateLine.appendChild(el('span', 'full-start-new__split', runtimeText(card.runtime_minutes)));
    }
    if (rateLine.firstChild) right.appendChild(rateLine);

    if (card.overview) {
      right.appendChild(el('div', 'full-start-new__description', card.overview));
    }

    // ---- action buttons (Lampa full-start__button) ----
    const actions = el('div', 'full-start-new__buttons');
    let lastAction: HTMLElement | false = false;

    function makeAction(icon: string, label: string, accent: boolean): { btn: HTMLElement; span: HTMLElement } {
      const btn = el('div', 'full-start__button selector' + (accent ? ' button--accent' : ''));
      btn.appendChild(iconEl(icon, 'full-start__button-ico'));
      const span = el('span', undefined, label);
      btn.appendChild(span);
      on(btn, 'hover:focus', function () {
        lastAction = btn;
      });
      actions.appendChild(btn);
      return { btn: btn, span: span };
    }

    // "Continue S2E5 · 25:12" — one press to the player, instead of watch →
    // source → episode → play. Only when there is somewhere to continue from.
    const rp = resumePoint(card);
    prefetchResolve(card, rp);
    continueBtn = null;
    if (rp) {
      let label = t('title.continue');
      if (rp.episode != null) label += ' S' + (rp.season != null ? rp.season : 1) + 'E' + rp.episode;
      if (rp.position > 0) label += ' · ' + fmtClock(rp.position);
      continueBtn = makeAction(ICON_PLAY, label, true).btn;
    }
    const watch = makeAction(ICON_PLAY, t('title.watch'), !rp).btn;
    // "hover:enter" is wired below, once season data is available.
    const torrents = makeAction(ICON_TORRENT, t('title.torrents'), false).btn;

    // ---- trailer (YouTube) — only when the backend supplied trailers -------
    if (card.trailers && card.trailers.length) {
      const trailerBtn = makeAction(ICON_TRAILER, t('title.trailer'), false).btn;
      on(trailerBtn, 'hover:enter', function () {
        openTrailersModal(card, card.trailers as Trailer[]);
      });
    }

    // ---- bookmark toggle ("В обране" / "Прибрати") ----
    let bookmarkBusy = false;
    const bookmarkMk = makeAction(ICON_BOOKMARK, '', false);
    const bookmark = bookmarkMk.btn;
    const bookmarkLabel = bookmarkMk.span;

    function refreshBookmark(): void {
      const on_ = sync.isBookmarked(card.tmdb_id, card.type);
      bookmarkLabel.textContent = on_ ? t('title.bookmark_remove') : t('title.bookmark_add');
      bookmark.classList.toggle('button--active', on_);
    }
    refreshBookmark();

    on(bookmark, 'hover:enter', function () {
      if (!isLogged()) {
        openLogin();
        return;
      }
      // In flight → ignore: a TV remote's double OK fired add and remove in
      // parallel, and the server applied them in whichever order they landed.
      if (bookmarkBusy) return;
      bookmarkBusy = true;
      const done = function () {
        bookmarkBusy = false;
      };
      sync
        .toggleBookmark(card.tmdb_id, card.type, {
          title: card.title,
          poster: card.poster,
          backdrop: card.backdrop,
          year: card.year,
          rating: card.rating,
        })
        .then(done, done);
      refreshBookmark();
    });

    if (unsubBookmarks) unsubBookmarks();
    unsubBookmarks = sync.subscribe('bookmarks', refreshBookmark);

    // ---- add-to-playlist ("Додати в плейлист") ----
    const playlistBtn = makeAction(ICON_PLAYLIST, t('title.playlist_add'), false).btn;
    on(playlistBtn, 'hover:enter', function () {
      if (!isLogged()) {
        openLogin();
        return;
      }
      openPlaylistPicker(card);
    });

    right.appendChild(actions);
    startBody.appendChild(right);
    start.appendChild(startBody);

    // Page 1 is a single vertical Scroll (Lampa full: info + action buttons).
    // The enclosing Scroll auto-follows focus via Controller.autoScrollTo. The
    // watch/torrents picker is now an in-place modal opened over this page.
    page1.classList.add('title-page--scroll');
    const pageScroll = new Scroll({ mask: true, over: true });
    pageScroll.render().className += ' title-page__scroll';
    page1Inner.appendChild(pageScroll.render());
    const pageBody = pageScroll.body();
    pageBody.appendChild(start);

    // Season list for the online picker (page 2). For a series with no
    // metadata seasons we synthesize a single season 1 so the picker still
    // works (episodes are then lazy-loaded via getTitleSeason).
    let seasons: Season[] = card.type === 'tv' && card.seasons ? card.seasons : [];
    if (card.type === 'tv' && !seasons.length) {
      seasons = [{ season: 1, name: t('title.season') + ' 1' }];
    }

    // The two action buttons are a tab selector for the in-place watch modal:
    // "Дивитись" opens it on the online tab, "Торренти" on the torrents tab.
    on(watch, 'hover:enter', function () {
      openWatchModal(card, seasons, 'online');
    });
    if (continueBtn && rp) {
      on(continueBtn, 'hover:enter', function () {
        // Last played via torrent → reopen that torrent file, not an online source.
        let lastTorrent = false;
        try {
          const raw = window.localStorage.getItem('promin:lastsrc:' + card.type + ':' + card.tmdb_id);
          lastTorrent = !!raw && (JSON.parse(raw) as { balanser?: string }).balanser === 'torrent';
        } catch (e) {
          /* ignore */
        }
        openWatchModal(card, seasons, lastTorrent ? 'torrents' : 'online', { season: rp.season, episode: rp.episode });
      });
    }
    on(torrents, 'hover:enter', function () {
      openWatchModal(card, seasons, 'torrents');
    });
    openEpisode = function (season: number | null, episode: number | null) {
      openWatchModal(card, seasons, 'online', { season: season, episode: episode });
    };

    // ---- page 1 controller (action buttons) ----
    const actionsController = {
      toggle: function () {
        Controller.collectionSet(actions);
        Controller.collectionFocus(lastAction || false, actions);
      },
      left: function () {
        Controller.moveOr('left', function () {
          Controller.toggle('menu');
        }); // exit to the rail from the leftmost button
      },
      right: function () {
        Controller.moveOr('right');
      },
      down: function () {
        // Open the modal on the tab of whichever button is focused (Торренти
        // → torrents), not always online.
        openWatchModal(card, seasons, lastAction === torrents ? 'torrents' : 'online');
      },
      back: function () {
        router.back();
      },
    };
    Controller.add('title', actionsController);

    toggleMode('title');
  }

  // ---- in-place watch/torrents modal (action-sheet template) ----
  // One modal, built per open for exactly the requested tab (no in-modal tab
  // chips). Online: source/season/voice dropdowns over an episode/quality list
  // (getOnlineSources → resolveOnline → openPlayer, timecode sync, next/prev/
  // voice re-resolve). Torrents: tracker/quality dropdowns over a folder list
  // (getTorrents → addTorrent → /stream → openPlayer, file picker for packs).
  // Both flows are ported verbatim from the old page-2 picker + screens/sources;
  // the only playback-adjacent change is a `if (closed) return;` guard so a Back
  // during a load doesn't grab focus. openPlayer is a router.push that keeps the
  // title (and this hidden overlay) mounted — closed stays false during playback
  // so onVoice/onNext/onPrev re-resolve keep working.
  function openWatchModal(
    card: Card,
    seasons: Season[],
    tab: 'online' | 'torrents',
    auto?: { season: number | null; episode: number | null }
  ): void {
    const isSeries = card.type === 'tv';
    // Per-title memory of the last source/voice the user actually played, so
    // "Continue" (and every reopen) lands on the same dub, not the first balancer.
    const lastSrcKey = 'promin:lastsrc:' + card.type + ':' + card.tmdb_id;
    // balanser 'torrent' = the last thing played was a torrent file: `torrent`
    // is the magnet id to re-add, `file` the file index inside it.
    interface LastSrc {
      balanser?: string;
      voice?: string | null;
      torrent?: string;
      file?: number;
      title?: string;
    }
    function readLastSrc(): LastSrc | null {
      try {
        const raw = window.localStorage.getItem(lastSrcKey);
        return raw ? (JSON.parse(raw) as LastSrc) : null;
      } catch (e) {
        return null;
      }
    }
    function writeLastSrc(balanser: string, voice: string | null): void {
      try {
        window.localStorage.setItem(lastSrcKey, JSON.stringify({ balanser: balanser, voice: voice }));
      } catch (e) {
        /* storage unavailable — memory is a convenience */
      }
    }
    function writeLastTorrent(torId: string, fileIndex: number, title: string): void {
      try {
        window.localStorage.setItem(lastSrcKey, JSON.stringify({ balanser: 'torrent', torrent: torId, file: fileIndex, title: title }));
      } catch (e) {
        /* ignore */
      }
    }

    let closed = false;
    let loaded = false;
    let busy = false;
    // Set by the online tab: invalidates the in-flight resolve so its late result
    // can't open the player after the user backed out of the spinner.
    let cancelBusy: (() => void) | null = null;

    // ---- overlay + action-sheet box (trailers-modal recipe) ----
    const overlay = el('div', 'action-sheet-overlay');
    const box = el('div', 'action-sheet');
    const head = el('div', 'action-sheet__head');
    head.appendChild(el('div', 'action-sheet__title', tab === 'torrents' ? t('sources.torrents') : t('title.watch')));
    if (card.title) head.appendChild(el('div', 'action-sheet__sub', card.title));
    const filters = el('div', 'watch-filters');
    head.appendChild(filters);
    box.appendChild(head);

    const listScroll = new Scroll({ mask: true, over: true });
    const listWrap = el('div', 'action-sheet__list');
    listWrap.appendChild(listScroll.render());
    box.appendChild(listWrap);
    const listBody = listScroll.body();
    listBody.className += ' full-episodes';
    overlay.appendChild(box);
    container.appendChild(overlay);
    // "Continue": the sheet stays hidden while autoplay works; it appears only if
    // autoplay fails or the user cancels (see autoResolve).
    if (auto) box.classList.add('hide');

    let lastFilter: HTMLElement | false = false;
    let lastRow: HTMLElement | false = false;

    // Back handler for the list mode — default returns to the filter row; the
    // torrent file-picker overrides it to return to the torrent list.
    let listBack: () => void = function () {
      toggleMode('watch_filters');
    };

    function makeFilter(labelKey: string, initial: string): { el: HTMLElement; value: HTMLElement } {
      const btn = el('div', 'filter-btn selector');
      btn.appendChild(el('div', 'filter-btn__label', t(labelKey)));
      const value = el('div', 'filter-btn__value', initial);
      btn.appendChild(value);
      on(btn, 'hover:focus', function () {
        lastFilter = btn;
      });
      return { el: btn, value: value };
    }

    function close(): void {
      closed = true;
      hideOverlay();
      if (overlay.parentNode) overlay.parentNode.removeChild(overlay);
      toggleMode('title');
      Controller.remove('watch_filters');
      Controller.remove('watch_list');
    }

    // ---- dropdown modal (catalog modal-overlay pattern) ----
    function openChoice(
      title: string,
      options: Array<{ label: string; value: string }>,
      current: string,
      onPick: (v: string) => void
    ): void {
      const ov = el('div', 'modal-overlay');
      const panel = el('div', 'modal');
      if (title) panel.appendChild(el('div', 'modal__title', title));
      const list = el('div', 'modal__list');
      const choiceScroll = new Scroll({ mask: true, over: true });
      list.appendChild(choiceScroll.render());
      const choiceBody = choiceScroll.body();
      let focusItem: HTMLElement | false = false;
      for (let i = 0; i < options.length; i++) {
        (function (opt: { label: string; value: string }) {
          const item = el('div', 'modal__item selector', opt.label);
          if (opt.value === current) {
            item.classList.add('modal__item--current');
            focusItem = item;
          }
          on(item, 'hover:enter', function () {
            closeChoice();
            onPick(opt.value);
          });
          choiceBody.appendChild(item);
        })(options[i]);
      }
      panel.appendChild(list);
      ov.appendChild(panel);
      container.appendChild(ov);

      function closeChoice(): void {
        if (ov.parentNode) ov.parentNode.removeChild(ov);
        toggleMode('watch_filters');
      }

      Controller.add('watch_modal', {
        toggle: function () {
          Controller.collectionSet(choiceScroll.render());
          Controller.collectionFocus(focusItem || false, choiceScroll.render());
        },
        up: function () {
          Controller.moveOr('up');
        },
        down: function () {
          Controller.moveOr('down');
        },
        back: function () {
          closeChoice();
        },
      });
      toggleMode('watch_modal');
    }

    // ---- shared list states ----
    function showListLoading(): void {
      empty(listBody);
      lastRow = false;
      listBody.appendChild(buildState({ kind: 'loading', text: t('common.loading') }));
      listScroll.reset();
    }

    function showListMessage(text: string, onRetry?: () => void): void {
      empty(listBody);
      lastRow = false;
      const box2 = buildState({ kind: 'error', text: text, onRetry: onRetry });
      listBody.appendChild(box2);
      listScroll.reset();
    }

    // ---- shared controllers ----
    Controller.add('watch_filters', {
      toggle: function () {
        Controller.collectionSet(filters);
        Controller.collectionFocus(lastFilter || false, filters);
      },
      left: function () {
        Controller.moveOr('left');
      },
      right: function () {
        Controller.moveOr('right');
      },
      up: function () {
        /* top edge — no-op; Back dismisses the modal */
      },
      down: function () {
        if (listBody.querySelector('.selector')) toggleMode('watch_list');
      },
      back: function () {
        close();
      },
    });

    Controller.add('watch_list', {
      toggle: function () {
        Controller.collectionSet(listBody);
        Controller.collectionFocus(lastRow || false, listBody);
      },
      left: function () {
        Controller.moveOr('left');
      },
      right: function () {
        Controller.moveOr('right');
      },
      up: function () {
        Controller.moveOr('up', function () {
          toggleMode('watch_filters');
        });
      },
      down: function () {
        Controller.moveOr('down');
      },
      back: function () {
        // Back on the spinner = cancel. Before, Back changed the mode under the
        // spinner and a second Back closed the modal while the resolve ran on —
        // then opened the player when nobody was waiting for it.
        if (busy) {
          busy = false;
          hideOverlay();
          if (cancelBusy) cancelBusy();
          return;
        }
        listBack();
      },
    });

    if (tab === 'online') {
      // ================= ONLINE =================
      const fSource = makeFilter('online.source', '');
      filters.appendChild(fSource.el);
      let fSeason: { el: HTMLElement; value: HTMLElement } | null = null;
      if (isSeries) {
        fSeason = makeFilter('online.season', '');
        filters.appendChild(fSeason.el);
      }
      const fVoice = makeFilter('online.voice', '');
      filters.appendChild(fVoice.el);

      // ---- state ----
      let sources: OnlineSource[] = [];
      let currentSource: OnlineSource | null = null;
      let sourceToken = 0; // bumped on every source switch; stale resolves bail (B14)
      cancelBusy = function () {
        sourceToken++;
        if (autoActive) {
          // Back during "Continue": stop trying sources, show the sheet for a
          // manual pick with whatever source we were on.
          revealModal();
          if (isSeries) probeVoices();
          else probeMovie();
        }
      };
      let voices: Voice[] = []; // filled from the last resolve
      let currentVoice: string | null = null;
      // Default to the first "real" season (skip a leading Specials / season 0,
      // which usually has no episodes) — mirrors Lampa's default selection.
      let currentSeason = -1;
      if (isSeries) {
        currentSeason = seasons[0].season;
        for (let si = 0; si < seasons.length; si++) {
          if (seasons[si].season >= 1) {
            currentSeason = seasons[si].season;
            break;
          }
        }
      }
      let currentEpisode = isSeries ? 1 : -1;
      // "Continue" target: play it as soon as a source is ready (see onSourceReady).
      let autoPlay = auto || null;
      if (autoPlay) {
        if (autoPlay.season != null) currentSeason = autoPlay.season;
        if (autoPlay.episode != null) currentEpisode = autoPlay.episode;
      }
      const remembered = readLastSrc();
      if (remembered && remembered.voice) currentVoice = remembered.voice;
      let movieMedia: PlayerMedia | null = null; // last resolved movie streams
      const epCache: { [season: number]: Episode[] } = {};

      function seasonName(n: number): string {
        for (let i = 0; i < seasons.length; i++) {
          if (seasons[i].season === n) return seasons[i].name || t('title.season') + ' ' + n;
        }
        return t('title.season') + ' ' + n;
      }

      function refreshLabels(): void {
        fSource.value.textContent = currentSource ? currentSource.name || currentSource.balanser : t('common.loading');
        if (fSeason) fSeason.value.textContent = seasonName(currentSeason);
        let vlabel = t('online.voice_auto');
        for (let i = 0; i < voices.length; i++) {
          if (voices[i].id === currentVoice) vlabel = voices[i].name;
        }
        fVoice.value.textContent = vlabel;
      }

      // ---- resolve helpers (ported from screens/sources) ----
      function resolveParamsFor(episode: number | null, voice: string | null): ResolveParams {
        return {
          balanser: (currentSource as OnlineSource).balanser,
          tmdb_id: card.tmdb_id,
          type: card.type,
          title: card.title,
          original_title: card.original_title,
          year: card.year != null ? card.year : undefined,
          imdb_id: card.external_ids ? card.external_ids.imdb_id : undefined,
          season: isSeries ? currentSeason : undefined,
          episode: episode != null ? episode : undefined,
          voice: voice != null ? voice : undefined,
        };
      }

      function toMedia(res: ResolveResponse, voice: string | null): PlayerMedia {
        return {
          type: res.type === 'mp4' ? 'mp4' : 'hls',
          streams: res.streams || [],
          subtitles: res.subtitles || [],
          voices: res.voices || [],
          currentVoice: res.voice || voice,
          audioNames: res.audio_names,
        };
      }

      // ---- open the player on a resolved media (shared movie/series) ----
      function openMedia(media: PlayerMedia, episode: number | null): void {
        if (currentSource) writeLastSrc(currentSource.balanser, currentVoice);
        let seasonNum: number | null = isSeries ? currentSeason : null;
        let epNum = episode;
        const tc = isLogged() ? sync.getTimecodeCached(card.tmdb_id, card.type, seasonNum, epNum) : null;

        openPlayer({
          title: card.title,
          subtitle: playSubtitle(seasonNum, epNum),
          poster: card.poster,
          media: media,
          tmdb_id: card.tmdb_id,
          media_type: card.type,
          imdb_id: card.external_ids ? card.external_ids.imdb_id : undefined,
          season: seasonNum,
          episode: epNum,
          resume: tc ? { position_sec: tc.position_sec, duration_sec: tc.duration_sec } : null,
          onProgress: function (pos, dur) {
            if (isLogged()) sync.saveTimecode(card.tmdb_id, card.type, seasonNum, epNum, pos, dur);
          },
          onVoice: function (voiceId, done) {
            resolveOnline(resolveParamsFor(isSeries ? epNum : null, voiceId)).then(
              function (r2) {
                if (closed) return;
                if (r2 && r2.streams && r2.streams.length) {
                  currentVoice = voiceId;
                  if (currentSource) writeLastSrc(currentSource.balanser, currentVoice); // "Continue" reopens this dub
                  refreshLabels();
                  done(toMedia(r2, voiceId));
                } else done(null);
              },
              function () {
                done(null);
              }
            );
          },
          // Movies: "next" is the watch queue head. A getter, so the phone adding
          // to the queue mid-film reaches the player's ended/skip paths live.
          get onNext() {
            if (isSeries && epNum != null)
              return function (done: NextDone) {
                switchEp((epNum as number) + 1, done);
              };
            return sync.queueLength() ? queueNext : undefined;
          },
          onPrev:
            isSeries && epNum != null
              ? function (done) {
                  switchEp((epNum as number) - 1, done);
                }
              : undefined,
          onEpisodes:
            isSeries && epNum != null
              ? function (done) {
                  const sn = seasonNum as number;
                  ensureSeason(sn).then(
                    function (list) {
                      if (closed) return;
                      const eps = toPlayerEpisodes(sn, list);
                      if (adjacentSeason(sn, 1) == null) {
                        // last season: the queue head is what follows the finale
                        withQueueTail(eps, function () {
                          done(eps, epNum as number);
                        });
                      } else done(eps, epNum as number);
                    },
                    function () {
                      done([], epNum as number);
                    }
                  );
                }
              : undefined,
          onEpisode:
            isSeries && epNum != null
              ? function (n, done) {
                  switchEp(n, done);
                }
              : undefined,
        });

        // Resolve a target episode and hand back media + that episode's own saved
        // timecode (resume), keeping epNum/seasonNum in sync so progress-saves and
        // further prev/next target the right episode. Past the season's last
        // episode (or before its first) the jump crosses into the adjacent
        // season — "no source" after the finale was a dead end.
        function switchEp(target: number, done: (m: PlayerMedia | null, meta?: import('../core/player').EpisodeMeta) => void): void {
          const sn = seasonNum as number;
          ensureSeason(sn).then(
            function (list) {
              if (closed) return;
              let lo = 1;
              let hi = 0;
              for (let i = 0; i < list.length; i++) {
                if (i === 0 || list[i].episode < lo) lo = list[i].episode;
                if (list[i].episode > hi) hi = list[i].episode;
              }
              if (list.length && target > hi) {
                crossSeason(adjacentSeason(sn, 1), 'first', done);
                return;
              }
              if (target < lo || target < 1) {
                crossSeason(adjacentSeason(sn, -1), 'last', done);
                return;
              }
              resolveEp(sn, target, done);
            },
            function () {
              resolveEp(sn, target, done);
            }
          );
        }
        function crossSeason(
          ns: number | null,
          which: 'first' | 'last',
          done: (m: PlayerMedia | null, meta?: import('../core/player').EpisodeMeta) => void
        ): void {
          if (ns == null) {
            // true edge of the show: forward → the watch queue, else the player's toast
            if (which === 'first' && playQueueHead()) return;
            done(null);
            return;
          }
          ensureSeason(ns).then(
            function (list) {
              if (closed) return;
              if (!list.length) {
                done(null);
                return;
              }
              resolveEp(ns, which === 'first' ? list[0].episode : list[list.length - 1].episode, done);
            },
            function () {
              done(null);
            }
          );
        }
        function resolveEp(sn: number, target: number, done: (m: PlayerMedia | null, meta?: import('../core/player').EpisodeMeta) => void): void {
          const prevSeason = currentSeason;
          currentSeason = sn; // resolveParamsFor reads it
          resolveOnline(resolveParamsFor(target, currentVoice)).then(
            function (m) {
              if (closed) return;
              if (m && m.streams && m.streams.length) {
                seasonNum = sn;
                epNum = target;
                currentEpisode = target; // the list must follow the player's next/prev
                if (sn !== prevSeason) renderSeason(sn);
                refreshLabels();
                const tc2 = isLogged() ? sync.getTimecodeCached(card.tmdb_id, card.type, sn, target) : null;
                done(toMedia(m, currentVoice), {
                  season: sn,
                  episode: target,
                  title: card.title,
                  subtitle: playSubtitle(sn, target),
                  resume: tc2 ? { position_sec: tc2.position_sec, duration_sec: tc2.duration_sec } : null,
                });
              } else {
                currentSeason = prevSeason;
                done(null);
              }
            },
            function () {
              currentSeason = prevSeason;
              done(null);
            }
          );
        }
      }

      // probeVoices() and the immediately following playEpisode() resolve the
      // SAME (source, season, episode, voice) — two balancer round-trips (each
      // up to ~25s) for one "Дивитись". Successful results are remembered for a
      // minute; resolved stream URLs live much longer than that.
      const RESOLVE_MEMO_MS = 60 * 1000;
      const resolveCache: { [k: string]: { at: number; res: ResolveResponse } } = {};
      function resolveMemo(params: ResolveParams): Promise<ResolveResponse> {
        const k = JSON.stringify(params);
        const hit = resolveCache[k];
        if (hit && Date.now() - hit.at < RESOLVE_MEMO_MS) return Promise.resolve(hit.res);
        const pending = prefetch && prefetch.key === k ? prefetch.p : resolveOnline(params);
        if (prefetch && prefetch.key === k) prefetch = null; // consumed once; a later open re-resolves
        return pending.then(function (res) {
          if (res && res.streams && res.streams.length) resolveCache[k] = { at: Date.now(), res: res };
          return res;
        });
      }

      // One resolve attempt guarded against source switches mid-flight.
      function resolveRetry(
        params: ResolveParams,
        tries: number,
        onDone: (res: ResolveResponse | null) => void,
        onErr: () => void
      ): void {
        const myToken = sourceToken; // snapshot: a source switch mid-resolve invalidates this
        resolveMemo(params).then(function (res) {
          if (closed || myToken !== sourceToken) return;
          void tries;
          onDone(res || null);
        }, function () {
          if (closed || myToken !== sourceToken) return;
          onErr();
        });
      }

      // ---- movie: resolve current source → list qualities → play ----
      function probeMovie(): void {
        if (!currentSource) return;
        showListLoading();
        resolveRetry(
          resolveParamsFor(null, currentVoice),
          2,
          function (res) {
            if (!res || !res.streams || !res.streams.length) {
              showListMessage(t('sources.resolve_empty'), probeMovie);
              return;
            }
            movieMedia = toMedia(res, currentVoice);
            if (res.voices && res.voices.length) voices = res.voices;
            if (res.voice) currentVoice = res.voice; // show the dub actually playing, not "auto"
            refreshLabels();
            renderStreams(movieMedia);
          },
          function () {
            showListMessage(t('sources.resolve_failed'), probeMovie);
          }
        );
      }

      function renderStreams(media: PlayerMedia): void {
        empty(listBody);
        lastRow = false;
        const streams = media.streams || [];
        if (!streams.length) {
          listBody.appendChild(buildState({ kind: 'empty', text: t('sources.resolve_empty') }));
          return;
        }
        for (let i = 0; i < streams.length; i++) {
          (function (idx: number) {
            const st = streams[idx];
            const row = el('div', 'online-quality selector');
            const qi = el('div', 'online-quality__ico');
            qi.appendChild(iconEl(ICON_PLAY, ''));
            row.appendChild(qi);
            const qbody = el('div', 'online-quality__body');
            qbody.appendChild(
              el('div', 'online-quality__label', st.label || st.quality || t('online.quality') + ' ' + (idx + 1))
            );
            if (st.quality) {
              const qm = el('div', 'online-quality__meta');
              qm.appendChild(qBadge(st.quality));
              qbody.appendChild(qm);
            }
            row.appendChild(qbody);
            on(row, 'hover:focus', function () {
              lastRow = row;
            });
            on(row, 'hover:enter', function () {
              playMovie(idx);
            });
            listBody.appendChild(row);
          })(i);
        }
        listScroll.reset();
      }

      function playMovie(idx: number): void {
        if (!movieMedia || busy) return;
        const base = movieMedia;
        const ordered = base.streams.slice();
        if (idx > 0 && idx < ordered.length) {
          const chosen = ordered.splice(idx, 1)[0];
          ordered.unshift(chosen);
        }
        openMedia(
          { type: base.type, streams: ordered, subtitles: base.subtitles, voices: base.voices, currentVoice: currentVoice, audioNames: base.audioNames },
          null
        );
      }

      // ---- series: episode list + resolve-per-episode play ----
      function fmtDur(min: number | null | undefined): string {
        if (!min) return '';
        return min + t('unit.min');
      }

      function renderEpisodes(list: Episode[]): void {
        empty(listBody);
        lastRow = false;
        if (!list.length) {
          listBody.appendChild(buildState({ kind: 'empty', text: t('online.episodes_empty') }));
          return;
        }
        for (let i = 0; i < list.length; i++) {
          const ep = list[i];
          const row = el('div', 'season-episode selector');
          row.setAttribute('data-ep', String(ep.episode));

          const imgWrap = el('div', 'season-episode__img');
          const img = el('img') as HTMLImageElement;
          img.alt = '';
          if (ep.still) {
            img.onload = function () {
              imgWrap.classList.add('season-episode__img--loaded');
            };
            // Eager-load only the first screenful; the rest load on first focus
            // (a 24-episode season was 24 poster loads competing with resolve).
            if (i < 6) img.src = ep.still;
          }
          imgWrap.appendChild(img);
          imgWrap.appendChild(el('div', 'season-episode__episode-number', String(ep.episode)));
          row.appendChild(imgWrap);

          // Duration/position for the watched bar + "поз / тривалість" text.
          const dur =
            ep.timecode && ep.timecode.duration_sec > 0
              ? ep.timecode.duration_sec
              : ep.runtime_minutes
                ? ep.runtime_minutes * 60
                : 0;
          const pos = ep.timecode ? ep.timecode.position_sec : 0;
          const viewed = !!(dur > 0 && pos >= dur * 0.9);
          if (viewed) row.classList.add('season-episode--viewed'); // dim watched
          if (viewed) {
            const v = el('div', 'season-episode__viewed');
            v.appendChild(iconEl(ICON_CHECK, ''));
            row.appendChild(v);
          }

          const epBody = el('div', 'season-episode__body');
          const headRow = el('div', 'season-episode__head');
          headRow.appendChild(
            el('div', 'season-episode__title', ep.episode + '. ' + (ep.name || t('title.episode') + ' ' + ep.episode))
          );
          // Time: "25:12 / 1:01:23" once started; total alone before; runtime else.
          let timeText = '';
          if (pos > 0 && dur > 0) timeText = fmtClock(pos) + ' / ' + fmtClock(dur);
          else if (dur > 0) timeText = fmtClock(dur);
          else timeText = fmtDur(ep.runtime_minutes);
          headRow.appendChild(el('div', 'season-episode__time', timeText));
          epBody.appendChild(headRow);

          // Full-width watched bar along the whole item bottom (100% = fully watched).
          if (pos > 0 && dur > 0) {
            const prog = el('div', 'season-episode__progress');
            const fill = el('div', 'season-episode__progress-fill');
            const pct = Math.max(0, Math.min(100, (pos / dur) * 100));
            fill.style.width = pct.toFixed(1) + '%';
            prog.appendChild(fill);
            row.appendChild(prog);
          }

          const footer = el('div', 'season-episode__footer');
          footer.appendChild(el('div', 'season-episode__info', ep.air_date || ep.overview || ''));
          const quality = el('div', 'season-episode__quality');
          if (ep.rating && ep.rating > 0) {
            quality.className += ' season-episode__vote';
            quality.textContent = '★ ' + ep.rating.toFixed(1);
          }
          footer.appendChild(quality);
          epBody.appendChild(footer);

          row.appendChild(epBody);

          (function (episode: number, stillUrl: string | null | undefined, image: HTMLImageElement) {
            on(row, 'hover:focus', function () {
              lastRow = row;
              if (stillUrl && !image.src) image.src = stillUrl; // lazy poster
              // Pointer hover retargets the ring/collection but not the mode —
              // without this the mode stays on the filter chips, whose UP is a
              // no-op, so arrows go dead after hovering into the list.
              if (Controller.enabled().name === 'watch_filters') toggleMode('watch_list');
            });
            on(row, 'hover:enter', function () {
              playEpisode(episode);
            });
          })(ep.episode, ep.still, img);
          listBody.appendChild(row);
        }
        listScroll.reset();
      }

      // After the player closes: the list was drawn before playback, so its
      // progress bars / ✓ are stale and focus sits on the episode you STARTED
      // from, not the one you stopped at (autoplay may have moved several on).
      resumeHook = function () {
        if (closed || !isSeries) return;
        const eps = epCache[currentSeason];
        if (!eps) return;
        for (let i = 0; i < eps.length; i++) {
          const tc = sync.getTimecodeCached(card.tmdb_id, card.type, currentSeason, eps[i].episode);
          if (tc) eps[i].timecode = { position_sec: tc.position_sec, duration_sec: tc.duration_sec };
        }
        renderEpisodes(eps);
        const row = listBody.querySelector('.season-episode[data-ep="' + currentEpisode + '"]') as HTMLElement | null;
        if (row) lastRow = row;
      };

      // Episode list of a season: memo → inline card data → /catalog/title?season.
      function ensureSeason(n: number): Promise<Episode[]> {
        if (epCache[n]) return Promise.resolve(epCache[n]);
        for (let i = 0; i < seasons.length; i++) {
          if (seasons[i].season === n && seasons[i].episodes && (seasons[i].episodes as Episode[]).length) {
            epCache[n] = seasons[i].episodes as Episode[];
            return Promise.resolve(epCache[n]);
          }
        }
        return getTitleSeason(card.type, card.tmdb_id, n).then(function (res) {
          let eps: Episode[] = [];
          if (res && res.episodes) {
            eps = res.episodes;
          } else if (res && res.seasons) {
            for (let i = 0; i < res.seasons.length; i++) {
              if (res.seasons[i].season === n && res.seasons[i].episodes) {
                eps = res.seasons[i].episodes as Episode[];
                break;
              }
            }
          }
          epCache[n] = eps;
          return eps;
        });
      }
      // Neighbouring real season (>= 1) in the picker order, or null at the edge.
      function adjacentSeason(n: number, dir: 1 | -1): number | null {
        let idx = -1;
        for (let i = 0; i < seasons.length; i++) if (seasons[i].season === n) idx = i;
        for (let i = idx + dir; i >= 0 && i < seasons.length; i += dir) {
          if (seasons[i].season >= 1) return seasons[i].season;
        }
        return null;
      }
      function episodeName(season: number | null, ep: number | null): string {
        const list = season != null ? epCache[season] : null;
        if (!list || ep == null) return '';
        for (let i = 0; i < list.length; i++) if (list[i].episode === ep) return list[i].name || '';
        return '';
      }
      // Player info-bar second line: "S1E3 · Episode name" (movies: none).
      function playSubtitle(season: number | null, ep: number | null): string {
        if (!isSeries || ep == null) return '';
        const name = episodeName(season, ep);
        return 'S' + (season != null ? season : 1) + 'E' + ep + (name ? ' · ' + name : '');
      }
      function toPlayerEpisodes(season: number | null, list: Episode[]): import('../core/player').PlayerEpisode[] {
        const out: import('../core/player').PlayerEpisode[] = [];
        for (let i = 0; i < list.length; i++) {
          const ep = list[i];
          const tc = ep.timecode || (isLogged() ? sync.getTimecodeCached(card.tmdb_id, card.type, season, ep.episode) : null);
          out.push({
            episode: ep.episode,
            name: ep.name,
            still: ep.still,
            position_sec: tc ? tc.position_sec : 0,
            duration_sec: tc && tc.duration_sec > 0 ? tc.duration_sec : ep.runtime_minutes ? ep.runtime_minutes * 60 : 0,
          });
        }
        return out;
      }

      function renderSeason(n: number): void {
        currentSeason = n;
        if (!epCache[n]) showListLoading();
        ensureSeason(n).then(
          function (eps) {
            if (closed || currentSeason !== n) return;
            renderEpisodes(eps);
          },
          function () {
            if (closed || currentSeason !== n) return;
            showListMessage(t('error.load'));
          }
        );
      }

      function playEpisode(episode: number): void {
        if (busy || !currentSource) return;
        busy = true;
        showOverlay(t('sources.resolving'));
        resolveRetry(
          resolveParamsFor(episode, currentVoice),
          2,
          function (res) {
            busy = false;
            hideOverlay();
            if (!res || !res.streams || !res.streams.length) {
              toast({ kind: 'error', title: t('sources.resolve_empty'), text: t('sources.resolve_empty_hint') });
              return;
            }
            currentEpisode = episode;
            if (res.voices && res.voices.length) voices = res.voices;
            if (res.voice) currentVoice = res.voice;
            refreshLabels();
            openMedia(toMedia(res, currentVoice), episode);
          },
          function () {
            busy = false;
            hideOverlay();
            toast({ kind: 'error', title: t('sources.resolve_failed'), text: t('toast.pick_other_source') });
          }
        );
      }

      // Populate the voice dropdown (and, for series, refresh) by resolving the
      // current episode in the background — doesn't disturb the visible list.
      function probeVoices(): void {
        if (!currentSource) return;
        resolveRetry(
          resolveParamsFor(isSeries ? currentEpisode : null, currentVoice),
          2,
          function (res) {
            if (closed || !res) return;
            if (res.voices && res.voices.length) {
              voices = res.voices;
              refreshLabels();
            }
          },
          function () {
            /* voices stay empty (auto) */
          }
        );
      }

      function onSourceReady(): void {
        if (autoPlay) {
          const target = autoPlay;
          autoPlay = null;
          if (isSeries) renderSeason(currentSeason); // the list behind the player stays correct
          autoResolve(target, 0);
          return;
        }
        if (isSeries) {
          renderSeason(currentSeason);
          probeVoices();
        } else {
          probeMovie();
        }
      }

      // "Continue" autoplay. The first version showed the list's own "loading
      // sources" spinner AND the resolve overlay stacked, with no text and no way
      // out — a cold lampac (three source retries) plus a 10s upstream timeout on
      // the first balancer read as an infinite hang. Now: ONE overlay with a
      // counter over a hidden sheet; the remembered source first, then the next
      // ones in list order up to AUTO_MAX_SOURCES; when all fail the sheet appears
      // for a manual pick. Back cancels (cancelBusy) and reveals the sheet.
      const AUTO_MAX_SOURCES = 3;
      let autoActive = false;
      let autoQueue: OnlineSource[] = [];
      function revealModal(): void {
        autoActive = false;
        box.classList.remove('hide');
      }
      function autoResolve(target: { season: number | null; episode: number | null }, attempt: number): void {
        if (attempt === 0) {
          autoActive = true;
          autoQueue = [];
          if (currentSource) autoQueue.push(currentSource);
          for (let i = 0; i < sources.length && autoQueue.length < AUTO_MAX_SOURCES; i++) {
            if (!currentSource || sources[i].balanser !== currentSource.balanser) autoQueue.push(sources[i]);
          }
          toggleMode('watch_list'); // its Back handler is the one that cancels a busy resolve
        }
        if (attempt >= autoQueue.length) {
          autoGiveUp();
          return;
        }
        currentSource = autoQueue[attempt];
        if (attempt > 0) {
          // A voice id belongs to the source it came from.
          voices = [];
          currentVoice = null;
          movieMedia = null;
        }
        refreshLabels();
        busy = true;
        sourceToken++; // this attempt's token; an older in-flight resolve bails
        showOverlay(
          t('sources.auto_resolving', {
            name: currentSource.name || currentSource.balanser,
            n: String(attempt + 1),
            total: String(autoQueue.length),
          })
        );
        const episode = isSeries ? (target.episode != null ? target.episode : 1) : null;
        resolveRetry(
          resolveParamsFor(episode, currentVoice),
          2,
          function (res) {
            if (!autoActive) return; // cancelled meanwhile
            if (!res || !res.streams || !res.streams.length) {
              autoResolve(target, attempt + 1);
              return;
            }
            busy = false;
            hideOverlay();
            revealModal(); // visible again under the player, for Back
            if (res.voices && res.voices.length) voices = res.voices;
            refreshLabels();
            if (isSeries) {
              currentEpisode = episode as number;
              openMedia(toMedia(res, currentVoice), episode);
            } else {
              movieMedia = toMedia(res, currentVoice);
              renderStreams(movieMedia);
              playMovie(0);
            }
          },
          function () {
            if (!autoActive) return;
            autoResolve(target, attempt + 1);
          }
        );
      }
      function autoGiveUp(): void {
        busy = false;
        hideOverlay();
        revealModal();
        toast({ kind: 'warning', title: t('sources.auto_failed'), text: t('toast.pick_other_source') });
        if (sources.length) currentSource = sources[0];
        voices = [];
        currentVoice = null;
        movieMedia = null;
        refreshLabels();
        if (isSeries) probeVoices();
        else probeMovie();
      }

      // ---- source list load ----
      // Sources are native providers: an empty list is a real "not in any
      // catalog", so no retry loop (the lampac-era cold-start retry is gone).
      function loadSources(): void {
        showListLoading();
        fSource.value.textContent = t('common.loading');
        getOnlineSources({
          tmdb_id: card.tmdb_id,
          type: card.type,
          title: card.title,
          original_title: card.original_title,
          year: card.year != null ? card.year : undefined,
          imdb_id: card.external_ids ? card.external_ids.imdb_id : undefined,
          season: isSeries ? currentSeason : undefined,
          episode: isSeries ? currentEpisode : undefined,
        }).then(
          function (res) {
            if (closed) return;
            sources = res && res.sources ? res.sources : [];
            if (!sources.length) {
              fSource.value.textContent = t('sources.empty');
              showListMessage(res && res.degraded ? t('sources.degraded') : t('sources.empty'), function () {
                loaded = false;
                loadSources();
              });
              return;
            }
            currentSource = sources[0];
            if (remembered && remembered.balanser) {
              for (let i = 0; i < sources.length; i++) {
                if (sources[i].balanser === remembered.balanser) currentSource = sources[i];
              }
            }
            refreshLabels();
            onSourceReady();
          },
          function () {
            if (closed) return;
            fSource.value.textContent = t('error.load');
            showListMessage(t('error.load'), function () {
              loaded = false;
              loadSources();
            });
          }
        );
      }

      // ---- dropdown wiring ----
      on(fSource.el, 'hover:enter', function () {
        if (!sources.length) return;
        const opts: Array<{ label: string; value: string }> = [];
        for (let i = 0; i < sources.length; i++) {
          opts.push({ label: sources[i].name || sources[i].balanser, value: sources[i].balanser });
        }
        openChoice(t('online.source'), opts, currentSource ? currentSource.balanser : '', function (v) {
          let picked: OnlineSource | null = null;
          for (let i = 0; i < sources.length; i++) {
            if (sources[i].balanser === v) picked = sources[i];
          }
          if (!picked || (currentSource && picked.balanser === currentSource.balanser)) return;
          currentSource = picked;
          sourceToken++; // invalidate any in-flight resolve of the previous source
          busy = false; // that in-flight resolve bails before clearing these — do it here (SRC-1)
          hideOverlay();
          voices = [];
          currentVoice = null;
          movieMedia = null;
          refreshLabels();
          onSourceReady();
        });
      });

      if (fSeason) {
        on(fSeason.el, 'hover:enter', function () {
          const opts: Array<{ label: string; value: string }> = [];
          for (let i = 0; i < seasons.length; i++) {
            opts.push({ label: seasons[i].name || t('title.season') + ' ' + seasons[i].season, value: String(seasons[i].season) });
          }
          openChoice(t('online.season'), opts, String(currentSeason), function (v) {
            const n = parseInt(v, 10);
            if (n === currentSeason) return;
            currentSeason = n; // set BEFORE refreshLabels so the dropdown shows the new season, not the old
            currentEpisode = 1;
            sourceToken++; // invalidate an in-flight resolve of the OLD season (would save timecode under the new one) (SRC-1)
            busy = false;
            hideOverlay();
            refreshLabels();
            renderSeason(n);
            probeVoices();
          });
        });
      }

      on(fVoice.el, 'hover:enter', function () {
        if (!voices.length) {
          toast({ kind: 'warning', text: t('online.voice_none') });
          return;
        }
        const opts: Array<{ label: string; value: string }> = [{ label: t('online.voice_auto'), value: '' }];
        for (let i = 0; i < voices.length; i++) {
          opts.push({ label: voices[i].name, value: voices[i].id });
        }
        openChoice(t('online.voice'), opts, currentVoice || '', function (v) {
          currentVoice = v || null;
          refreshLabels();
          if (!isSeries) probeMovie();
        });
      });

      // ---- open trigger ----
      if (!loaded) {
        loaded = true;
        refreshLabels();
        loadSources();
      }
    } else {
      // ================= TORRENTS =================
      // Torrent resume key: series → (first season, ep 1); movie → (null, null).
      const torSeason = isSeries ? seasons[0].season : null;
      const torEpisode = isSeries ? 1 : null;

      let trackerFilter = '';
      let qualityFilter = '';
      let torrentsCache: Torrent[] | null = null;

      const fTracker = makeFilter('online.source', t('common.all'));
      const fQuality = makeFilter('online.quality', t('common.all'));
      filters.appendChild(fTracker.el);
      filters.appendChild(fQuality.el);

      // Distinct trackers / qualities present in the loaded torrent list, each
      // prefixed with an "all" reset option.
      function trackerOptions(): Array<{ label: string; value: string }> {
        const opts: Array<{ label: string; value: string }> = [{ label: t('common.all'), value: '' }];
        const seen: { [k: string]: boolean } = {};
        const list = torrentsCache || [];
        for (let i = 0; i < list.length; i++) {
          const tr = list[i].tracker || '';
          if (tr && !seen[tr]) {
            seen[tr] = true;
            opts.push({ label: tr, value: tr });
          }
        }
        return opts;
      }

      function qualityOptions(): Array<{ label: string; value: string }> {
        const opts: Array<{ label: string; value: string }> = [{ label: t('common.all'), value: '' }];
        const seen: { [k: string]: boolean } = {};
        const list = torrentsCache || [];
        for (let i = 0; i < list.length; i++) {
          const q = list[i].quality || parseMeta(list[i].title).quality;
          if (q && !seen[q]) {
            seen[q] = true;
            opts.push({ label: q, value: q });
          }
        }
        return opts;
      }

      function refreshFilterLabels(): void {
        fTracker.value.textContent = trackerFilter || t('common.all');
        fQuality.value.textContent = qualityFilter || t('common.all');
      }

      on(fTracker.el, 'hover:enter', function () {
        openChoice(t('online.source'), trackerOptions(), trackerFilter, function (v) {
          if (v === trackerFilter) return;
          trackerFilter = v;
          refreshFilterLabels();
          renderTorrentList(torrentsCache || [], true);
        });
      });
      on(fQuality.el, 'hover:enter', function () {
        openChoice(t('online.quality'), qualityOptions(), qualityFilter, function (v) {
          if (v === qualityFilter) return;
          qualityFilter = v;
          refreshFilterLabels();
          renderTorrentList(torrentsCache || [], true);
        });
      });

      function showTorrents(enter: boolean): void {
        if (torrentsCache) {
          renderTorrentList(torrentsCache, enter);
          return;
        }
        loadTorrents(enter);
      }

      function loadTorrents(enter: boolean): void {
        showListLoading();
        getTorrents({
          tmdb_id: card.tmdb_id,
          type: card.type,
          title: card.title,
          original_title: card.original_title,
          year: card.year != null ? card.year : undefined,
          // Don't send season: it's hardcoded to season 1, and the backend drops
          // any pack whose declared seasons don't include it → later-season packs
          // vanished. Show all packs; a proper season picker is a later phase.
          // (torSeason/torEpisode still key the resume slot below.)
        }).then(
          function (res) {
            if (closed) return;
            const list = res && res.torrents ? res.torrents : [];
            torrentsCache = list;
            if (!list.length) {
              showListMessage(t('sources.torrents_empty'));
              return;
            }
            renderTorrentList(list, enter);
          },
          function () {
            if (closed) return;
            showListMessage(t('error.load'), function () {
              loadTorrents(true);
            });
          }
        );
      }

      // Does a torrent pass the current tracker/quality dropdown filters?
      function torrentPasses(tor: Torrent): boolean {
        if (trackerFilter && (tor.tracker || '') !== trackerFilter) return false;
        if (qualityFilter) {
          const q = tor.quality || parseMeta(tor.title).quality;
          if (q !== qualityFilter) return false;
        }
        return true;
      }

      // Each torrent renders as a Lampa-style "folder" card.
      function renderTorrentList(list: Torrent[], enter: boolean): void {
        empty(listBody);
        lastRow = false;
        refreshFilterLabels();
        listBack = function () {
          toggleMode('watch_filters');
        };

        let shown = 0;
        for (let i = 0; i < list.length; i++) {
          (function (tor: Torrent) {
            if (!torrentPasses(tor)) return;
            shown++;

            const row = el('div', 'torrent-folder selector');
            const icon = el('div', 'torrent-folder__icon');
            icon.appendChild(iconEl(ICON_FOLDER, 'torrent-folder__icon-svg'));
            row.appendChild(icon);

            const main = el('div', 'torrent-folder__body');
            const primary = tor.voices && tor.voices.length ? tor.voices.join(', ') : tor.title || '';
            main.appendChild(el('div', 'torrent-folder__title', primary));

            const parsed = parseMeta(tor.title);
            const quality = tor.quality || parsed.quality;
            const parts: string[] = [];
            const sizeText = tor.size_human || fmtBytes(tor.size);
            if (sizeText) parts.push(sizeText);
            if (parsed.hdr) parts.push(parsed.hdr);
            if (parsed.codec) parts.push(parsed.codec);
            if (parsed.dv) parts.push(parsed.dv);

            const meta = el('div', 'torrent-folder__meta');
            if (quality) meta.appendChild(qBadge(quality));
            if (parts.length) meta.appendChild(el('span', 'torrent-folder__metatext', parts.join(' / ')));
            const seeds = el('span', 'torrent-badge ' + seedClass(tor.seeders));
            seeds.appendChild(el('span', 'torrent-badge__dot', ''));
            seeds.appendChild(el('span', undefined, String(tor.seeders || 0)));
            meta.appendChild(seeds);
            main.appendChild(meta);

            row.appendChild(main);

            on(row, 'hover:focus', function () {
              lastRow = row;
              listScroll.update(row);
            });
            on(row, 'hover:enter', function () {
              selectTorrent(tor);
            });
            listBody.appendChild(row);
          })(list[i]);
        }

        if (!shown) {
          listBody.appendChild(buildState({ kind: 'empty', text: t('sources.torrents_empty') }));
        }

        listScroll.reset();
        if (enter && shown) toggleMode('watch_list'); // don't drop focus into an empty (filtered-to-zero) list
      }

      // Add the chosen torrent, then play (single video) or show a file picker.
      function selectTorrent(tor: Torrent): void {
        if (busy) return;
        busy = true;
        showOverlay(t('sources.torrents_adding'));

        addTorrent(tor.id).then(
          function (res) {
            if (closed) return;
            busy = false;
            hideOverlay();
            if (!res || !res.infohash) {
              toast({ kind: 'error', title: t('sources.torrents_add_failed'), text: t('toast.try_again') });
              return;
            }
            const files = res.files || [];
            const videos: TorrentFile[] = [];
            for (let i = 0; i < files.length; i++) {
              if (files[i].is_video) videos.push(files[i]);
            }
            const playable = videos.length ? videos : files;
            if (!playable.length) {
              toast({ kind: 'warning', title: t('sources.torrents_no_video'), text: t('sources.torrents_no_video_hint') });
              return;
            }
            if (playable.length === 1) {
              playTorrentFile(res.infohash, playable[0], tor.title, false, undefined, tor.id);
            } else {
              renderFileList(res.infohash, playable, tor.title, tor.id);
            }
          },
          function () {
            if (closed) return;
            busy = false;
            hideOverlay();
            toast({ kind: 'error', title: t('sources.torrents_add_failed'), text: t('toast.try_again') });
          }
        );
      }

      // File picker for multi-file packs (series / collections). Back returns to
      // the torrent list.
      function renderFileList(infohash: string, files: TorrentFile[], torrentTitle: string, torId?: string): void {
        empty(listBody);
        lastRow = false;
        listBack = function () {
          renderTorrentList(torrentsCache || [], true);
        };

        listBody.appendChild(el('div', 'torrent-files__hint', t('torrent.select_file')));

        for (let i = 0; i < files.length; i++) {
          (function (file: TorrentFile) {
            const row = el('div', 'torrent-file selector');
            row.appendChild(el('div', 'torrent-file__name', file.name || 'file ' + file.index));
            const size = fmtBytes(file.size);
            if (size) row.appendChild(el('div', 'torrent-file__size', size));
            on(row, 'hover:focus', function () {
              lastRow = row;
              listScroll.update(row);
            });
            on(row, 'hover:enter', function () {
              playTorrentFile(infohash, file, torrentTitle, true, files, torId);
            });
            listBody.appendChild(row);
          })(files[i]);
        }

        listScroll.reset();
        toggleMode('watch_list');
      }

      // Open the player on a torrent file's /stream URL. The backend serves it
      // progressively (type mp4) UNLESS device caps force a server-side remux/
      // transcode to HLS: an .mkv on a webview that can't demux MKV (mkv=false →
      // copy_mkv), or an HEVC/AV1 release on a TV with no such decoder
      // (hevc=false → transcode). In those cases /stream 302-redirects to an HLS
      // playlist, so the player must use its HLS engine — a progressive <video>
      // just polls the manifest forever without ever loading a segment.
      function playTorrentFile(infohash: string, file: TorrentFile, torrentTitle: string, isPack: boolean, files?: TorrentFile[], torId?: string): void {
        if (torId) writeLastTorrent(torId, file.index, torrentTitle); // "Continue" reopens this file
        const built = torrentMedia(infohash, file, torrentTitle);
        const media = built.media;
        const willBeHls = built.hls;

        // Timecode slot. A series pack keeps every episode in one torrent, so the
        // slot must come from the FILE (SxxEyy in its name; index as a last resort),
        // not from the title — otherwise all episodes shared slot E1.
        function slotOf(f: TorrentFile): { season: number | null; episode: number | null } {
          const parsed = isSeries ? parseEpisode(f.name || '') : null;
          return {
            season: parsed && parsed.season != null ? parsed.season : torSeason,
            episode: !isSeries ? null : parsed ? parsed.episode : isPack ? f.index : torEpisode,
          };
        }
        let cur = file;
        const slot = slotOf(file);
        let season = slot.season;
        let episode = slot.episode;

        // Pack episodes in playback order (video files, by parsed episode then
        // index) — feeds next/prev and the in-player list.
        const packEps: TorrentFile[] = [];
        if (isSeries && isPack && files) {
          for (let i = 0; i < files.length; i++) {
            if (files[i].is_video === false) continue;
            const e = slotOf(files[i]).episode;
            if (e != null && e > 0) packEps.push(files[i]);
          }
          packEps.sort(function (a, b) {
            const ea = slotOf(a).episode as number;
            const eb = slotOf(b).episode as number;
            return ea !== eb ? ea - eb : a.index - b.index;
          });
        }
        function packIndex(f: TorrentFile): number {
          for (let i = 0; i < packEps.length; i++) if (packEps[i].index === f.index) return i;
          return -1;
        }
        function switchFile(target: TorrentFile | undefined, done: (m: PlayerMedia | null, meta?: import('../core/player').EpisodeMeta) => void): void {
          if (!target) {
            done(null);
            return;
          }
          const b = torrentMedia(infohash, target, torrentTitle);
          const s2 = slotOf(target);
          cur = target;
          season = s2.season;
          episode = s2.episode;
          const tc2 = isLogged() ? sync.getTimecodeCached(card.tmdb_id, card.type, season, episode) : null;
          done(b.media, {
            season: season,
            episode: episode,
            title: card.title,
            subtitle: target.name || torrentTitle || '',
            resume: tc2 ? { position_sec: tc2.position_sec, duration_sec: tc2.duration_sec } : null,
          });
        }
        const hasPack = packEps.length > 1 && packIndex(file) >= 0;

        const tc = isLogged() ? sync.getTimecodeCached(card.tmdb_id, card.type, season, episode) : null;

        openPlayer({
          title: card.title,
          subtitle: cur.name || torrentTitle || '',
          poster: card.poster,
          media: media,
          tmdb_id: card.tmdb_id,
          media_type: card.type,
          imdb_id: card.external_ids ? card.external_ids.imdb_id : undefined,
          season: season,
          episode: episode,
          // Past the pack's last file (or a single-file torrent): the watch queue.
          get onNext() {
            if (hasPack)
              return function (done: NextDone) {
                const nx = packEps[packIndex(cur) + 1];
                if (!nx && playQueueHead()) return;
                switchFile(nx, done);
              };
            return sync.queueLength() ? queueNext : undefined;
          },
          onPrev: hasPack
            ? function (done) {
                switchFile(packEps[packIndex(cur) - 1], done);
              }
            : undefined,
          onEpisodes: hasPack
            ? function (done) {
                const list: import('../core/player').PlayerEpisode[] = [];
                for (let i = 0; i < packEps.length; i++) {
                  const s3 = slotOf(packEps[i]);
                  const t3 = isLogged() ? sync.getTimecodeCached(card.tmdb_id, card.type, s3.season, s3.episode) : null;
                  list.push({
                    episode: s3.episode as number,
                    name: packEps[i].name,
                    position_sec: t3 ? t3.position_sec : 0,
                    duration_sec: t3 ? t3.duration_sec : 0,
                  });
                }
                done(list, episode as number);
              }
            : undefined,
          onEpisode: hasPack
            ? function (n, done) {
                let target: TorrentFile | undefined;
                for (let i = 0; i < packEps.length; i++) if (slotOf(packEps[i]).episode === n) target = packEps[i];
                switchFile(target, done);
              }
            : undefined,
          // Server-remuxed/transcoded HLS grows as it's built and has no real
          // total in the manifest — seed the seekbar from the TMDB runtime so it
          // doesn't creep from 10s→16s→…. Only for the HLS (remux) path.
          durationHint: willBeHls && card.runtime_minutes ? card.runtime_minutes * 60 : undefined,
          resume: tc ? { position_sec: tc.position_sec, duration_sec: tc.duration_sec } : null,
          onProgress: function (pos, dur) {
            if (isLogged()) sync.saveTimecode(card.tmdb_id, card.type, season, episode, pos, dur);
          },
          // HLS-remux torrents can carry several dubs; expose them so the player
          // offers a track menu (switching re-muxes the picked track inline —
          // hls.js alternate-audio doesn't play on the TV target).
          loadAudioTracks: willBeHls
            ? function (done) {
                getTorrentAudio(infohash, cur.index).then(
                  function (r) {
                    done((r && r.tracks) || []);
                  },
                  function () {
                    done([]);
                  }
                );
              }
            : undefined,
        });
      }

      // ---- open trigger ----
      // "Continue" on a title last played from a torrent: re-add that torrent
      // and play the same file straight away (its saved position resumes in
      // the player). Anything missing → the normal torrent list.
      const lastTor = auto ? readLastSrc() : null;
      if (lastTor && lastTor.balanser === 'torrent' && lastTor.torrent) {
        busy = true;
        showOverlay(t('sources.torrents_adding'));
        addTorrent(lastTor.torrent).then(
          function (res) {
            if (closed) return;
            busy = false;
            hideOverlay();
            const files = (res && res.files) || [];
            let target: TorrentFile | undefined;
            for (let i = 0; i < files.length; i++) if (files[i].index === lastTor.file) target = files[i];
            if (!res || !res.infohash || !target) {
              showTorrents(true);
              return;
            }
            const videos: TorrentFile[] = [];
            for (let i = 0; i < files.length; i++) if (files[i].is_video !== false) videos.push(files[i]);
            playTorrentFile(res.infohash, target, lastTor.title || card.title, videos.length > 1, videos, lastTor.torrent);
          },
          function () {
            if (closed) return;
            busy = false;
            hideOverlay();
            showTorrents(true);
          }
        );
      } else {
        showTorrents(true);
      }
    }

    toggleMode('watch_filters');
  }

  function load(): void {
    // Cached: the title screen reads timecodes from sync, and season episodes
    // via getTitleSeason, so a 5-minute-old card is not stale for it.
    getTitleCached(params.type, params.id).then(
      function (card) {
        if (destroyed) return; // Back before load finished — don't grab focus
        if (card && card.tmdb_id) {
          render(card);
          // Library's continue lane: go straight on playing (title stays under
          // the player for Back). Falls back to the plain title when there is
          // nothing to continue.
          if (params.resume && continueBtn) trigger(continueBtn, 'hover:enter');
          else if (params.episode != null && openEpisode) openEpisode(params.season != null ? params.season : 1, params.episode);
          else if (params.autoplay && openEpisode) openEpisode(null, null);
        } else showError();
      },
      function () {
        if (destroyed) return;
        showError();
      }
    );
  }

  load();

  return {
    destroy: function () {
      destroyed = true;
      head.destroy();
      if (unsubBookmarks) unsubBookmarks();
    },
    resume: function () {
      if (resumeHook) resumeHook();
      Controller.toggle(lastMode);
    },
  };
}
