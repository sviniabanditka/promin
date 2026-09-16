// Live-TV overlay for the player (docs/tv.md). While a channel plays the
// remote behaves like a TV set: ▲/▼ zap through the current list, digits pick
// a channel by its number, OK opens the guide — channels on the left (logo,
// number, what is on now with a progress bar), the focused channel's
// programme on the right. Every zap shows a short info bar (now / next).
// Controller modes: 'player_tv' (channel column), 'player_tv_prog' (programme
// column). The host (player) owns switching streams.

import Controller, { on } from '../controller';
import { Navigator } from '../nav';
import { Scroll } from '../scroll';
import { t } from '../i18n';
import { el, empty, pad2 } from '../../ui/dom';
import { PlayerMedia, EpisodeMeta } from './index'; // type-only: erased, no runtime cycle

export interface TvGuideChannel {
  id: string;
  name: string;
  logo: string;
  quality: string;
}

export interface TvProgram {
  start: number; // unix seconds
  stop: number;
  title: string;
  desc?: string;
}

export interface TvNowNext {
  now?: TvProgram;
  next?: TvProgram;
}

// What the TV screen hands to the player alongside the first channel's media.
export interface PlayerTv {
  channels: TvGuideChannel[];
  index: number;
  play: (index: number, done: (m: PlayerMedia | null, meta?: EpisodeMeta) => void) => void;
  epg: (id: string, done: (items: TvProgram[]) => void) => void;
  nowNext: (done: (items: { [id: string]: TvNowNext }) => void) => void;
}

export interface TvGuideHost {
  switchTo: (index: number) => void;
  setMode: (name: string) => void;
}

export interface TvGuide {
  open: () => void;
  close: () => void;
  isOpen: () => boolean;
  zap: (dir: 1 | -1) => void;
  digit: (n: number) => void;
  // The player switched to channel i (zap, guide or digits): refresh the bar.
  switched: (index: number) => void;
  destroy: () => void;
}

const INFO_MS = 5000;
const DIGITS_MS = 1800;
const NOW_TTL_MS = 60000;
const LOGO_WINDOW = 25; // rows around the focus whose logos are loaded

function hhmm(sec: number): string {
  const d = new Date(sec * 1000);
  return pad2(d.getHours()) + ':' + pad2(d.getMinutes());
}

function dayLabel(sec: number): string {
  const d = new Date(sec * 1000);
  const today = new Date();
  const diff = Math.round((new Date(d.getFullYear(), d.getMonth(), d.getDate()).getTime() - new Date(today.getFullYear(), today.getMonth(), today.getDate()).getTime()) / 86400000);
  if (diff === 0) return t('tv.today');
  if (diff === 1) return t('tv.tomorrow');
  if (diff === -1) return t('tv.yesterday');
  return pad2(d.getDate()) + '.' + pad2(d.getMonth() + 1);
}

function progress(p: TvProgram | undefined, nowSec: number): number {
  if (!p || p.stop <= p.start) return 0;
  return Math.max(0, Math.min(1, (nowSec - p.start) / (p.stop - p.start)));
}

export function mountTvGuide(root: HTMLElement, tv: PlayerTv, host: TvGuideHost): TvGuide {
  let nn: { [id: string]: TvNowNext } = {};
  let nnAt = 0;
  let open = false;
  let destroyed = false;
  let epgCache: { [id: string]: TvProgram[] } = {};
  let focusedIdx = tv.index;
  let epgTimer = 0;
  let infoTimer = 0;
  let digitsTimer = 0;
  let digits = '';

  // ---- DOM ----
  const overlay = el('div', 'tvg hide');
  root.appendChild(overlay);
  const listCol = el('div', 'tvg__list');
  overlay.appendChild(listCol);
  const listScroll = new Scroll({ mask: true, over: true });
  listCol.appendChild(listScroll.render());
  const listBody = listScroll.body();
  const progCol = el('div', 'tvg__prog');
  overlay.appendChild(progCol);
  const progHead = el('div', 'tvg__prog-head');
  progCol.appendChild(progHead);
  const progScroll = new Scroll({ mask: true, over: true });
  progCol.appendChild(progScroll.render());
  const progBody = progScroll.body();

  const info = el('div', 'tvg-info hide');
  root.appendChild(info);
  const infoLogo = el('div', 'tvg-info__logo');
  info.appendChild(infoLogo);
  const infoMain = el('div', 'tvg-info__main');
  info.appendChild(infoMain);
  const infoTop = el('div', 'tvg-info__top');
  infoMain.appendChild(infoTop);
  const infoNum = el('span', 'tvg-info__num');
  infoTop.appendChild(infoNum);
  const infoName = el('span', 'tvg-info__name');
  infoTop.appendChild(infoName);
  const infoNow = el('div', 'tvg-info__now');
  infoMain.appendChild(infoNow);
  const infoBar = el('div', 'tvg-info__bar');
  const infoFill = el('div', 'tvg-info__fill');
  infoBar.appendChild(infoFill);
  infoMain.appendChild(infoBar);
  const infoNext = el('div', 'tvg-info__next');
  infoMain.appendChild(infoNext);

  // ---- channel rows ----
  const rows: HTMLElement[] = [];
  const rowNow: HTMLElement[] = [];
  const rowFill: HTMLElement[] = [];
  const rowLogo: HTMLElement[] = [];
  for (let i = 0; i < tv.channels.length; i++) {
    (function (i: number) {
      const ch = tv.channels[i];
      const row = el('div', 'tvg-ch selector');
      const logo = el('div', 'tvg-ch__logo');
      if (!ch.logo) logo.appendChild(el('div', 'tvg-ch__initial', (ch.name || '?').slice(0, 2).toUpperCase()));
      row.appendChild(logo);
      row.appendChild(el('div', 'tvg-ch__num', String(i + 1)));
      const txt = el('div', 'tvg-ch__text');
      txt.appendChild(el('div', 'tvg-ch__name', ch.name));
      const now = el('div', 'tvg-ch__now');
      txt.appendChild(now);
      const bar = el('div', 'tvg-ch__bar');
      const fill = el('div', 'tvg-ch__fill');
      bar.appendChild(fill);
      txt.appendChild(bar);
      row.appendChild(txt);
      on(row, 'hover:focus', function () {
        focusedIdx = i;
        revealLogos(i);
        scheduleEpg(ch.id);
      });
      on(row, 'hover:enter', function () {
        close();
        if (i !== tv.index) host.switchTo(i);
      });
      rows.push(row);
      rowNow.push(now);
      rowFill.push(fill);
      rowLogo.push(logo);
      listBody.appendChild(row);
    })(i);
  }

  // ponytail: every channel gets a row up front (2k rows in "all channels");
  // only logos are lazy. Virtualise the column if a TV chokes on it.
  function revealLogos(center: number): void {
    const from = Math.max(0, center - LOGO_WINDOW);
    const to = Math.min(tv.channels.length - 1, center + LOGO_WINDOW);
    for (let i = from; i <= to; i++) {
      const box = rowLogo[i];
      if (box.getAttribute('data-loaded') || !tv.channels[i].logo) continue;
      box.setAttribute('data-loaded', '1');
      const img = document.createElement('img');
      img.alt = '';
      img.onerror = function () {
        img.style.display = 'none';
        box.appendChild(el('div', 'tvg-ch__initial', (tv.channels[i].name || '?').slice(0, 2).toUpperCase()));
      };
      img.src = tv.channels[i].logo;
      box.appendChild(img);
    }
  }

  function markCurrent(): void {
    for (let i = 0; i < rows.length; i++) rows[i].classList.toggle('is-current', i === tv.index);
  }

  function paintNow(): void {
    const nowSec = Math.floor(Date.now() / 1000);
    for (let i = 0; i < rows.length; i++) {
      const v = nn[tv.channels[i].id];
      const p = v && v.now;
      rowNow[i].textContent = p ? hhmm(p.start) + '  ' + p.title : '';
      rowFill[i].style.width = Math.round(progress(p, nowSec) * 100) + '%';
      (rowFill[i].parentNode as HTMLElement).classList.toggle('hide', !p);
    }
  }

  function refreshNow(done?: () => void): void {
    if (Date.now() - nnAt < NOW_TTL_MS) {
      if (done) done();
      return;
    }
    tv.nowNext(function (items) {
      if (destroyed) return;
      nn = items || {};
      nnAt = Date.now();
      paintNow();
      if (done) done();
    });
  }

  // ---- programme column ----
  function scheduleEpg(id: string): void {
    if (epgTimer) clearTimeout(epgTimer);
    epgTimer = window.setTimeout(function () {
      epgTimer = 0;
      loadEpg(id);
    }, 250);
  }

  function loadEpg(id: string): void {
    const ch = tv.channels[focusedIdx];
    progHead.textContent = ch ? ch.name : '';
    if (epgCache[id]) {
      renderProgs(epgCache[id]);
      return;
    }
    empty(progBody);
    progBody.appendChild(el('div', 'tvg-pr__empty', '…'));
    tv.epg(id, function (items) {
      if (destroyed) return;
      epgCache[id] = items || [];
      if (tv.channels[focusedIdx] && tv.channels[focusedIdx].id === id) renderProgs(epgCache[id]);
    });
  }

  let nowRow: HTMLElement | null = null;
  function renderProgs(items: TvProgram[]): void {
    empty(progBody);
    nowRow = null;
    if (!items.length) {
      progBody.appendChild(el('div', 'tvg-pr__empty', t('tv.no_epg')));
      progScroll.reset();
      return;
    }
    const nowSec = Math.floor(Date.now() / 1000);
    let lastDay = '';
    for (let i = 0; i < items.length; i++) {
      (function (p: TvProgram) {
        const day = dayLabel(p.start);
        if (day !== lastDay) {
          lastDay = day;
          progBody.appendChild(el('div', 'tvg-pr__day', day));
        }
        const isNow = p.start <= nowSec && p.stop > nowSec;
        const row = el('div', 'tvg-pr selector' + (isNow ? ' is-now' : p.stop <= nowSec ? ' is-past' : ''));
        row.appendChild(el('div', 'tvg-pr__time', hhmm(p.start)));
        const txt = el('div', 'tvg-pr__text');
        txt.appendChild(el('div', 'tvg-pr__title', p.title));
        if (isNow) {
          const bar = el('div', 'tvg-pr__bar');
          const fill = el('div', 'tvg-pr__fill');
          fill.style.width = Math.round(progress(p, nowSec) * 100) + '%';
          bar.appendChild(fill);
          txt.appendChild(bar);
        }
        if (p.desc) {
          const d = el('div', 'tvg-pr__desc hide', p.desc);
          txt.appendChild(d);
          on(row, 'hover:enter', function () {
            d.classList.toggle('hide');
            progScroll.update(row);
          });
        }
        row.appendChild(txt);
        progBody.appendChild(row);
        if (isNow) nowRow = row;
      })(items[i]);
    }
    progScroll.reset();
    if (nowRow) progScroll.immediate(nowRow, true); // open on what is on now, not on yesterday evening
    if (Controller.enabled().name === 'player_tv_prog') {
      Controller.collectionSet(progScroll.render());
      Controller.collectionFocus(nowRow || false, progScroll.render());
    }
  }

  // ---- info bar ----
  function showInfo(index: number, typed?: string): void {
    const ch = tv.channels[index];
    if (!ch) return;
    empty(infoLogo);
    if (ch.logo) {
      const img = document.createElement('img');
      img.alt = '';
      img.src = ch.logo;
      infoLogo.appendChild(img);
    } else {
      infoLogo.appendChild(el('div', 'tvg-ch__initial', (ch.name || '?').slice(0, 2).toUpperCase()));
    }
    infoNum.textContent = typed != null ? typed + '_' : String(index + 1);
    infoName.textContent = ch.name + (ch.quality ? ' · ' + ch.quality : '');
    const v = nn[ch.id];
    const nowSec = Math.floor(Date.now() / 1000);
    infoNow.textContent = v && v.now ? hhmm(v.now.start) + '–' + hhmm(v.now.stop) + '  ' + v.now.title : '';
    infoFill.style.width = Math.round(progress(v && v.now, nowSec) * 100) + '%';
    infoBar.classList.toggle('hide', !(v && v.now));
    infoNext.textContent = v && v.next ? t('tv.next') + ': ' + hhmm(v.next.start) + '  ' + v.next.title : '';
    info.classList.remove('hide');
    if (infoTimer) clearTimeout(infoTimer);
    infoTimer = window.setTimeout(function () {
      infoTimer = 0;
      info.classList.add('hide');
    }, INFO_MS);
  }

  // ---- controller modes ----
  Controller.add('player_tv', {
    toggle: function () {
      Controller.collectionSet(listScroll.render());
      Controller.collectionFocus(rows[focusedIdx] || rows[tv.index] || false, listScroll.render());
    },
    up: function () {
      Controller.moveOr('up');
    },
    down: function () {
      Controller.moveOr('down');
    },
    right: function () {
      if (progBody.querySelector('.selector')) host.setMode('player_tv_prog');
    },
    left: function () {
      close();
    },
    enter: function () {
      const f = Navigator.getFocusedElement();
      if (f) {
        const i = rows.indexOf(f);
        if (i >= 0) {
          close();
          if (i !== tv.index) host.switchTo(i);
        }
      }
    },
    back: function () {
      close();
    },
  });
  Controller.add('player_tv_prog', {
    toggle: function () {
      Controller.collectionSet(progScroll.render());
      Controller.collectionFocus(nowRow || false, progScroll.render());
    },
    up: function () {
      Controller.moveOr('up');
    },
    down: function () {
      Controller.moveOr('down');
    },
    left: function () {
      host.setMode('player_tv');
    },
    enter: function () {
      const f = Navigator.getFocusedElement();
      if (f) {
        const d = f.querySelector('.tvg-pr__desc');
        if (d) {
          d.classList.toggle('hide');
          progScroll.update(f);
        }
      }
    },
    back: function () {
      host.setMode('player_tv');
    },
  });

  function openGuide(): void {
    if (open || destroyed) return;
    open = true;
    info.classList.add('hide');
    focusedIdx = tv.index;
    markCurrent();
    revealLogos(tv.index);
    overlay.classList.remove('hide');
    listScroll.reset();
    refreshNow();
    loadEpg(tv.channels[tv.index] ? tv.channels[tv.index].id : '');
    host.setMode('player_tv');
    if (rows[tv.index]) listScroll.immediate(rows[tv.index], true);
  }

  function close(): void {
    if (!open) return;
    open = false;
    overlay.classList.add('hide');
    host.setMode('player');
  }

  function commitDigits(): void {
    digitsTimer = 0;
    const n = parseInt(digits, 10);
    digits = '';
    if (n >= 1 && n <= tv.channels.length) {
      if (n - 1 !== tv.index) host.switchTo(n - 1);
      else showInfo(tv.index);
    } else {
      info.classList.add('hide');
    }
  }

  const guide: TvGuide = {
    open: openGuide,
    close: close,
    isOpen: function () {
      return open;
    },
    zap: function (dir) {
      if (!tv.channels.length) return;
      host.switchTo((tv.index + dir + tv.channels.length) % tv.channels.length);
    },
    digit: function (n) {
      if (digits.length >= 4) digits = '';
      digits += String(n);
      showInfo(tv.index, digits);
      if (digitsTimer) clearTimeout(digitsTimer);
      digitsTimer = window.setTimeout(commitDigits, DIGITS_MS);
    },
    switched: function (index) {
      tv.index = index;
      markCurrent();
      refreshNow(function () {
        if (!open) showInfo(index);
      });
    },
    destroy: function () {
      destroyed = true;
      if (epgTimer) clearTimeout(epgTimer);
      if (infoTimer) clearTimeout(infoTimer);
      if (digitsTimer) clearTimeout(digitsTimer);
      Controller.remove('player_tv');
      Controller.remove('player_tv_prog');
      listScroll.destroy();
      progScroll.destroy();
      epgCache = {};
    },
  };

  markCurrent();
  refreshNow(function () {
    showInfo(tv.index);
  });
  return guide;
}
