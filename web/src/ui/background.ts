// Focus-driven backdrop: two stacked layers cross-fade via opacity only
// (transform/opacity are the only animatable props on weak TV GPUs — see
// docs/frontend.md). The backdrop image comes ready from the
// card DTO as a relative "/img/..." path; we request the w1280 size.

import { el } from './dom';
import { imgSize } from '../core/api';

export class Background {
  private root: HTMLElement;
  private layerA: HTMLElement;
  private layerB: HTMLElement;
  private active: HTMLElement;
  private current = '';

  constructor() {
    this.root = el('div', 'background');
    this.layerA = el('div', 'background__layer');
    this.layerB = el('div', 'background__layer');
    const shade = el('div', 'background__shade');
    this.root.appendChild(this.layerA);
    this.root.appendChild(this.layerB);
    this.root.appendChild(shade);
    this.active = this.layerA;
  }

  render(): HTMLElement {
    return this.root;
  }

  // Cross-fade to the given DTO backdrop path (or clear when empty).
  set(backdropPath: string | null | undefined): void {
    const url = backdropPath ? imgSize(backdropPath, 'w1280') : '';
    if (url === this.current) {
      return;
    }
    this.current = url;

    const next = this.active === this.layerA ? this.layerB : this.layerA;
    if (url) {
      next.style.backgroundImage = "url('" + url + "')";
      next.classList.add('visible');
    } else {
      next.classList.remove('visible');
    }
    this.active.classList.remove('visible');
    this.active = next;
  }
}
