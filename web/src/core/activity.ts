// Screen stack ("Activity" in Lampa's terms).
//
// push() creates a new screen container over the previous one (previous is
// paused + hidden, not destroyed, so back() is instant with no re-fetch).
// Beyond maxActivities the oldest is torn down for real (DOM removed,
// destroy() run) to bound memory on weak TV hardware. back() destroys the
// top and resumes the one beneath. replaceRoot() tears the whole stack down
// and starts fresh (used for menu-level navigation).

import Controller from './controller';

// Name of the throwaway controller that owns input between push() and the new
// screen's first Controller.toggle(). Without it the PREVIOUS (now hidden)
// screen's controller stays active while the new one is still fetching, so an
// OK during the spinner fires hover:enter on an invisible card: a second title
// pushed on top, or a second replaceRoot with a double fetch.
const PENDING = 'activity_pending';

export interface ScreenInstance {
  destroy?(): void;
  pause?(): void;
  resume?(): void;
}

export type RenderFn = (container: HTMLElement) => ScreenInstance | void;

interface Entry {
  container: HTMLElement;
  screen: ScreenInstance | null;
}

export class Activity {
  private root: HTMLElement;
  private stack: Entry[];
  private maxActivities: number;

  constructor(root: HTMLElement, maxActivities: number = 5) {
    this.root = root;
    this.stack = [];
    this.maxActivities = maxActivities;
  }

  push(render: RenderFn): HTMLElement {
    const prev = this.stack[this.stack.length - 1];
    if (prev) {
      if (prev.screen && prev.screen.pause) {
        prev.screen.pause();
      }
      prev.container.style.display = 'none';
    }

    const container = document.createElement('div');
    container.className = 'activity';
    this.root.appendChild(container);

    const self = this;
    Controller.add(PENDING, {
      toggle: function () {
        Controller.clear(); // drop the hidden screen's select_active too
      },
      back: function () {
        self.back();
      },
    });
    Controller.toggle(PENDING);

    let screen: ScreenInstance | null = null;
    try {
      screen = render(container) || null;
    } catch (err) {
      // A throwing screen must not leave a black hole: put the previous one
      // back exactly as it was, then let the error surface.
      if (container.parentNode) container.parentNode.removeChild(container);
      if (prev) {
        prev.container.style.display = '';
        if (prev.screen && prev.screen.resume) prev.screen.resume();
      }
      throw err;
    }
    this.stack.push({ container: container, screen: screen });

    while (this.stack.length > this.maxActivities) {
      // Evict the second-oldest, never index 0: the root (Home) must survive so
      // a deep chain (home→title→sources→…) can still Back all the way down to
      // it instead of hitting an empty stack and exiting. See stability audit #12.
      const evicted = this.stack.splice(1, 1)[0];
      if (evicted) {
        this.destroyEntry(evicted);
      }
    }

    return container;
  }

  replaceRoot(render: RenderFn): HTMLElement {
    while (this.stack.length) {
      const entry = this.stack.pop();
      if (entry) {
        this.destroyEntry(entry);
      }
    }
    return this.push(render);
  }

  back(): boolean {
    if (this.stack.length <= 1) {
      return false;
    }
    const top = this.stack.pop();
    if (top) {
      this.destroyEntry(top);
    }
    const newTop = this.stack[this.stack.length - 1];
    if (newTop) {
      newTop.container.style.display = '';
      if (newTop.screen && newTop.screen.resume) {
        newTop.screen.resume();
      }
    }
    return true;
  }

  size(): number {
    return this.stack.length;
  }

  private destroyEntry(entry: Entry): void {
    if (entry.screen && entry.screen.destroy) {
      entry.screen.destroy();
    }
    if (entry.container.parentNode) {
      entry.container.parentNode.removeChild(entry.container);
    }
  }
}
