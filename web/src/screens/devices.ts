// Devices / sessions screen (docs/frontend.md §"Профиль / устройства",
// docs/api.md). Pushed from the profile screen. Lists the current
// user's active sessions (GET /auth/devices); OK on a session opens a confirm
// dialog and revokes it (DELETE /auth/devices/{token_id}). The current session
// is shown with a badge and can't be revoked from here (self-eviction guard —
// the backend needs ?force=true, which this screen never sends).
//
// ES5 target (swc): plain functions, no async/await/for-of/spread/find/includes.

import Controller, { on } from '../core/controller';
import { Scroll } from '../core/scroll';
import { t } from '../core/i18n';
import * as router from '../core/router';
import { ScreenInstance } from '../core/activity';
import { getDevices, deleteDevice, Device } from '../core/api';
import { el, empty, pad2 } from '../ui/dom';
import { buildHead, Head } from '../ui/head';
import { buildFooter } from '../ui/shell';
import { buildState } from '../ui/state';
import { toast } from '../ui/toast';

function fmtDate(sec: number | undefined): string {
  if (!sec || sec <= 0) return '';
  try {
    const d = new Date(sec * 1000);
    return (
      d.getFullYear() +
      '-' +
      pad2(d.getMonth() + 1) +
      '-' +
      pad2(d.getDate()) +
      ' ' +
      pad2(d.getHours()) +
      ':' +
      pad2(d.getMinutes())
    );
  } catch (e) {
    return '';
  }
}

export function mountDevices(container: HTMLElement): ScreenInstance {
  container.className += ' devices-screen';

  const header = el('div', 'devices-header');
  header.appendChild(el('div', 'devices-header__title', t('devices.title')));
  container.appendChild(header);

  const head: Head = buildHead();
  container.appendChild(head.el);
  container.appendChild(buildFooter());

  const wrap = el('div', 'devices-wrap');
  const scroll = new Scroll({ mask: true, over: true });
  wrap.appendChild(scroll.render());
  container.appendChild(wrap);
  const body = scroll.body();
  body.classList.add('devices-body');

  let lastRow: HTMLElement | false = false;
  let destroyed = false;
  let paused = false; // hidden under a pushed screen: never toggle from a late load
  let cache: Device[] = [];

  // Back handler for the content mode; the confirm dialog overrides it while open.
  const contentController = {
    toggle: function () {
      Controller.collectionSet(body);
      Controller.collectionFocus(lastRow || false, body);
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

  // ---- revoke confirm ----
  let confirmBox: HTMLElement | null = null;
  function closeConfirm(): void {
    if (confirmBox && confirmBox.parentNode) confirmBox.parentNode.removeChild(confirmBox);
    confirmBox = null;
    Controller.toggle('content');
  }
  function askRevoke(dev: Device, row: HTMLElement): void {
    const name = dev.device_name || dev.token_id;
    const box = el('div', 'settings-modal');
    const inner = el('div', 'player__confirm');
    inner.appendChild(el('div', 'player__confirm-text', t('devices.revoke_confirm', { name: name })));
    const actions = el('div', 'player__confirm-actions');
    const yes = el('div', 'button button--accent selector', t('devices.revoke'));
    on(yes, 'hover:enter', function () {
      closeConfirm();
      revoke(dev, row);
    });
    const no = el('div', 'button selector', t('action.cancel'));
    on(no, 'hover:enter', function () {
      closeConfirm();
    });
    actions.appendChild(yes);
    actions.appendChild(no);
    inner.appendChild(actions);
    box.appendChild(inner);
    container.appendChild(box);
    confirmBox = box;

    Controller.add('devices_confirm', {
      toggle: function () {
        Controller.collectionSet(box);
        Controller.collectionFocus(no, box);
      },
      left: function () {
        Controller.moveOr('left');
      },
      right: function () {
        Controller.moveOr('right');
      },
      back: function () {
        closeConfirm();
      },
    });
    Controller.toggle('devices_confirm');
  }

  function revoke(dev: Device, row: HTMLElement): void {
    // Optimistic: drop the row, then call the backend.
    if (row.parentNode) row.parentNode.removeChild(row);
    const next: Device[] = [];
    for (let i = 0; i < cache.length; i++) {
      if (cache[i].token_id !== dev.token_id) next.push(cache[i]);
    }
    cache = next;
    lastRow = false;
    if (!cache.length) showState('empty', t('devices.empty'));
    Controller.toggle('content');
    deleteDevice(dev.token_id).then(
      function () {
        /* gone */
      },
      function () {
        toast({ kind: 'error', title: t('devices.revoke_failed'), text: t('toast.try_again') });
      }
    );
  }

  function renderList(list: Device[]): void {
    if (!list.length) {
      showState('empty', t('devices.empty'));
      return;
    }
    empty(body);
    lastRow = false;

    for (let i = 0; i < list.length; i++) {
      (function (dev: Device) {
        const row = el('div', 'device-row selector');
        const main = el('div', 'device-row__main');
        main.appendChild(el('div', 'device-row__name', dev.device_name || dev.token_id));

        const meta = el('div', 'device-row__meta');
        if (dev.device_type) meta.appendChild(el('span', 'device-row__type', dev.device_type));
        const seen = fmtDate(dev.last_seen);
        if (seen) meta.appendChild(el('span', 'device-row__seen', t('devices.last_seen', { time: seen })));
        main.appendChild(meta);
        row.appendChild(main);

        if (dev.current) {
          row.appendChild(el('div', 'device-row__badge', t('devices.current')));
        } else {
          row.appendChild(el('div', 'device-row__action', t('devices.revoke')));
        }

        on(row, 'hover:focus', function () {
          lastRow = row;
        });
        on(row, 'hover:enter', function () {
          // The current session isn't revocable from here (see header note).
          if (dev.current) return;
          askRevoke(dev, row);
        });
        body.appendChild(row);
      })(list[i]);
    }

    scroll.reset();
  }

  function load(): void {
    showSpinner();
    getDevices().then(
      function (res) {
        if (destroyed) return;
        cache = res && res.devices ? res.devices : [];
        renderList(cache);
        if (!paused) Controller.toggle('content');
      },
      function () {
        if (destroyed) return;
        // Focusable retry: a bare error state has no .selector, so nothing gets
        // the ring and arrows are dead — only a blind Back escapes.
        empty(body);
        lastRow = false;
        body.appendChild(buildState({ kind: 'error', text: t('error.load'), onRetry: load }));
        if (!paused) Controller.toggle('content');
      }
    );
  }

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
      Controller.add('content', contentController);
      Controller.toggle('content');
    },
  };
}
