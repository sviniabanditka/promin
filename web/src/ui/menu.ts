// Shared left icon rail, used by every menu-level screen (home / catalog /
// search). Registers the 'menu' Controller mode. Enter on an item navigates
// via screens/nav; Right returns focus to the screen's 'content' mode; Back
// falls through to the router's exit handler (root screens only).

import Controller, { on } from '../core/controller';
import * as router from '../core/router';
import { t } from '../core/i18n';
import { el } from './dom';
import {
  iconEl,
  ICON_HOME, ICON_HOME_FILL,
  ICON_CATALOG, ICON_CATALOG_FILL,
  ICON_SEARCH, ICON_SEARCH_FILL,
  ICON_SETTINGS, ICON_SETTINGS_FILL,
  ICON_BOOKMARK, ICON_BOOKMARK_FILL,
} from './icons';
import { openMenu, MenuKey } from '../screens/nav';

interface MenuDef {
  iconFill: string;
  key: MenuKey;
  labelKey: string;
  icon: string;
}

const ITEMS: MenuDef[] = [
  { key: 'home', labelKey: 'menu.home', icon: ICON_HOME, iconFill: ICON_HOME_FILL },
  { key: 'catalog', labelKey: 'menu.catalog', icon: ICON_CATALOG, iconFill: ICON_CATALOG_FILL },
  { key: 'search', labelKey: 'menu.search', icon: ICON_SEARCH, iconFill: ICON_SEARCH_FILL },
  { key: 'library', labelKey: 'menu.library', icon: ICON_BOOKMARK, iconFill: ICON_BOOKMARK_FILL },
  { key: 'settings', labelKey: 'menu.settings', icon: ICON_SETTINGS, iconFill: ICON_SETTINGS_FILL },
];

export interface Menu {
  el: HTMLElement;
  activate(): void;
}

// returnMode is the controller mode the rail hands focus back to on Right / on
// re-entering the current screen. Home/catalog use 'content'; the title screen
// uses 'title' (its action-buttons controller) — hardcoding 'content' meant
// Left-into-rail on the title page could never get back to the buttons.
export function buildMenu(activeKey: MenuKey | 'none', returnMode: string): Menu {
  const root = el('div', 'menu');
  const list = el('div', 'menu__list');

  let last: HTMLElement | false = false;
  // The rail item for the screen we're on. Used as the initial focus target so
  // entering Library/Catalog/Search/Settings rings THAT item, not Home.
  let current: HTMLElement | false = false;

  for (let i = 0; i < ITEMS.length; i++) {
    const def = ITEMS[i];
    const item = el('div', 'menu__item selector');
    if (def.key === activeKey) {
      item.classList.add('menu__item--current');
      current = item;
    }
    const label = t(def.labelKey);
    item.setAttribute('aria-label', label);
    item.appendChild(iconEl(def.key === activeKey ? def.iconFill : def.icon, 'menu__ico'));
    // Text label revealed on focus (icons alone are unreadable on a 10-ft TV).
    // Positioned absolutely to the right so showing it never shifts the rail
    // or the content beside it — audit item.
    item.appendChild(el('div', 'menu__label', label));

    on(item, 'hover:focus', function () {
      last = item;
    });
    on(item, 'hover:enter', function () {
      if (def.key === activeKey) {
        Controller.toggle(returnMode);
      } else {
        openMenu(def.key);
      }
    });

    list.appendChild(item);
  }

  root.appendChild(list);

  const calls = {
    toggle: function () {
      Controller.collectionSet(root);
      Controller.collectionFocus(last || current || false, root);
    },
    right: function () {
      Controller.toggle(returnMode);
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

  function activate(): void {
    Controller.add('menu', calls);
  }

  activate();

  return { el: root, activate: activate };
}
