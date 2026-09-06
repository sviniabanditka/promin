// Scroll — a port of Lampa's src/interaction/scroll.js, TV branch only.
//
// The whole point (per docs/frontend.md and the owner's brief): a lane
// is NOT scrolled with native scrollLeft/scrollTop. Instead the inner
// `.scroll__body` is moved with translate3d and a CSS transition on
// `transform` (see styles.css `.scroll__body { transition: transform .3s }`,
// copied from Lampa's scroll.scss). Old Tizen/webOS choke on native smooth
// scroll but handle translate3d fine.
//
// We keep only the pieces the Home screen needs: the translate math
// (getElementPosition + maxOffset, verbatim from the original), update()
// / immediate() / reset(), and the onScroll/onAnimateEnd hooks. The
// touch/mouse-wheel input branches from the original are dropped — remote
// input drives everything through the Navigator/Controller instead.

import Template from './template';

// Pointer hover must move the focus RING only, never scroll (the "scroll follows
// the cursor / pages while moving the mouse" bug). onHover (pointer.ts) arms this
// around Navigator.focus(); the whole focus dispatch is synchronous (nav.ts
// send()), so update() below sees it and skips the scroll, then onHover clears it
// in a finally. Never armed on TV (no mouseover) → remote scroll unchanged.
let hoverNoScroll = false;
export function setHoverNoScroll(v: boolean): void {
  hoverNoScroll = v;
}

// Extra px kept between a top/left-aligned focused element and the scroll
// container edge, so its ::after focus ring (~0.5rem = 8px outside the card)
// stays inside overflow:hidden. ~12px covers the 0.5rem inset + 0.3rem border.
const FOCUS_EDGE_MARGIN = 12;

export interface ScrollParams {
  horizontal?: boolean;
  mask?: boolean;
  over?: boolean;
  nopadding?: boolean;
  notransition?: boolean;
  step?: number;
  scroll_by_item?: boolean;
  end_ratio?: number;
}

export class Scroll {
  private params: ScrollParams;
  private html: HTMLElement;
  private _body: HTMLElement;
  private content: HTMLElement;
  private scroll_position = 0;

  // The single live transitionend handler for the current animation (deduped so
  // two startScroll calls inside one 0.3s window can't stack listeners → double
  // onAnimateEnd → double lazy-append). null when idle.
  private pendingEnd: ((e: Event) => void) | null = null;
  // Fallback settle: with `body.legacy-tv * { transition: none }` (or a body hidden
  // mid-animation) transitionend never fires and `animating` stuck true forever —
  // pointer.ts then swallowed every tap on the lane. 0.3s transition + margin.
  private pendingTimer = 0;
  // True while a translate transition is in flight (a touch drag reads this to
  // catch a coasting list and stop it).
  animating = false;

  onScroll: ((position: number) => void) | null = null;
  onAnimateEnd: (() => void) | null = null;

  get horizontal(): boolean {
    return !!this.params.horizontal;
  }

  // Registry so Controller.focus can auto-scroll the nearest Scroll to any
  // focused element WITHOUT every screen manually wiring scroll.update into
  // each hover:focus (the source of the title/sources "dead scroll" bugs).
  static byBody: Array<{ body: HTMLElement; scroll: Scroll }> = [];

  // Called on every focus / wheel / touch move — keep it alloc-free. Just find
  // the match (no per-call live[] rebuild + contains() over every entry). The
  // registry is compacted lazily on push (constructor) instead.
  static forBody(body: HTMLElement): Scroll | null {
    for (let i = 0; i < Scroll.byBody.length; i++) {
      if (Scroll.byBody[i].body === body) return Scroll.byBody[i].scroll;
    }
    return null;
  }

  // Drop registry entries whose DOM is gone (destroyed screens) so the list
  // stays bounded. contains() (not isConnected — absent on Chromium ~47).
  private static compact(): void {
    const live: Array<{ body: HTMLElement; scroll: Scroll }> = [];
    for (let i = 0; i < Scroll.byBody.length; i++) {
      if (document.documentElement.contains(Scroll.byBody[i].body)) live.push(Scroll.byBody[i]);
    }
    Scroll.byBody = live;
  }

  constructor(params: ScrollParams = {}) {
    this.params = params;

    this.html = Template.scroll();
    this._body = this.html.querySelector('.scroll__body') as HTMLElement;
    this.content = this.html.querySelector('.scroll__content') as HTMLElement;
    Scroll.compact(); // prune dead screens on each new Scroll (not on the hot forBody path)
    Scroll.byBody.push({ body: this._body, scroll: this });

    if (params.horizontal) this.html.classList.add('scroll--horizontal');
    if (params.mask) this.html.classList.add('scroll--mask');
    if (params.over) this.html.classList.add('scroll--over');
    if (params.nopadding) this.html.classList.add('scroll--nopadding');
    if (params.notransition) this._body.classList.add('notransition');
  }

  // Largest (most negative) offset we may translate to before running past
  // the content — verbatim from scroll.js maxOffset().
  // Content padding is static for a Scroll, yet it was read through
  // getComputedStyle (a style recalc) on every update/maxOffset — 2–4× per
  // keypress with nested scrolls. Cached once the element has layout.
  private padCache = -1;
  private contentPadding(): number {
    if (this.padCache >= 0) return this.padCache;
    const horizontal = !!this.params.horizontal;
    const p =
      parseInt(
        window.getComputedStyle(this.content, null).getPropertyValue('padding-' + (horizontal ? 'left' : 'top')),
        10
      ) || 0;
    if (this.html.offsetParent !== null) this.padCache = p; // only trust a laid-out element
    return p;
  }

  private maxOffset(offset: number): number {
    const horizontal = !!this.params.horizontal;
    const w = horizontal ? this.html.offsetWidth : this.html.offsetHeight;
    const p = this.contentPadding();
    const s = horizontal ? this._body.scrollWidth : this._body.scrollHeight;

    offset = Math.min(0, offset);
    offset = Math.max(-(Math.max(s + p * 2, w) - w), offset);

    return offset;
  }

  // Translate offset that brings `elem` into view (or centered when
  // tocenter) — verbatim from scroll.js getElementPosition().
  private getElementPosition(elem: HTMLElement, tocenter?: boolean): number {
    const horizontal = !!this.params.horizontal;
    const dir = horizontal ? 'left' : 'top';
    const siz = horizontal ? 'offsetWidth' : 'offsetHeight';

    const target = elem;

    const p = tocenter ? this.contentPadding() : 0;

    const ofs_elm = (target.getBoundingClientRect() as unknown as { [k: string]: number })[dir];
    const ofs_box = (this._body.getBoundingClientRect() as unknown as { [k: string]: number })[dir];
    // Leave a small margin when aligning the top/left edge, so the focus ring
    // (drawn on ::after ~0.5rem outside the card) isn't clipped by the
    // scroll container's overflow:hidden. Only for edge-align (not center).
    const edgeMargin = tocenter ? 0 : FOCUS_EDGE_MARGIN;
    const center = ofs_box + edgeMargin + (tocenter ? this.content[siz] / 2 - target[siz] / 2 - p : 0);
    let scrl = Math.min(0, center - ofs_elm);
    scrl = this.maxOffset(scrl);

    return scrl;
  }

  private translateScroll(): void {
    const horizontal = !!this.params.horizontal;
    const x = Math.round(horizontal ? this.scroll_position : 0);
    const y = Math.round(horizontal ? 0 : this.scroll_position);
    const value = 'translate3d(' + x + 'px, ' + y + 'px, 0px)';
    this._body.style['transform' as any] = value;
    (this._body.style as unknown as { [k: string]: string })['-webkit-transform'] = value;
  }

  private startScroll(to_position: number): void {
    if (this.scroll_position === to_position) {
      return;
    }
    // The painted transform is Math.round()ed. If old and new positions round to
    // the same px, the transform string doesn't change → no transitionend →
    // `animating` would stay true forever. Settle synchronously instead.
    if (Math.round(this.scroll_position) === Math.round(to_position)) {
      this.scroll_position = to_position;
      this.animating = false;
      this.fireAnimateEnd();
      return;
    }

    this.scroll_position = to_position;
    this.translateScroll();

    // Fires once when the transform transition settles; used by the vertical
    // Main scroll to lazy-append the next batch of lines (parity with the
    // original's webkitTransitionEnd handler). Dedupe: drop any prior pending
    // handler first, so a retarget mid-animation replaces (not stacks) it.
    if (this.pendingEnd) this._body.removeEventListener('transitionend', this.pendingEnd);
    const self = this;
    const onEnd = function (e: Event) {
      // Only our own transform settling ends the scroll — a child's transition
      // (card focus transform) bubbles the same event and would clear `animating`
      // before the scroll actually finished.
      if (e.target !== self._body) return;
      self._body.removeEventListener('transitionend', onEnd);
      self.pendingEnd = null;
      if (self.pendingTimer) window.clearTimeout(self.pendingTimer);
      self.pendingTimer = 0;
      self.animating = false;
      if (self.onAnimateEnd) self.onAnimateEnd();
    };
    this.pendingEnd = onEnd;
    this.animating = true;
    this._body.addEventListener('transitionend', onEnd);
    if (this.pendingTimer) window.clearTimeout(this.pendingTimer);
    this.pendingTimer = window.setTimeout(function () {
      onEnd({ target: self._body } as unknown as Event);
    }, 400);

    if (this.onScroll) this.onScroll(-this.scroll_position);
  }

  // Pixel-delta scroll — the entry point wheel/touch drive (every other public
  // mover takes an element). Positive delta scrolls content up/left (finger/
  // wheel-down convention). animate=false = finger-follow (no transition, and
  // therefore NO transitionend → the caller must settle via an animated scroll
  // or an explicit fireAnimateEnd()).
  scrollBy(deltaPx: number, animate: boolean): void {
    const target = this.maxOffset(this.scroll_position - deltaPx);
    if (animate) {
      this.startScroll(target);
      return;
    }
    if (this.scroll_position === target) return;
    this._body.classList.add('notransition');
    this.scroll_position = target;
    this.translateScroll();
    if (this.onScroll) this.onScroll(-this.scroll_position);
  }

  // Halt a coasting animation in place (touch-catch). Reads the live translate
  // off the computed matrix so the stop point is exactly where the finger lands
  // — translate3d serializes as matrix3d, whose translate is m41/m42.
  stop(): void {
    if (!this.animating) return;
    const tr = window.getComputedStyle(this._body).getPropertyValue('transform');
    if (tr && tr !== 'none') {
      const M = (window as unknown as { WebKitCSSMatrix?: unknown; DOMMatrix?: unknown }).WebKitCSSMatrix ||
        (window as unknown as { DOMMatrix?: unknown }).DOMMatrix;
      if (M) {
        const mx = new (M as { new (s: string): { m41: number; m42: number } })(tr);
        this.scroll_position = this.horizontal ? mx.m41 : mx.m42;
      }
    }
    if (this.pendingEnd) {
      this._body.removeEventListener('transitionend', this.pendingEnd);
      this.pendingEnd = null;
    }
    this.animating = false;
    const self = this;
    this._body.classList.add('notransition');
    this.translateScroll();
    window.setTimeout(function () {
      self._body.classList.remove('notransition');
    }, 5);
  }

  // Manually fire the settle hook — used when a finger-follow drag ends without
  // a trailing animated scroll (which would otherwise never fire transitionend).
  fireAnimateEnd(): void {
    if (this.onAnimateEnd) this.onAnimateEnd();
  }

  // Animated scroll so `elem` is visible (or centered when tocenter).
  update(elem: HTMLElement, tocenter?: boolean): void {
    // Hover: move the ring, don't scroll. This is the single funnel both
    // autoScrollTo and every per-screen hover:focus update() land in.
    if (hoverNoScroll) return;
    this.startScroll(this.getElementPosition(elem, tocenter));
  }

  // Same target as update() but without the transition.
  immediate(elem: HTMLElement, tocenter?: boolean): void {
    const self = this;
    if (this.pendingEnd) {
      this._body.removeEventListener('transitionend', this.pendingEnd);
      this.pendingEnd = null;
    }
    this.animating = false;
    this._body.classList.add('notransition');
    this.scroll_position = this.getElementPosition(elem, tocenter);
    this.translateScroll();
    window.setTimeout(function () {
      self._body.classList.remove('notransition');
    }, 5);
  }

  append(object: HTMLElement): void {
    this._body.appendChild(object);
  }

  body(): HTMLElement {
    return this._body;
  }

  render(): HTMLElement {
    return this.html;
  }

  position(): number {
    return this.scroll_position;
  }

  reset(): void {
    if (this.pendingEnd) {
      this._body.removeEventListener('transitionend', this.pendingEnd);
      this.pendingEnd = null;
    }
    this.animating = false;
    this.scroll_position = 0;
    this.translateScroll();
  }

  destroy(): void {
    if (this.html.parentNode) {
      this.html.parentNode.removeChild(this.html);
    }
  }
}
