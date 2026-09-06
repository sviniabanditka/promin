// Settings screen (docs/frontend.md, docs/api.md). A vertical
// list of preferences (mode 'content') behind the left menu rail. OK on a row
// opens a small value-picker modal (mode 'settings_modal'); the legacy-TV row
// toggles in place. Every change applies immediately (i18n re-render, player /
// capabilities pick the new value up) and is mirrored to the backend via
// core/settings when logged in.
//
// ES5 target (swc): plain functions, no async/await/for-of/spread/find/includes.

import Controller, { on } from '../core/controller';
import { Scroll } from '../core/scroll';
import { t, getLang, Lang } from '../core/i18n';
import { ScreenInstance } from '../core/activity';
import {
  getPing,
  clearMyHistory,
  deleteMyData,
  getTelegramStatus,
  createTelegramLink,
  unlinkTelegram,
  TelegramStatus,
  revokeOtherDevices,
} from '../core/api';
import * as router from '../core/router';
import * as sync from '../core/sync';
import { isLogged, getUser, logout as authLogout, clearLocal as authClearLocal } from '../core/auth';
import { mountPinEntry } from './pin';
import { openDevices } from './nav';
import {
  getScreensaverMin,
  setScreensaverMin,
  getSubSize,
  setSubSize,
  SubSize,
  isReduceMotion,
  setReduceMotion,
  isDebugMode,
  setDebugMode,
  isNightMode,
  setNightMode,
  getNightDim,
  setNightDim,
  NIGHT_DIM_VALUES,
} from '../core/settings';
import { report, viewportInfo } from '../core/diag';
import { steerHost } from '../core/legacy';
import { toast } from '../ui/toast';
import * as screensaver from '../core/screensaver';
import { openConfirm } from '../ui/confirm';
import { el, empty } from '../ui/dom';
import { buildMenu, Menu } from '../ui/menu';
import { buildHead, Head } from '../ui/head';
import { buildFooter } from '../ui/shell';
import {
  getDefaultQuality,
  getPlayerEngine,
  isLegacyTv,
  setLanguage,
  setDefaultQuality,
  setPlayerEngine,
  setLegacyTv,
  Quality,
  Engine,
} from '../core/settings';

interface Option {
  value: string;
  label: string;
}

export function mountSettings(container: HTMLElement): ScreenInstance {
  container.className += ' settings-screen';

  const menu: Menu = buildMenu('settings', 'content');
  container.appendChild(menu.el);

  const head: Head = buildHead();
  container.appendChild(head.el);
  container.appendChild(buildFooter());

  const wrap = el('div', 'settings-wrap');
  const scroll = new Scroll({ mask: true, over: true });
  wrap.appendChild(scroll.render());
  container.appendChild(wrap);
  const body = scroll.body();
  body.classList.add('settings-body');

  let rowEls: HTMLElement[] = [];
  let focusIndex = 0;
  let version = '';
  let versionValueEl: HTMLElement | null = null;

  // ---- value pickers -------------------------------------------------

  function langOptions(): Option[] {
    return [
      { value: 'uk', label: t('lang.uk') },
      { value: 'ru', label: t('lang.ru') },
      { value: 'en', label: t('lang.en') },
    ];
  }
  function qualityOptions(): Option[] {
    return [
      { value: 'auto', label: t('quality.auto') },
      { value: '2160', label: t('quality.2160') },
      { value: '1080', label: t('quality.1080') },
      { value: '720', label: t('quality.720') },
      { value: '480', label: t('quality.480') },
    ];
  }
  function screensaverOptions(): Option[] {
    return [
      { value: '0', label: t('screensaver.off') },
      { value: '3', label: t('screensaver.minutes', { n: '3' }) },
      { value: '5', label: t('screensaver.minutes', { n: '5' }) },
      { value: '10', label: t('screensaver.minutes', { n: '10' }) },
    ];
  }
  function engineOptions(): Option[] {
    return [
      { value: 'auto', label: t('engine.auto') },
      { value: 'hlsjs', label: t('engine.hlsjs') },
      { value: 'native', label: t('engine.native') },
    ];
  }

  // ---- logout confirm ------------------------------------------------
  // Session-ending action: confirmed like playlist-delete / device-revoke.
  function askLogout(): void {
    openConfirm(container, {
      text: t('profile.logout_confirm'),
      yesLabel: t('profile.logout'),
      mode: 'settings_logout',
      onYes: function () {
        sync.stop();
        const gate = function () {
          router.replaceRoot(mountPinEntry);
        };
        authLogout().then(gate, gate);
      },
    });
  }

  // ---- Telegram companion --------------------------------------------

  let tgStatus: TelegramStatus | null = null;
  function tgLabel(st: TelegramStatus): string {
    if (!st.enabled) return t('telegram.disabled_short');
    return t(st.linked ? 'telegram.linked' : 'telegram.not_linked');
  }
  function askUnlinkTelegram(): void {
    openConfirm(container, {
      text: t('telegram.unlink_confirm'),
      yesLabel: t('telegram.unlink'),
      mode: 'settings_telegram',
      onYes: function () {
        Controller.toggle('content');
        unlinkTelegram().then(
          function () {
            tgStatus = null;
            toast({ kind: 'success', icon: '✓', text: t('telegram.unlinked') });
            renderList();
            Controller.toggle('content');
          },
          function () {
            toast({ kind: 'error', title: t('error.load'), text: t('toast.try_again') });
          }
        );
      },
    });
  }
  // Link code sheet: a 6-digit code (also as a t.me deep link) the user sends
  // to the bot; the sheet polls status and closes itself once linked.
  function openTelegramLink(): void {
    const overlay = el('div', 'settings-modal');
    const box = el('div', 'settings-modal__box tg-box');
    const qr = document.createElement('img');
    qr.className = 'tg-qr hide';
    qr.alt = '';
    box.appendChild(qr);
    const side = el('div', 'tg-side');
    side.appendChild(el('div', 'settings-modal__title', t('telegram.link_title')));
    const codeEl = el('div', 'tg-code', '······');
    const hint = el('div', 'tg-hint', t('telegram.link_hint', { bot: tgStatus && tgStatus.bot_username ? '@' + tgStatus.bot_username : t('telegram.the_bot') }));
    const link = el('div', 'tg-link', '');
    side.appendChild(codeEl);
    side.appendChild(hint);
    side.appendChild(link);
    const close = el('div', 'button selector tg-close', t('action.cancel'));
    side.appendChild(close);
    box.appendChild(side);
    overlay.appendChild(box);
    container.appendChild(overlay);
    modal = overlay;

    let poll = 0;
    let dead = false;
    function stop(): void {
      dead = true;
      if (poll) window.clearInterval(poll);
    }
    function done(): void {
      stop();
      closeModal();
    }
    createTelegramLink().then(
      function (l) {
        if (dead) return;
        codeEl.textContent = l.code.slice(0, 3) + ' ' + l.code.slice(3);
        if (l.deep_link) link.textContent = l.deep_link.replace(/^https?:\/\//, '');
        if (l.qr) {
          qr.src = l.qr;
          qr.classList.remove('hide');
        }
      },
      function () {
        if (dead) return;
        codeEl.textContent = '—';
        hint.textContent = t('error.load');
      }
    );
    poll = window.setInterval(function () {
      getTelegramStatus().then(
        function (st) {
          if (dead) return;
          tgStatus = st;
          if (st.linked) {
            toast({ kind: 'success', icon: '✓', title: t('telegram.linked_toast'), text: t('telegram.linked_hint') });
            done();
            renderList();
            Controller.toggle('content');
          }
        },
        function () {}
      );
    }, 3000);
    on(close, 'hover:enter', done);
    Controller.add('settings_modal', {
      toggle: function () {
        Controller.collectionSet(overlay);
        Controller.collectionFocus(close, overlay);
      },
      back: function () {
        done();
      },
    });
    Controller.toggle('settings_modal');
  }

  // ---- modal (mode 'settings_modal') ---------------------------------

  let modal: HTMLElement | null = null;

  function closeModal(): void {
    if (modal && modal.parentNode) modal.parentNode.removeChild(modal);
    modal = null;
    Controller.toggle('content');
  }

  function openOptions(titleText: string, options: Option[], current: string, onSelect: (v: string) => void): void {
    const overlay = el('div', 'settings-modal');
    const box = el('div', 'settings-modal__box');
    box.appendChild(el('div', 'settings-modal__title', titleText));
    const list = el('div', 'settings-modal__list');
    box.appendChild(list);

    let activeItem: HTMLElement | false = false;
    for (let i = 0; i < options.length; i++) {
      (function (opt: Option) {
        const item = el('div', 'settings-opt selector' + (opt.value === current ? ' is-active' : ''), opt.label);
        if (opt.value === current) activeItem = item;
        on(item, 'hover:enter', function () {
          onSelect(opt.value);
        });
        list.appendChild(item);
      })(options[i]);
    }

    overlay.appendChild(box);
    container.appendChild(overlay);
    modal = overlay;

    Controller.add('settings_modal', {
      toggle: function () {
        Controller.collectionSet(overlay);
        Controller.collectionFocus(activeItem || false, overlay);
      },
      up: function () {
        Controller.moveOr('up');
      },
      down: function () {
        Controller.moveOr('down');
      },
      back: function () {
        closeModal();
      },
    });
    Controller.toggle('settings_modal');
  }

  // ---- list ----------------------------------------------------------

  const contentController = {
    toggle: function () {
      Controller.collectionSet(body);
      const target = rowEls[focusIndex] || false;
      Controller.collectionFocus(target, body);
    },
    left: function () {
      Controller.toggle('menu');
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

  function renderList(): void {
    empty(body);
    rowEls = [];
    versionValueEl = null;

    body.appendChild(el('div', 'settings-title', t('settings.title')));

    function addRow(labelKey: string, valueTextStr: string, onEnter: (() => void) | null): HTMLElement {
      const idx = rowEls.length;
      const row = el('div', 'settings-row' + (onEnter ? ' selector' : ' settings-row--static'));
      row.appendChild(el('div', 'settings-row__label', t(labelKey)));
      const val = el('div', 'settings-row__value', valueTextStr);
      row.appendChild(val);
      if (onEnter) {
        on(row, 'hover:focus', function () {
          focusIndex = idx;
        });
        on(row, 'hover:enter', onEnter);
        rowEls.push(row);
      }
      body.appendChild(row);
      return val;
    }

    // Account section (profile merged into settings). Only when signed in.
    if (isLogged()) {
      body.appendChild(el('div', 'settings-section', t('menu.profile')));
      const u = getUser();
      if (u) addRow('profile.signed_in', u.login, null);
      addRow('devices.open', '', function () {
        openDevices();
      });
      addRow('devices.revoke_others', '', function () {
        openConfirm(container, {
          text: t('devices.revoke_others_confirm'),
          yesLabel: t('devices.revoke_others_yes'),
          mode: 'settings_devices',
          onYes: function () {
            Controller.toggle('content');
            revokeOtherDevices().then(
              function (r) {
                toast({ kind: 'success', icon: '✓', title: t('devices.revoke_others_done'), text: String(r && r.revoked != null ? r.revoked : '') });
              },
              function () {
                toast({ kind: 'error', title: t('error.load'), text: t('toast.try_again') });
              }
            );
          },
        });
      });
      const tgVal = addRow('telegram.row', tgStatus ? tgLabel(tgStatus) : '…', function () {
        if (!tgStatus) return;
        if (!tgStatus.enabled) {
          toast({ kind: 'info', text: t('telegram.disabled') });
          return;
        }
        if (tgStatus.linked) askUnlinkTelegram();
        else openTelegramLink();
      });
      if (!tgStatus) {
        getTelegramStatus().then(
          function (st) {
            tgStatus = st;
            tgVal.textContent = tgLabel(st);
          },
          function () {
            tgVal.textContent = '—';
          }
        );
      }
      addRow('profile.logout', '', function () {
        askLogout();
      });
    }

    body.appendChild(el('div', 'settings-section', t('settings.section_general')));

    addRow('settings.lang', t('lang.' + getLang()), function () {
      openOptions(t('settings.lang'), langOptions(), getLang(), function (v) {
        setLanguage(v as Lang);
        closeModal();
        // Labels are language-dependent — rebuild the whole list.
        renderList();
        Controller.toggle('content');
      });
    });

    const svMin = getScreensaverMin();
    const svText = svMin > 0 ? t('screensaver.minutes', { n: String(svMin) }) : t('screensaver.off');
    addRow('settings.screensaver', svText, function () {
      openOptions(t('settings.screensaver'), screensaverOptions(), String(svMin), function (v) {
        setScreensaverMin(parseInt(v, 10) || 0);
        screensaver.reschedule();
        closeModal();
        renderList();
        Controller.toggle('content');
      });
    });

    body.appendChild(el('div', 'settings-section', t('settings.section_playback')));

    addRow('settings.night', t(isNightMode() ? 'toggle.on' : 'toggle.off'), function () {
      setNightMode(!isNightMode());
      renderList();
      Controller.toggle('content'); // re-collect the fresh rows and refocus rowEls[focusIndex]
    });
    addRow('settings.night_dim', getNightDim() + '%', function () {
      const opts: Option[] = [];
      for (let i = 0; i < NIGHT_DIM_VALUES.length; i++) {
        opts.push({ value: String(NIGHT_DIM_VALUES[i]), label: NIGHT_DIM_VALUES[i] + '%' });
      }
      openOptions(t('settings.night_dim'), opts, String(getNightDim()), function (v) {
        setNightDim(parseInt(v, 10));
        closeModal();
        renderList();
        Controller.toggle('content');
      });
    });

    addRow('settings.quality', t('quality.' + getDefaultQuality()), function () {
      openOptions(t('settings.quality'), qualityOptions(), getDefaultQuality(), function (v) {
        setDefaultQuality(v as Quality);
        closeModal();
        renderList();
        Controller.toggle('content');
      });
    });

    addRow('settings.engine', t('engine.' + getPlayerEngine()), function () {
      openOptions(t('settings.engine'), engineOptions(), getPlayerEngine(), function (v) {
        setPlayerEngine(v as Engine);
        closeModal();
        renderList();
        Controller.toggle('content');
      });
    });

    addRow('settings.subs_size', t('subsize.' + getSubSize()), function () {
      openOptions(
        t('settings.subs_size'),
        [
          { value: 'small', label: t('subsize.small') },
          { value: 'medium', label: t('subsize.medium') },
          { value: 'large', label: t('subsize.large') },
        ],
        getSubSize(),
        function (v) {
          setSubSize(v as SubSize);
          closeModal();
          renderList();
          Controller.toggle('content');
        }
      );
    });

    addRow('settings.legacy', t(isLegacyTv() ? 'toggle.on' : 'toggle.off'), function () {
      // In-place toggle (no modal) — the fastest interaction for a boolean.
      // Device-local; flipping it may move the app to the other host.
      setLegacyTv(!isLegacyTv());
      renderList();
      Controller.toggle('content');
      toast({ kind: 'info', title: t('settings.legacy') + ': ' + t(isLegacyTv() ? 'toggle.on' : 'toggle.off'), text: t(isLegacyTv() ? 'settings.legacy_on_hint' : 'settings.legacy_off_hint'), duration: 5000 });
      steerHost();
    });

    addRow('settings.reduce_motion', t(isReduceMotion() ? 'toggle.on' : 'toggle.off'), function () {
      setReduceMotion(!isReduceMotion());
      renderList();
      Controller.toggle('content');
    });

    // Diagnostics mode (device-local): key codes, viewport, JS errors and
    // future probes go to the server log via core/diag.ts.
    addRow('settings.debug', t(isDebugMode() ? 'toggle.on' : 'toggle.off'), function () {
      setDebugMode(!isDebugMode());
      renderList();
      Controller.toggle('content');
      if (isDebugMode()) {
        report('viewport', viewportInfo());
        toast({ kind: 'info', title: t('settings.debug') + ': ' + t('toggle.on'), text: t('settings.debug_on_hint'), duration: 5000 });
      }
    });

    body.appendChild(el('div', 'settings-section', t('settings.about')));

    versionValueEl = addRow('settings.version', version || '…', null);

    // Danger zone: destructive, signed-in only, each behind a confirm sheet.
    if (isLogged()) {
      body.appendChild(el('div', 'settings-section settings-section--danger', t('settings.danger')));
      const hist = addRow('settings.clear_history', '', function () {
        openConfirm(container, {
          text: t('settings.clear_history_confirm'),
          yesLabel: t('settings.clear_history_yes'),
          mode: 'settings_danger',
          onYes: function () {
            Controller.toggle('content');
            clearMyHistory().then(
              function () {
                toast({ kind: 'success', icon: '✓', text: t('settings.clear_history_done') });
              },
              function () {
                toast({ kind: 'error', title: t('error.load'), text: t('toast.try_again') });
              }
            );
          },
        });
      });
      hist.parentElement!.classList.add('settings-row--danger');
      const wipe = addRow('settings.delete_data', '', function () {
        openConfirm(container, {
          text: t('settings.delete_data_confirm'),
          yesLabel: t('settings.delete_data_yes'),
          mode: 'settings_danger',
          onYes: function () {
            sync.stop();
            const gate = function () {
              authClearLocal();
              router.replaceRoot(mountPinEntry);
            };
            deleteMyData().then(gate, gate);
          },
        });
      });
      wipe.parentElement!.classList.add('settings-row--danger');
    }

    Controller.add('content', contentController);
  }

  // ---- init ----------------------------------------------------------

  getPing().then(
    function (res) {
      version = res && res.version ? res.version : '';
      if (versionValueEl) versionValueEl.textContent = version || '—';
    },
    function () {
      if (versionValueEl) versionValueEl.textContent = '—';
    }
  );

  renderList();
  Controller.toggle('content');

  return {
    destroy: function () {
      head.destroy();
    },
    resume: function () {
      menu.activate();
      Controller.add('content', contentController);
      Controller.toggle('content');
    },
  };
}
