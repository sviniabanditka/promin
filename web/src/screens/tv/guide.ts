// Shared live-TV guide widgets (docs/tv.md): a channel column (logo, number,
// name, what is on now with a progress bar) and a programme pane (the focused
// channel's schedule with day separators, the current entry highlighted, the
// focused entry's description on top). Used by the TV screen and by the live
// player's overlay, so both look and behave the same.

import { on } from '../../core/controller';
import { Scroll } from '../../core/scroll';
import { t } from '../../core/i18n';
import { getTvEpg, TvProgram, TvNowNext } from '../../core/api';
import { el, empty, pad2 } from '../../ui/dom';

export interface GuideChannel {
  id: string;
  name: string;
  logo: string;
  quality: string;
  favorite?: boolean;
}

export type NowMap = { [id: string]: TvNowNext };

export function hhmm(sec: number): string {
  const d = new Date(sec * 1000);
  return pad2(d.getHours()) + ':' + pad2(d.getMinutes());
}

export function dayLabel(sec: number): string {
  const d = new Date(sec * 1000);
  const today = new Date();
  const diff = Math.round((new Date(d.getFullYear(), d.getMonth(), d.getDate()).getTime() - new Date(today.getFullYear(), today.getMonth(), today.getDate()).getTime()) / 86400000);
  if (diff === 0) return t('tv.today');
  if (diff === 1) return t('tv.tomorrow');
  if (diff === -1) return t('tv.yesterday');
  return pad2(d.getDate()) + '.' + pad2(d.getMonth() + 1);
}

export function progress(p: TvProgram | undefined, nowSec: number): number {
  if (!p || p.stop <= p.start) return 0;
  return Math.max(0, Math.min(1, (nowSec - p.start) / (p.stop - p.start)));
}

export function nowSec(): number {
  return Math.floor(Date.now() / 1000);
}

function initial(name: string): HTMLElement {
  return el('div', 'tvg-ch__initial', (name || '?').slice(0, 2).toUpperCase());
}

// Logo box that loads its image on demand (2k rows in "all channels").
export function logoBox(ch: GuideChannel, cls: string): HTMLElement {
  const box = el('div', cls);
  if (!ch.logo) box.appendChild(initial(ch.name));
  else box.setAttribute('data-logo', ch.logo);
  return box;
}

export function loadLogo(box: HTMLElement, name: string): void {
  const src = box.getAttribute('data-logo');
  if (!src) return;
  box.removeAttribute('data-logo');
  const img = document.createElement('img');
  img.alt = '';
  img.onerror = function () {
    img.onerror = null;
    img.style.display = 'none';
    box.appendChild(initial(name));
  };
  img.src = src;
  box.appendChild(img);
}

// ---- channel column ------------------------------------------------------------

export interface ChannelListHooks {
  onFocus: (index: number) => void;
  onEnter: (index: number) => void;
  onLong?: (index: number) => void;
}

const LOGO_WINDOW = 25;

export class ChannelList {
  readonly scroll: Scroll;
  channels: GuideChannel[] = [];
  private rows: HTMLElement[] = [];
  private nowEls: HTMLElement[] = [];
  private barEls: HTMLElement[] = [];
  private fillEls: HTMLElement[] = [];
  private logoEls: HTMLElement[] = [];
  private nn: NowMap = {};
  private current = '';
  lastFocused: HTMLElement | false = false;

  constructor(private hooks: ChannelListHooks) {
    this.scroll = new Scroll({ mask: true, over: true });
  }

  render(): HTMLElement {
    return this.scroll.render();
  }

  setChannels(list: GuideChannel[]): void {
    const self = this;
    this.channels = list;
    this.rows = [];
    this.nowEls = [];
    this.barEls = [];
    this.fillEls = [];
    this.logoEls = [];
    this.lastFocused = false;
    const body = this.scroll.body();
    empty(body);
    for (let i = 0; i < list.length; i++) {
      (function (i: number) {
        const ch = list[i];
        const row = el('div', 'tvg-ch selector' + (ch.favorite ? ' is-fav' : ''));
        row.setAttribute('data-id', ch.id);
        const logo = logoBox(ch, 'tvg-ch__logo');
        row.appendChild(logo);
        row.appendChild(el('div', 'tvg-ch__num', String(i + 1)));
        const txt = el('div', 'tvg-ch__text');
        const name = el('div', 'tvg-ch__name', ch.name);
        if (ch.quality) name.appendChild(el('span', 'tvg-ch__q', ch.quality));
        txt.appendChild(name);
        const now = el('div', 'tvg-ch__now');
        txt.appendChild(now);
        const bar = el('div', 'tvg-ch__bar hide');
        const fill = el('div', 'tvg-ch__fill');
        bar.appendChild(fill);
        txt.appendChild(bar);
        row.appendChild(txt);
        row.appendChild(el('div', 'tvg-ch__star', '★'));
        on(row, 'hover:focus', function () {
          self.lastFocused = row;
          self.revealLogos(i);
          self.hooks.onFocus(i);
        });
        on(row, 'hover:enter', function () {
          self.hooks.onEnter(i);
        });
        if (self.hooks.onLong) {
          on(row, 'hover:long', function () {
            if (self.hooks.onLong) self.hooks.onLong(i);
          });
        }
        self.rows.push(row);
        self.nowEls.push(now);
        self.barEls.push(bar);
        self.fillEls.push(fill);
        self.logoEls.push(logo);
        body.appendChild(row);
      })(i);
    }
    this.scroll.reset();
    this.paintNow();
    this.setCurrent(this.current);
    this.revealLogos(0);
  }

  count(): number {
    return this.channels.length;
  }

  row(i: number): HTMLElement | null {
    return this.rows[i] || null;
  }

  indexOf(row: HTMLElement): number {
    return this.rows.indexOf(row);
  }

  indexOfId(id: string): number {
    for (let i = 0; i < this.channels.length; i++) if (this.channels[i].id === id) return i;
    return -1;
  }

  revealLogos(center: number): void {
    const from = Math.max(0, center - LOGO_WINDOW);
    const to = Math.min(this.channels.length - 1, center + LOGO_WINDOW);
    for (let i = from; i <= to; i++) loadLogo(this.logoEls[i], this.channels[i].name);
  }

  setNow(nn: NowMap): void {
    this.nn = nn || {};
    this.paintNow();
  }

  paintNow(): void {
    const now = nowSec();
    for (let i = 0; i < this.rows.length; i++) {
      const v = this.nn[this.channels[i].id];
      const p = v && v.now;
      this.nowEls[i].textContent = p ? hhmm(p.start) + '  ' + p.title : '';
      this.fillEls[i].style.width = Math.round(progress(p, now) * 100) + '%';
      this.barEls[i].classList.toggle('hide', !p);
    }
  }

  // The channel that is playing (player overlay) — highlighted.
  setCurrent(id: string): void {
    this.current = id;
    for (let i = 0; i < this.rows.length; i++) this.rows[i].classList.toggle('is-current', this.channels[i].id === id);
  }

  setFavorite(i: number, on_: boolean): void {
    if (this.channels[i]) this.channels[i].favorite = on_;
    if (this.rows[i]) this.rows[i].classList.toggle('is-fav', on_);
  }

  removeAt(i: number): void {
    const row = this.rows[i];
    if (row && row.parentNode) row.parentNode.removeChild(row);
    this.channels.splice(i, 1);
    this.rows.splice(i, 1);
    this.nowEls.splice(i, 1);
    this.barEls.splice(i, 1);
    this.fillEls.splice(i, 1);
    this.logoEls.splice(i, 1);
    for (let k = i; k < this.rows.length; k++) {
      const num = this.rows[k].querySelector('.tvg-ch__num');
      if (num) num.textContent = String(k + 1);
    }
    if (this.lastFocused === row) this.lastFocused = false;
  }

  // Element to focus when the column takes the controller: the current
  // channel, else the last focused row, else the first.
  focusTarget(): HTMLElement | false {
    if (this.current) {
      const i = this.indexOfId(this.current);
      if (i >= 0) return this.rows[i];
    }
    return this.lastFocused || this.rows[0] || false;
  }

  destroy(): void {
    this.scroll.destroy();
  }
}

// ---- programme pane ------------------------------------------------------------

export interface ProgramPaneHooks {
  onEnter: (ch: GuideChannel, p: TvProgram) => void;
  // Long-press OK on a programme row — the TV screen records it (docs/tv.md).
  // Absent (the player's overlay) → the row has no long press.
  onLong?: (ch: GuideChannel, p: TvProgram) => void;
}

export class ProgramPane {
  readonly el: HTMLElement;
  readonly scroll: Scroll;
  private head: HTMLElement;
  private headLogo: HTMLElement;
  private headName: HTMLElement;
  private headSub: HTMLElement;
  private detail: HTMLElement;
  private detailTime: HTMLElement;
  private detailTitle: HTMLElement;
  private detailDesc: HTMLElement;
  private cache: { [id: string]: TvProgram[] } = {};
  private timer = 0;
  private destroyed = false;
  channel: GuideChannel | null = null;
  nowRow: HTMLElement | null = null;
  lastFocused: HTMLElement | false = false;

  constructor(private hooks: ProgramPaneHooks) {
    this.el = el('div', 'tvp');
    this.head = el('div', 'tvp__head');
    this.headLogo = el('div', 'tvp__logo');
    this.head.appendChild(this.headLogo);
    const ht = el('div', 'tvp__head-text');
    this.headName = el('div', 'tvp__name');
    this.headSub = el('div', 'tvp__sub');
    ht.appendChild(this.headName);
    ht.appendChild(this.headSub);
    this.head.appendChild(ht);
    this.el.appendChild(this.head);
    this.detail = el('div', 'tvp__detail hide');
    this.detailTime = el('div', 'tvp__detail-time');
    this.detailTitle = el('div', 'tvp__detail-title');
    this.detailDesc = el('div', 'tvp__detail-desc');
    this.detail.appendChild(this.detailTime);
    this.detail.appendChild(this.detailTitle);
    this.detail.appendChild(this.detailDesc);
    this.el.appendChild(this.detail);
    this.scroll = new Scroll({ mask: true, over: true });
    this.el.appendChild(this.scroll.render());
  }

  // Show a channel's schedule (debounced: the column is browsed quickly).
  show(ch: GuideChannel | null, immediate?: boolean): void {
    const self = this;
    if (this.timer) {
      clearTimeout(this.timer);
      this.timer = 0;
    }
    this.channel = ch;
    empty(this.headLogo);
    if (!ch) {
      this.headName.textContent = '';
      this.headSub.textContent = '';
      empty(this.scroll.body());
      this.detail.classList.add('hide');
      return;
    }
    const logo = logoBox(ch, 'tvp__logo-box');
    loadLogo(logo, ch.name);
    this.headLogo.appendChild(logo);
    this.headName.textContent = ch.name;
    this.headSub.textContent = t('tv.live') + (ch.quality ? ' · ' + ch.quality : '');
    if (this.cache[ch.id]) {
      this.renderList(this.cache[ch.id]);
      return;
    }
    const run = function () {
      self.timer = 0;
      empty(self.scroll.body());
      self.scroll.body().appendChild(el('div', 'tvg-pr__empty', '…'));
      getTvEpg(ch.id).then(
        function (r) {
          if (self.destroyed) return;
          self.cache[ch.id] = r.items || [];
          if (self.channel && self.channel.id === ch.id) self.renderList(self.cache[ch.id]);
        },
        function () {
          if (self.destroyed) return;
          self.cache[ch.id] = [];
          if (self.channel && self.channel.id === ch.id) self.renderList([]);
        }
      );
    };
    if (immediate) run();
    else this.timer = window.setTimeout(run, 250);
  }

  private showDetail(p: TvProgram | null): void {
    if (!p) {
      this.detail.classList.add('hide');
      return;
    }
    this.detailTime.textContent = dayLabel(p.start) + ' · ' + hhmm(p.start) + '–' + hhmm(p.stop);
    this.detailTitle.textContent = p.title;
    this.detailDesc.textContent = p.desc || '';
    this.detailDesc.classList.toggle('hide', !p.desc);
    this.detail.classList.remove('hide');
  }

  private renderList(items: TvProgram[]): void {
    const self = this;
    const ch = this.channel;
    const body = this.scroll.body();
    empty(body);
    this.nowRow = null;
    this.lastFocused = false;
    if (!ch) return;
    if (!items.length) {
      body.appendChild(el('div', 'tvg-pr__empty', t('tv.no_epg')));
      this.showDetail(null);
      this.scroll.reset();
      return;
    }
    const now = nowSec();
    let lastDay = '';
    let nowP: TvProgram | null = null;
    for (let i = 0; i < items.length; i++) {
      (function (p: TvProgram) {
        const day = dayLabel(p.start);
        if (day !== lastDay) {
          lastDay = day;
          body.appendChild(el('div', 'tvg-pr__day', day));
        }
        const isNow = p.start <= now && p.stop > now;
        const row = el('div', 'tvg-pr selector' + (isNow ? ' is-now' : p.stop <= now ? ' is-past' : ''));
        row.appendChild(el('div', 'tvg-pr__time', hhmm(p.start)));
        const txt = el('div', 'tvg-pr__text');
        txt.appendChild(el('div', 'tvg-pr__title', p.title));
        if (isNow) {
          const bar = el('div', 'tvg-pr__bar');
          const fill = el('div', 'tvg-pr__fill');
          fill.style.width = Math.round(progress(p, now) * 100) + '%';
          bar.appendChild(fill);
          txt.appendChild(bar);
        }
        row.appendChild(txt);
        on(row, 'hover:focus', function () {
          self.lastFocused = row;
          self.showDetail(p);
        });
        on(row, 'hover:enter', function () {
          self.hooks.onEnter(ch, p);
        });
        if (self.hooks.onLong) {
          on(row, 'hover:long', function () {
            if (self.hooks.onLong) self.hooks.onLong(ch, p);
          });
        }
        body.appendChild(row);
        if (isNow) {
          self.nowRow = row;
          nowP = p;
        }
      })(items[i]);
    }
    this.showDetail(nowP);
    this.scroll.reset();
    if (this.nowRow) this.scroll.immediate(this.nowRow, true);
  }

  hasRows(): boolean {
    return !!this.scroll.body().querySelector('.selector');
  }

  focusTarget(): HTMLElement | false {
    return this.lastFocused || this.nowRow || (this.scroll.body().querySelector('.selector') as HTMLElement | null) || false;
  }

  destroy(): void {
    this.destroyed = true;
    if (this.timer) clearTimeout(this.timer);
    this.scroll.destroy();
  }
}

// ---- now / next cache shared by the screen and the player -----------------------

let nnCache: NowMap = {};
let nnAt = 0;
let nnPending: Promise<NowMap> | null = null;

export function loadNowNext(fetcher: () => Promise<{ items: NowMap }>): Promise<NowMap> {
  if (Date.now() - nnAt < 60000) return Promise.resolve(nnCache);
  if (nnPending) return nnPending;
  nnPending = fetcher().then(
    function (r) {
      nnCache = r.items || {};
      nnAt = Date.now();
      nnPending = null;
      return nnCache;
    },
    function () {
      nnPending = null;
      return nnCache;
    }
  );
  return nnPending;
}
