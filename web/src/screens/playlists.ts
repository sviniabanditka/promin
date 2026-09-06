// Playlists (docs/api.md). Two screens live here:
//   - mountPlaylists: menu-level list of the user's playlists behind the left
//     menu rail. Create (on-screen keyboard for the name), rename, delete, and
//     open. Each row is [open | rename | delete] so left/right move between the
//     actions and up/down between rows.
//   - mountPlaylistItems: pushed grid/list of one playlist's items. Items come
//     back as bare {tmdb_id, media_type} (docs/api.md), so each row is enriched via
//     getTitle for a poster/title. OK opens the title screen; a per-row action
//     removes the item.
//
// ES5 target (swc): plain functions, no async/await/for-of/spread/find/includes.

import Controller, { on } from '../core/controller';
import { Scroll } from '../core/scroll';
import { t } from '../core/i18n';
import * as router from '../core/router';
import { ScreenInstance } from '../core/activity';
import {
  getPlaylists,
  createPlaylist,
  renamePlaylist,
  deletePlaylist,
  getPlaylistItems,
  removePlaylistItem,
  getTitle,
  Playlist,
  PlaylistItem,
} from '../core/api';
import { el, empty } from '../ui/dom';
import { buildMenu, Menu } from '../ui/menu';
import { buildHead, Head } from '../ui/head';
import { buildFooter } from '../ui/shell';
import { buildKeyboard } from '../ui/keyboard';
import { buildState } from '../ui/state';
import { toast } from '../ui/toast';
import { openConfirm } from '../ui/confirm';
import { openTitle, openPlaylist } from './nav';

function trim(s: string): string {
  return (s || '').replace(/^\s+|\s+$/g, '');
}

// ---- name-entry modal (shared by create + rename) ----------------------
//
// An overlay with an on-screen keyboard (ui/keyboard) plus a Save button. The
// keyboard owns the value; onRightEdge / Up jump to Save; Back on an empty
// value (or Back on Save) closes without saving. Returns to `returnMode`.
export function openNameModal(
  container: HTMLElement,
  titleText: string,
  initialValue: string,
  returnMode: string,
  onSubmit: (name: string) => void
): void {
  const overlay = el('div', 'settings-modal');
  const box = el('div', 'pl-name__box');
  box.appendChild(el('div', 'pl-name__title', titleText));
  const valueDisp = el('div', 'pl-name__value', initialValue || '');
  box.appendChild(valueDisp);

  let value = initialValue || '';

  function close(): void {
    if (overlay.parentNode) overlay.parentNode.removeChild(overlay);
    Controller.toggle(returnMode);
  }

  const keyboard = buildKeyboard({
    controllerName: 'pl_kb',
    initialValue: initialValue,
    onChange: function (v: string) {
      value = v;
      valueDisp.textContent = v || '';
    },
    onRightEdge: function () {
      Controller.toggle('pl_kb_form');
    },
    onBackEmpty: function () {
      close();
    },
  });
  box.appendChild(keyboard.el);

  const actions = el('div', 'pl-name__actions');
  const save = el('div', 'button button--accent selector', t('playlists.save'));
  let submitted = false;
  on(save, 'hover:enter', function () {
    const name = trim(value);
    if (!name || submitted) return; // double OK created two playlists
    submitted = true;
    if (overlay.parentNode) overlay.parentNode.removeChild(overlay);
    onSubmit(name);
  });
  actions.appendChild(save);
  box.appendChild(actions);

  overlay.appendChild(box);
  container.appendChild(overlay);

  Controller.add('pl_kb_form', {
    toggle: function () {
      Controller.collectionSet(actions);
      Controller.collectionFocus(save, actions);
    },
    left: function () {
      Controller.toggle('pl_kb');
    },
    up: function () {
      Controller.toggle('pl_kb');
    },
    back: function () {
      close();
    },
  });

  keyboard.register();
  Controller.toggle('pl_kb');
}

// ======================= playlists list =================================

export function mountPlaylists(container: HTMLElement): ScreenInstance {
  container.className += ' playlists-screen';

  const menu: Menu = buildMenu('library', 'content');
  container.appendChild(menu.el);

  const head: Head = buildHead();
  container.appendChild(head.el);
  container.appendChild(buildFooter());

  const wrap = el('div', 'playlists-wrap');
  const scroll = new Scroll({ mask: true, over: true });
  wrap.appendChild(scroll.render());
  container.appendChild(wrap);
  const body = scroll.body();
  body.classList.add('playlists-body');

  let lastRow: HTMLElement | false = false;
  let destroyed = false;
  let cache: Playlist[] = [];

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
      Controller.toggle('menu');
    },
  };
  Controller.add('content', contentController);

  let lastKey = '';
  function showSpinner(): void {
    empty(body);
    lastRow = false;
    body.appendChild(buildState({ kind: 'loading' }));
  }

  function doCreate(): void {
    openNameModal(container, t('playlists.create'), '', 'content', function (name) {
      createPlaylist(name).then(
        function () {
          load(true);
        },
        function () {
          toast({ kind: 'error', title: t('playlists.create_failed'), text: t('toast.try_again') });
          Controller.toggle('content');
        }
      );
    });
  }

  function doRename(pl: Playlist): void {
    openNameModal(container, t('playlists.rename'), pl.name || '', 'content', function (name) {
      renamePlaylist(pl.id, name).then(
        function () {
          load(true);
        },
        function () {
          toast({ kind: 'error', title: t('playlists.rename_failed'), text: t('toast.try_again') });
          Controller.toggle('content');
        }
      );
    });
  }

  // ---- delete confirm ----
  function askDelete(pl: Playlist): void {
    openConfirm(container, {
      text: t('playlists.delete_confirm', { name: pl.name || '' }),
      yesLabel: t('playlists.delete'),
      mode: 'pl_confirm',
      onYes: function () {
        deletePlaylist(pl.id).then(
          function () {
            load(true);
          },
          function () {
            toast({ kind: 'error', title: t('playlists.delete_failed'), text: t('toast.try_again') });
            Controller.toggle('content');
          }
        );
      },
    });
  }

  function renderList(list: Playlist[]): void {
    empty(body);
    lastRow = false;

    const createBtn = el('div', 'button pl-create selector', t('playlists.create'));
    createBtn.setAttribute('data-key', 'create');
    on(createBtn, 'hover:focus', function () {
      lastRow = createBtn;
      lastKey = 'create';
    });
    on(createBtn, 'hover:enter', doCreate);
    body.appendChild(createBtn);

    if (!list.length) {
      body.appendChild(buildState({ kind: 'empty', text: t('playlists.empty') }));
    }

    for (let i = 0; i < list.length; i++) {
      (function (pl: Playlist) {
        const row = el('div', 'pl-row');

        const main = el('div', 'pl-row__main selector');
        main.setAttribute('data-key', String(pl.id));
        main.appendChild(el('div', 'pl-row__name', pl.name || ''));
        const count = pl.items_count != null ? pl.items_count : 0;
        main.appendChild(el('div', 'pl-row__count', t('playlists.count', { n: String(count) })));
        on(main, 'hover:focus', function () {
          lastRow = main;
          lastKey = String(pl.id);
        });
        on(main, 'hover:enter', function () {
          openPlaylist(pl.id, pl.name || '');
        });
        row.appendChild(main);

        const rename = el('div', 'pl-row__act selector', t('playlists.rename'));
        rename.setAttribute('data-key', pl.id + ':rename');
        on(rename, 'hover:focus', function () {
          lastRow = rename;
          lastKey = pl.id + ':rename';
        });
        on(rename, 'hover:enter', function () {
          doRename(pl);
        });
        row.appendChild(rename);

        const del = el('div', 'pl-row__act pl-row__act--del selector', t('playlists.delete'));
        del.setAttribute('data-key', pl.id + ':del');
        on(del, 'hover:focus', function () {
          lastRow = del;
          lastKey = pl.id + ':del';
        });
        on(del, 'hover:enter', function () {
          askDelete(pl);
        });
        row.appendChild(del);

        body.appendChild(row);
      })(list[i]);
    }

    // Rebuilt on every resume() — find the row we were on by key instead of
    // dropping focus to the first row.
    if (lastKey) {
      const keep = body.querySelector('[data-key="' + lastKey + '"]') as HTMLElement | null;
      if (keep) lastRow = keep;
    }
    scroll.reset();
  }

  function load(focus: boolean): void {
    showSpinner();
    getPlaylists().then(
      function (res) {
        if (destroyed) return;
        cache = res && res.playlists ? res.playlists : [];
        renderList(cache);
        if (focus) Controller.toggle('content');
      },
      function () {
        if (destroyed) return;
        empty(body);
        lastRow = false;
        body.appendChild(
          buildState({ kind: 'error', text: t('error.load'), onRetry: function () { load(true); } })
        );
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
      // Re-register (the 'content' name was overwritten by the pushed
      // playlist-items screen) AND re-focus. load(true) re-renders then
      // toggles 'content', so the Navigator collection points at the fresh
      // rows — without the toggle, arrows hit a dead controller after Back.
      menu.activate();
      Controller.add('content', contentController);
      load(true);
    },
  };
}

// ======================= playlist items =================================

export interface PlaylistItemsParams {
  id: number;
  name: string;
}

export function mountPlaylistItems(container: HTMLElement, params: PlaylistItemsParams): ScreenInstance {
  container.className += ' playlists-screen pl-items-screen';

  const header = el('div', 'devices-header');
  header.appendChild(el('div', 'devices-header__title', params.name || t('playlists.title')));
  container.appendChild(header);

  const wrap = el('div', 'pl-items-wrap');
  const scroll = new Scroll({ mask: true, over: true });
  wrap.appendChild(scroll.render());
  container.appendChild(wrap);
  const body = scroll.body();
  body.classList.add('pl-items-body');

  let lastRow: HTMLElement | false = false;
  let destroyed = false;
  let cache: PlaylistItem[] = [];

  const contentController = {
    toggle: function () {
      Controller.collectionSet(body);
      Controller.collectionFocus(lastRow || false, body);
    },
    left: function () {
      Controller.moveOr('left');
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
      router.back();
    },
  };
  Controller.add('content', contentController);

  function showState(kind: 'empty' | 'error', text: string): void {
    empty(body);
    lastRow = false;
    body.appendChild(buildState({ kind: kind, text: text }));
  }

  function showSpinner(): void {
    empty(body);
    lastRow = false;
    body.appendChild(buildState({ kind: 'loading' }));
  }

  function removeItem(item: PlaylistItem, row: HTMLElement): void {
    // Focus the neighbour that slides up, not row #1.
    const sibling = (row.nextElementSibling || row.previousElementSibling) as HTMLElement | null;
    const neighbour = sibling ? (sibling.querySelector('.pl-item__main') as HTMLElement | null) : null;
    if (row.parentNode) row.parentNode.removeChild(row);
    const next: PlaylistItem[] = [];
    for (let i = 0; i < cache.length; i++) {
      if (cache[i].id !== item.id) next.push(cache[i]);
    }
    cache = next;
    lastRow = neighbour || false;
    // Persist the deletion regardless of whether the list is now empty — the
    // empty-branch used to return before this call, so the last item never got
    // deleted server-side.
    removePlaylistItem(params.id, item.id).then(
      function () {},
      function () {
        // Server rejected — undo the optimistic removal so UI matches reality.
        toast({ kind: 'error', title: t('playlists.item_remove_failed'), text: t('toast.try_again') });
        load();
      }
    );
    if (!cache.length) {
      // Empty state has no selector — route to the rail instead of stranding
      // focus in an unfocusable 'content'.
      showState('empty', t('playlists.items_empty'));
      Controller.toggle('content');
      return;
    }
    Controller.toggle('content');
  }

  // Fill a row's title/poster/year from the catalog once it resolves.
  function enrich(item: PlaylistItem, nameEl: HTMLElement, metaEl: HTMLElement, thumb: HTMLElement): void {
    const type = item.media_type === 'tv' ? 'tv' : 'movie';
    getTitle(type, item.tmdb_id).then(
      function (card) {
        if (destroyed || !card) return;
        nameEl.textContent = card.title || nameEl.textContent;
        if (card.year) metaEl.textContent = String(card.year);
        if (card.poster) {
          const img = el('img', 'pl-item__img');
          img.src = card.poster;
          img.alt = card.title || '';
          empty(thumb);
          thumb.appendChild(img);
        }
      },
      function () {
        /* leave the placeholder text */
      }
    );
  }

  function renderList(list: PlaylistItem[]): void {
    if (!list.length) {
      showState('empty', t('playlists.items_empty'));
      return;
    }
    empty(body);
    lastRow = false;

    for (let i = 0; i < list.length; i++) {
      (function (item: PlaylistItem) {
        const row = el('div', 'pl-item');

        const main = el('div', 'pl-item__main selector');
        const thumb = el('div', 'pl-item__thumb');
        main.appendChild(thumb);
        const info = el('div', 'pl-item__info');
        const nameEl = el('div', 'pl-item__name', '#' + item.tmdb_id);
        const metaEl = el('div', 'pl-item__meta', '');
        info.appendChild(nameEl);
        info.appendChild(metaEl);
        main.appendChild(info);
        on(main, 'hover:focus', function () {
          lastRow = main;
        });
        on(main, 'hover:enter', function () {
          openTitle(item.media_type === 'tv' ? 'tv' : 'movie', item.tmdb_id);
        });
        row.appendChild(main);

        const del = el('div', 'pl-item__act selector', t('playlists.item_remove'));
        on(del, 'hover:focus', function () {
          lastRow = del;
        });
        on(del, 'hover:enter', function () {
          removeItem(item, row);
        });
        row.appendChild(del);

        body.appendChild(row);
        enrich(item, nameEl, metaEl, thumb);
      })(list[i]);
    }

    scroll.reset();
  }

  function load(): void {
    showSpinner();
    getPlaylistItems(params.id).then(
      function (res) {
        if (destroyed) return;
        cache = res && res.items ? res.items : [];
        renderList(cache);
        // Empty list renders a selector-less state block — focus the rail, not
        // an unfocusable 'content'.
        Controller.toggle(cache.length ? 'content' : 'menu');
      },
      function () {
        if (destroyed) return;
        empty(body);
        lastRow = false;
        body.appendChild(
          buildState({ kind: 'error', text: t('error.load'), onRetry: function () { load(); } })
        );
        Controller.toggle('content');
      }
    );
  }

  load();

  return {
    destroy: function () {
      destroyed = true;
    },
    resume: function () {
      Controller.add('content', contentController);
      Controller.toggle('content');
    },
  };
}
