// Navigation glue: maps abstract navigation intents (menu items, opening a
// title) to concrete screen factories + router verbs. Kept in one module so
// the menu component and each screen depend on this instead of importing one
// another (the cycle is only exercised at call time, never at module init).

import * as router from '../core/router';
import { t } from '../core/i18n';
import { toast } from '../ui/toast';
import { mountHome } from './home';
import { mountCatalog } from './catalog';
import { mountSearch } from './search';
import { mountTitle } from './title';
import { mountPinEntry } from './pin';
import { mountBookmarks } from './bookmarks';
import { mountTorrents } from './torrents';
import { mountSettings } from './settings';
import { mountPlaylists, mountPlaylistItems } from './playlists';
import { mountLibrary } from './library';
import { mountDevices } from './devices';
import { isLogged } from '../core/auth';

export type MenuKey =
  | 'home'
  | 'catalog'
  | 'search'
  | 'library'
  | 'settings';

// The service is closed by default: the only way in is a PIN. "Login" now means
// the PIN entry screen (password login was removed with the PIN rework).
export function openLogin(): void {
  router.replaceRoot(mountPinEntry);
}

export function openMenu(key: MenuKey): void {
  if (key === 'home') {
    router.replaceRoot(mountHome, '/');
  } else if (key === 'catalog') {
    router.replaceRoot(mountCatalog, '/catalog');
  } else if (key === 'search') {
    router.replaceRoot(mountSearch, '/search');
  } else if (key === 'library') {
    // Library (favourites + playlists + continue) requires a session.
    if (isLogged()) router.replaceRoot(mountLibrary, '/library');
    else router.replaceRoot(mountPinEntry);
  } else if (key === 'settings') {
    router.replaceRoot(mountSettings, '/settings');
  } else {
    toast({ kind: 'info', text: t('settings.soon') });
  }
}

// Open the device-session manager (pushed from the settings account section).
export function openDevices(): void {
  router.push(function (container: HTMLElement) {
    return mountDevices(container);
  }, '/devices');
}

// Downloads screen, pushed from the Library "Завантаження" tile (torrents moved
// off the rail into Library).
export function openTorrents(): void {
  router.push(function (container: HTMLElement) {
    return mountTorrents(container);
  }, '/torrents');
}

// Open one playlist's items grid (pushed from the playlists / library screen).
export function openPlaylist(id: number, name: string): void {
  router.push(function (container: HTMLElement) {
    return mountPlaylistItems(container, { id: id, name: name });
  }, '/playlist/' + id);
}

// Full favourites grid, pushed from the Library "Обране" lane's More tile.
export function openBookmarks(): void {
  router.push(function (container: HTMLElement) {
    return mountBookmarks(container);
  }, '/bookmarks');
}

// Full playlists CRUD screen, pushed from the Library "Плейлисти" lane's
// manage tile (create / rename / delete live there).
export function openPlaylistsManage(): void {
  router.push(function (container: HTMLElement) {
    return mountPlaylists(container);
  }, '/playlists');
}

// Open the full catalog scoped to a home-lane category (pushed from a lane's
// "More" tile), pre-filtered and with infinite vertical scroll.
export function openCatalog(category: string): void {
  router.push(function (container: HTMLElement) {
    return mountCatalog(container, { category: category });
  }, '/catalog/' + encodeURIComponent(category));
}

// resume: open the title AND immediately continue playback from the saved
// position (Library's "continue" lane) — Back from the player lands on the
// title, so details stay one press away. autoplay: start a movie from the top
// as soon as the title renders (watch queue).
export function openTitle(type: 'movie' | 'tv', id: number, resume?: boolean, season?: number | null, episode?: number | null, autoplay?: boolean): void {
  router.push(function (container: HTMLElement) {
    return mountTitle(container, {
      type: type,
      id: id,
      resume: !!resume,
      season: season != null ? season : undefined,
      episode: episode != null ? episode : undefined,
      autoplay: !!autoplay,
    });
  }, titlePath(type, id, season, episode));
}

// Route of a title screen; an episode deep link carries ?s=&e=.
export function titlePath(type: 'movie' | 'tv', id: number, season?: number | null, episode?: number | null): string {
  let p = '/title/' + type + '/' + id;
  if (episode != null) p += '?s=' + (season != null ? season : 1) + '&e=' + episode;
  return p;
}

// Route of the search screen for a query.
export function searchPath(q: string): string {
  const term = (q || '').trim();
  return '/search' + (term ? '?q=' + encodeURIComponent(term) : '');
}

function parseQuery(qs: string): { [k: string]: string } {
  const out: { [k: string]: string } = {};
  const parts = qs.split('&');
  for (let i = 0; i < parts.length; i++) {
    if (!parts[i]) continue;
    const eq = parts[i].indexOf('=');
    const k = eq >= 0 ? parts[i].slice(0, eq) : parts[i];
    const v = eq >= 0 ? parts[i].slice(eq + 1) : '';
    try {
      out[decodeURIComponent(k)] = decodeURIComponent(v.replace(/\+/g, ' '));
    } catch (e) {
      /* malformed escape — skip the pair */
    }
  }
  return out;
}

function intOrNull(v: string | undefined): number | null {
  if (v == null || v === '') return null;
  const n = parseInt(v, 10);
  return isNaN(n) ? null : n;
}

// Open the screen a route names, rebuilding the stack from scratch: the
// section's menu-level screen as the root plus the detail screen on top, so
// Back from a deep-linked title lands on Home like it does when browsing.
// Accepts a bare path or a location.hash ("#/title/tv/1399?s=1&e=3").
// Anything unknown or malformed opens Home. Callers must have checked
// isLogged() — the PIN gate is routed by app.ts.
export function openRoute(raw: string): void {
  const s = (raw || '').replace(/^#/, '').replace(/^\/+/, '');
  const qi = s.indexOf('?');
  const path = qi >= 0 ? s.slice(0, qi) : s;
  const q = parseQuery(qi >= 0 ? s.slice(qi + 1) : '');
  const seg = path.split('/');
  let arg = '';
  try {
    arg = decodeURIComponent(seg[1] || '');
  } catch (e) {
    arg = '';
  }

  switch (seg[0]) {
    case 'title': {
      const type = seg[1] === 'tv' ? 'tv' : 'movie';
      const id = parseInt(seg[2] || '', 10);
      if (!(id > 0)) break;
      router.replaceRoot(mountHome, '/');
      openTitle(type, id, false, intOrNull(q.s), intOrNull(q.e));
      return;
    }
    case 'catalog':
      if (arg) {
        router.replaceRoot(function (container: HTMLElement) {
          return mountCatalog(container, { category: arg });
        }, '/catalog/' + encodeURIComponent(arg));
      } else {
        router.replaceRoot(mountCatalog, '/catalog');
      }
      return;
    case 'search':
      router.replaceRoot(function (container: HTMLElement) {
        return mountSearch(container, { q: q.q || '' });
      }, searchPath(q.q || ''));
      return;
    case 'library':
      openMenu('library');
      return;
    case 'settings':
      openMenu('settings');
      return;
    case 'playlist': {
      const id = parseInt(seg[1] || '', 10);
      if (!(id > 0)) break;
      openMenu('library');
      openPlaylist(id, '');
      return;
    }
    case 'bookmarks':
      openMenu('library');
      openBookmarks();
      return;
    case 'playlists':
      openMenu('library');
      openPlaylistsManage();
      return;
    case 'torrents':
      openMenu('library');
      openTorrents();
      return;
    case 'devices':
      openMenu('settings');
      openDevices();
      return;
  }
  router.replaceRoot(mountHome, '/');
}
