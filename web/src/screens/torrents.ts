// "Мої торренти" — active-torrents screen (menu-level). Lists the torrents
// currently seeding on the server (GET /torrents/active): name, size, a
// download-progress bar and a "cached" badge. Enter plays the torrent (picks
// the single video file, or shows a file picker for packs); a per-row delete
// button removes it (DELETE /torrents/{infohash}).
//
// Layout mirrors the bookmarks screen: shared left menu rail + a vertical
// list. Each row carries a play cell and a delete cell so the geometry
// navigator moves left/right between them and up/down between rows.
//
// ES5 target: plain functions, no async/await/for-of/spread/find/includes.

import Controller, { on } from '../core/controller';
import { Scroll } from '../core/scroll';
import { t } from '../core/i18n';
import { ScreenInstance } from '../core/activity';
import { getActiveTorrents, deleteTorrent, ActiveTorrent, TorrentFile } from '../core/api';
import { el, empty, fmtBytes } from '../ui/dom';
import { buildMenu, Menu } from '../ui/menu';
import { buildHead, Head } from '../ui/head';
import { buildFooter } from '../ui/shell';
import { buildState } from '../ui/state';
import { toast } from '../ui/toast';
import { openConfirm } from '../ui/confirm';
import { openMenu } from './nav';
import { openPlayer } from '../core/player';
import { torrentMedia } from '../core/torrentPlay';


export function mountTorrents(container: HTMLElement): ScreenInstance {
  container.className += ' torrents-screen';

  const menu: Menu = buildMenu('library', 'content');
  container.appendChild(menu.el);

  const head: Head = buildHead();
  container.appendChild(head.el);
  container.appendChild(buildFooter());

  const wrap = el('div', 'torrents-wrap');
  const scroll = new Scroll({ mask: true, over: true });
  wrap.appendChild(scroll.render());
  container.appendChild(wrap);
  const body = scroll.body();
  body.classList.add('torrents-body');

  let lastRow: HTMLElement | false = false;
  let destroyed = false;
  let cache: ActiveTorrent[] = [];

  // Back handler for the content mode — default returns to the menu rail; the
  // file picker overrides it to return to the list.
  let contentBack: () => void = function () {
    Controller.toggle('menu');
  };

  const contentController = {
    toggle: function () {
      Controller.collectionSet(body);
      Controller.collectionFocus(lastRow || false, body);
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
      Controller.moveOr('up');
    },
    down: function () {
      Controller.moveOr('down');
    },
    back: function () {
      contentBack();
    },
  };
  Controller.add('content', contentController);

  function showState(kind: 'empty' | 'error', text: string, cta: boolean): void {
    empty(body);
    lastRow = false;
    contentBack = function () {
      Controller.toggle('menu');
    };
    const box = cta
      ? buildState({
          kind: kind,
          text: text,
          actionLabel: t('bookmarks.to_catalog'),
          onRetry: function () {
            openMenu('catalog');
          },
        })
      : buildState({ kind: kind, text: text });
    body.appendChild(box);
  }

  let lastKey = '';
  function showSpinner(): void {
    empty(body);
    lastRow = false;
    body.appendChild(buildState({ kind: 'loading' }));
  }

  function renderList(list: ActiveTorrent[], focus: boolean): void {
    if (!list.length) {
      showState('empty', t('torrents.empty'), true);
      if (focus) Controller.toggle('content');
      return;
    }
    empty(body);
    lastRow = false;
    contentBack = function () {
      Controller.toggle('menu');
    };

    for (let i = 0; i < list.length; i++) {
      (function (tor: ActiveTorrent) {
        const row = el('div', 'torrent-item');

        const main = el('div', 'torrent-item__main selector');
        main.setAttribute('data-key', tor.infohash);
        main.appendChild(el('div', 'torrent-item__name', tor.name || tor.title || tor.infohash));

        const meta = el('div', 'torrent-item__meta');
        const sizeText = tor.size_human || fmtBytes(tor.size);
        if (sizeText) meta.appendChild(el('span', 'torrent-item__size', sizeText));
        if (tor.cached) meta.appendChild(el('span', 'torrent-badge torrent-badge--cached', t('torrents.cached')));
        else if (tor.progress != null) {
          meta.appendChild(
            el('span', 'torrent-item__pct', Math.round(Math.max(0, Math.min(1, tor.progress)) * 100) + '%')
          );
        }
        main.appendChild(meta);

        if (!tor.cached && tor.progress != null) {
          const track = el('div', 'torrent-progress');
          const fill = el('div', 'torrent-progress__fill');
          fill.style.width = Math.round(Math.max(0, Math.min(1, tor.progress)) * 100) + '%';
          track.appendChild(fill);
          main.appendChild(track);
        }

        on(main, 'hover:focus', function () {
          lastRow = main;
          lastKey = tor.infohash;
        });
        on(main, 'hover:enter', function () {
          play(tor);
        });
        row.appendChild(main);

        const del = el('div', 'torrent-item__del selector', t('torrents.delete'));
        del.setAttribute('data-key', tor.infohash + ':del');
        on(del, 'hover:focus', function () {
          lastRow = del;
          lastKey = tor.infohash + ':del';
        });
        on(del, 'hover:enter', function () {
          askRemove(tor, row);
        });
        row.appendChild(del);

        body.appendChild(row);
      })(list[i]);
    }

    // The list is rebuilt on every resume() (Back from the player/files), which
    // used to drop focus to row #1. Find the row we were on by key.
    if (lastKey) {
      const keep = body.querySelector('[data-key="' + lastKey + '"]') as HTMLElement | null;
      if (keep) lastRow = keep;
    }
    scroll.reset();
    if (focus) Controller.toggle('content');
  }

  // Deleting a torrent drops its cached data from disk — not cheaply reversible,
  // so it gets the same confirm as playlist-delete / device-revoke.
  function askRemove(tor: ActiveTorrent, row: HTMLElement): void {
    openConfirm(container, {
      text: t('torrents.delete_confirm', { name: tor.name || '' }),
      yesLabel: t('torrents.delete'),
      mode: 'torrent_confirm',
      onYes: function () {
        removeTorrent(tor, row);
      },
    });
  }

  function removeTorrent(tor: ActiveTorrent, row: HTMLElement): void {
    // Optimistic: drop the row immediately, re-focus the neighbour that took
    // its place (next row, else previous) instead of bouncing to row #1.
    const sibling = (row.nextElementSibling || row.previousElementSibling) as HTMLElement | null;
    const neighbour = sibling ? (sibling.querySelector('.torrent-item__main') as HTMLElement | null) : null;
    if (row.parentNode) row.parentNode.removeChild(row);
    const next: ActiveTorrent[] = [];
    for (let i = 0; i < cache.length; i++) {
      if (cache[i].infohash !== tor.infohash) next.push(cache[i]);
    }
    cache = next;
    if (!cache.length) {
      showState('empty', t('torrents.empty'), true);
      Controller.toggle('content');
    } else {
      lastRow = neighbour || false;
      Controller.toggle('content');
    }
    deleteTorrent(tor.infohash).then(
      function () {
        /* gone */
      },
      function () {
        toast(t('torrents.delete_failed'));
      }
    );
  }

  // Play an active torrent. Uses the file list if the backend supplied one
  // (pick the single video / show a picker), else best-effort file index 0.
  function play(tor: ActiveTorrent): void {
    const files = tor.files || [];
    const videos: TorrentFile[] = [];
    for (let i = 0; i < files.length; i++) {
      if (files[i].is_video) videos.push(files[i]);
    }
    const playable = videos.length ? videos : files;
    if (!playable.length) {
      launch(tor.infohash, { index: 0, name: tor.name || tor.title || '' }, tor.name || tor.title || '');
      return;
    }
    if (playable.length === 1) {
      launch(tor.infohash, playable[0], tor.name || tor.title || '');
    } else {
      renderFileList(tor.infohash, playable, tor.name || tor.title || '');
    }
  }

  function renderFileList(infohash: string, files: TorrentFile[], torrentTitle: string): void {
    empty(body);
    lastRow = false;
    contentBack = function () {
      renderList(cache, true);
    };
    body.appendChild(el('div', 'torrent-files__hint', t('torrent.select_file')));

    for (let i = 0; i < files.length; i++) {
      (function (file: TorrentFile) {
        const r = el('div', 'torrent-file selector');
        r.appendChild(el('div', 'torrent-file__name', file.name || 'file ' + file.index));
        const size = fmtBytes(file.size);
        if (size) r.appendChild(el('div', 'torrent-file__size', size));
        on(r, 'hover:focus', function () {
          lastRow = r;
        });
        on(r, 'hover:enter', function () {
          launch(infohash, file, torrentTitle);
        });
        body.appendChild(r);
      })(files[i]);
    }
    scroll.reset();
    Controller.toggle('content');
  }

  // No tmdb context here (active torrents aren't tied to a catalog title), so
  // resume/timecodes are skipped — just play the progressive /stream URL.
  function launch(infohash: string, file: TorrentFile, torrentTitle: string): void {
    // Same capability-aware builder as the title screen: an .mkv/HEVC file
    // played as raw progressive mp4 was a black screen on Tizen.
    openPlayer({ title: torrentTitle, media: torrentMedia(infohash, file, torrentTitle).media });
  }

  function load(focus: boolean): void {
    showSpinner();
    getActiveTorrents().then(
      function (res) {
        if (destroyed) return;
        cache = res && res.torrents ? res.torrents : [];
        renderList(cache, focus);
      },
      function () {
        if (destroyed) return;
        showState('error', t('error.load'), false);
        if (focus) Controller.toggle('content');
      }
    );
  }

  load(false);
  Controller.toggle('menu');

  return {
    destroy: function () {
      destroyed = true;
      head.destroy();
    },
    resume: function () {
      menu.activate();
      load(true);
    },
  };
}
