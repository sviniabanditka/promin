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
    router.replaceRoot(mountHome);
  } else if (key === 'catalog') {
    router.replaceRoot(mountCatalog);
  } else if (key === 'search') {
    router.replaceRoot(mountSearch);
  } else if (key === 'library') {
    // Library (favourites + playlists + continue) requires a session.
    if (isLogged()) router.replaceRoot(mountLibrary);
    else router.replaceRoot(mountPinEntry);
  } else if (key === 'settings') {
    router.replaceRoot(mountSettings);
  } else {
    toast(t('settings.soon'));
  }
}

// Open the device-session manager (pushed from the settings account section).
export function openDevices(): void {
  router.push(function (container: HTMLElement) {
    return mountDevices(container);
  });
}

// Downloads screen, pushed from the Library "Завантаження" tile (torrents moved
// off the rail into Library).
export function openTorrents(): void {
  router.push(function (container: HTMLElement) {
    return mountTorrents(container);
  });
}

// Open one playlist's items grid (pushed from the playlists / library screen).
export function openPlaylist(id: number, name: string): void {
  router.push(function (container: HTMLElement) {
    return mountPlaylistItems(container, { id: id, name: name });
  });
}

// Full favourites grid, pushed from the Library "Обране" lane's More tile.
export function openBookmarks(): void {
  router.push(function (container: HTMLElement) {
    return mountBookmarks(container);
  });
}

// Full playlists CRUD screen, pushed from the Library "Плейлисти" lane's
// manage tile (create / rename / delete live there).
export function openPlaylistsManage(): void {
  router.push(function (container: HTMLElement) {
    return mountPlaylists(container);
  });
}

// Open the full catalog scoped to a home-lane category (pushed from a lane's
// "More" tile), pre-filtered and with infinite vertical scroll.
export function openCatalog(category: string): void {
  router.push(function (container: HTMLElement) {
    return mountCatalog(container, { category: category });
  });
}

// resume: open the title AND immediately continue playback from the saved
// position (Library's "continue" lane) — Back from the player lands on the
// title, so details stay one press away.
export function openTitle(type: 'movie' | 'tv', id: number, resume?: boolean): void {
  router.push(function (container: HTMLElement) {
    return mountTitle(container, { type: type, id: id, resume: !!resume });
  });
}
