// PIN entry — the hard gate. A 6-digit dialer usable by D-pad (TV remote),
// mouse and touch (one hover:enter handler covers all), plus hardware number
// keys. On success the profile's session is stored and Home mounts. There is no
// "back" — the gate can't be dismissed.
//
// ES5 target (swc): plain functions/const/let.

import Controller, { on } from '../core/controller';
import * as router from '../core/router';
import { ScreenInstance } from '../core/activity';
import { el, empty } from '../ui/dom';
import { loginPin } from '../core/auth';
import * as sync from '../core/sync';
import { syncFromServer } from '../core/settings';
import { mountHome } from './home';
import { ApiError } from '../core/api';
import { t } from '../core/i18n';

const KEYS = ['1', '2', '3', '4', '5', '6', '7', '8', '9', '', '0', 'back'];

export function mountPinEntry(container: HTMLElement): ScreenInstance {
  container.className += ' pin-screen';

  const box = el('div', 'pin-box');
  box.appendChild(el('div', 'pin-title', t('pin.title')));

  const dots = el('div', 'pin-dots');
  box.appendChild(dots);

  const err = el('div', 'pin-err', '');
  box.appendChild(err);

  const pad = el('div', 'pin-pad');
  box.appendChild(pad);
  container.appendChild(box);

  let buf = '';
  let busy = false;

  function paintDots(): void {
    empty(dots);
    for (let i = 0; i < 6; i++) {
      dots.appendChild(el('div', 'pin-dot' + (i < buf.length ? ' pin-dot--on' : '')));
    }
  }

  function shake(): void {
    box.classList.remove('pin-box--shake');
    // reflow to restart the animation
    void box.offsetWidth;
    box.classList.add('pin-box--shake');
  }

  function submit(): void {
    if (buf.length !== 6 || busy) return;
    busy = true;
    err.textContent = '';
    loginPin(buf).then(
      function () {
        sync.start();
        syncFromServer(function () {
          /* adopt server settings; ignore result */
        });
        router.replaceRoot(mountHome);
      },
      function (e: ApiError) {
        busy = false;
        buf = '';
        paintDots();
        shake();
        err.textContent = e && e.status === 429 ? t('pin.err_ratelimit') : t('pin.err_wrong');
      }
    );
  }

  function press(d: string): void {
    if (busy || buf.length >= 6) return;
    buf += d;
    paintDots();
    if (buf.length === 6) submit();
  }
  function backspace(): void {
    if (busy || !buf.length) return;
    buf = buf.slice(0, -1);
    paintDots();
    err.textContent = '';
  }

  // ---- pad cells ----
  for (let i = 0; i < KEYS.length; i++) {
    const k = KEYS[i];
    if (k === '') {
      pad.appendChild(el('div', 'pin-cell pin-cell--gap'));
      continue;
    }
    const cell = el('div', 'pin-cell selector', k === 'back' ? '⌫' : k);
    (function (key: string) {
      on(cell, 'hover:enter', function () {
        if (key === 'back') backspace();
        else press(key);
      });
    })(k);
    pad.appendChild(cell);
  }

  paintDots();

  Controller.add('pin', {
    toggle: function () {
      Controller.collectionSet(pad);
      Controller.collectionFocus(false, pad);
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
      /* the gate cannot be dismissed */
    },
  });
  Controller.toggle('pin');

  // Hardware number keys (USB keyboard / some remotes) fill the buffer directly.
  function onKey(e: KeyboardEvent): void {
    const code = e.keyCode || e.which;
    if (code >= 48 && code <= 57) {
      press(String(code - 48));
    } else if (code >= 96 && code <= 105) {
      press(String(code - 96)); // numpad
    } else if (code === 8) {
      e.preventDefault();
      backspace();
    } else if (code === 13) {
      submit();
    }
  }
  window.addEventListener('keydown', onKey);

  return {
    destroy: function () {
      window.removeEventListener('keydown', onKey);
    },
  };
}
