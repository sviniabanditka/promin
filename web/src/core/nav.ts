// Spatial navigator — a letter-for-letter TypeScript port of Lampa's
// public/vender/navigator/navigator.js (SpatialNavigator).
//
// The geometry is exactly the original's: on every directional key the
// candidate rects are split into 9 regions relative to the focused rect
// (_partition), then ranked by the same chain of distance functions
// (_getDistanceFunction) in the same priority order (navigate()). We keep
// straightOnly=true and straightOverlapThreshold=0.5 like upstream, so the
// oblique region is dropped and off-axis elements only count when they
// overlap the straight lane by >= 50%.
//
// Only cosmetic changes vs. the original: the prototype object became a
// class, the collection is a typed HTMLElement[], and the event system
// (follow/send) is kept for the 'focus'/'unfocus' events the Controller
// subscribes to (see core/controller.ts, mirroring app.js:411).

export type Direction = 'left' | 'right' | 'up' | 'down';

interface Center {
  x: number;
  y: number;
  left: number;
  right: number;
  top: number;
  bottom: number;
}

interface Rect {
  left: number;
  top: number;
  width: number;
  height: number;
  right: number;
  bottom: number;
  center: Center;
  element: HTMLElement;
}

interface PrioritySet {
  group: Rect[];
  distance: Array<(rect: Rect) => number>;
}

type Listener = (event: { target: SpatialNavigator; elem: HTMLElement }) => void;

export class SpatialNavigator {
  // Limit navigation to vertical/horizontal only; oblique targets ignored.
  straightOnly = true;
  // How much an off-axis element must overlap the straight lane (0..1).
  straightOverlapThreshold = 0.5;
  // true: hidden .selector elements (buttons toggled off via .hide, unavailable
  // tabs/filters) are excluded from spatial navigation. The whole app manages
  // availability with .hide and assumes hidden = non-navigable (player buttons,
  // sources tabs, catalog filters); with this false they kept a 0×0 rect at
  // (0,0) and trapped focus in the corner. Must stay true.
  ignoreHiddenElement = true;
  silent = false;

  _collection: HTMLElement[] = [];
  private _focus: HTMLElement | null = null;
  private _listeners: { [type: string]: Listener[] } = {};

  // ---- event system (follow/send) -----------------------------------

  follow(type: string, listener: Listener): void {
    if (this._listeners[type] === undefined) {
      this._listeners[type] = [];
    }
    if (this._listeners[type].indexOf(listener) === -1) {
      this._listeners[type].push(listener);
    }
  }

  send(type: string, event: { elem: HTMLElement }): void {
    const listenerArray = this._listeners[type];
    if (listenerArray !== undefined) {
      const full = { target: this, elem: event.elem };
      const array = listenerArray.slice(0);
      for (let i = 0, l = array.length; i < l; i++) {
        array[i].call(this, full);
      }
    }
  }

  // ---- geometry -----------------------------------------------------

  private _getRect(elem: HTMLElement): Rect | null {
    if (!this._isNavigable(elem)) {
      return null;
    }

    let base: { left: number; top: number; width: number; height: number };
    if (elem.getBoundingClientRect) {
      const cr = elem.getBoundingClientRect();
      base = { left: cr.left, top: cr.top, width: cr.width, height: cr.height };
    } else {
      return null;
    }

    const center: Center = {
      x: base.left + Math.floor(base.width / 2),
      y: base.top + Math.floor(base.height / 2),
      left: 0,
      right: 0,
      top: 0,
      bottom: 0,
    };
    center.left = center.right = center.x;
    center.top = center.bottom = center.y;

    return {
      left: base.left,
      top: base.top,
      width: base.width,
      height: base.height,
      right: base.left + base.width,
      bottom: base.top + base.height,
      center: center,
      element: elem,
    };
  }

  private _getAllRects(excludedElem: HTMLElement): Rect[] {
    const rects: Rect[] = [];
    const self = this;
    this._collection.forEach(function (elem) {
      if (!excludedElem || excludedElem !== elem) {
        const rect = self._getRect(elem);
        if (rect) {
          rects.push(rect);
        }
      }
    });
    return rects;
  }

  private _isNavigable(elem: HTMLElement): boolean {
    if (this.ignoreHiddenElement && elem instanceof HTMLElement) {
      // Runs for EVERY collection element on every key press (a catalog grid is
      // 200+ cards). offsetWidth/Height are the cheap reads and already cover
      // display:none (a 0×0 box); getComputedStyle — a forced style recalc — is
      // only for the rare visibility:hidden element that still has a box.
      // No getComputedStyle at all: a 0×0 box already covers display:none, and
      // nothing focusable in styles.css uses visibility:hidden (only a PIN
      // spacer cell and a ::before). The style recalc ran for every visible
      // element on every keypress — 200–600 on a catalog grid.
      if (elem.offsetWidth <= 0 && elem.offsetHeight <= 0) return false;
      if (elem.hasAttribute('aria-hidden')) return false;
    }
    return true;
  }

  // Split rects into 9 groups relative to targetRect (see the diagram in
  // the original). Corner groups spill into the adjacent straight group
  // when they overlap the straight lane by >= threshold.
  private _partition(rects: Rect[], targetRect: { left: number; right: number; top: number; bottom: number; width?: number; height?: number }): Rect[][] {
    const groups: Rect[][] = [[], [], [], [], [], [], [], [], []];

    let threshold = this.straightOverlapThreshold;
    if (threshold > 1 || threshold < 0) {
      threshold = 0.5;
    }

    // When partitioning around a center point (internal groups) width/height
    // are 0; the original relies on left==right==top==bottom there.
    const width = targetRect.width !== undefined ? targetRect.width : 0;
    const height = targetRect.height !== undefined ? targetRect.height : 0;

    rects.forEach(function (rect) {
      const center = rect.center;
      let x: number;
      let y: number;
      let groupId: number;

      if (center.x < targetRect.left) {
        x = 0;
      } else if (center.x <= targetRect.right) {
        x = 1;
      } else {
        x = 2;
      }

      if (center.y < targetRect.top) {
        y = 0;
      } else if (center.y <= targetRect.bottom) {
        y = 1;
      } else {
        y = 2;
      }

      groupId = y * 3 + x;
      groups[groupId].push(rect);

      if ([0, 2, 6, 8].indexOf(groupId) !== -1) {
        if (rect.left <= targetRect.right - width * threshold) {
          if (groupId === 2) {
            groups[1].push(rect);
          } else if (groupId === 8) {
            groups[7].push(rect);
          }
        }

        if (rect.right >= targetRect.left + width * threshold) {
          if (groupId === 0) {
            groups[1].push(rect);
          } else if (groupId === 6) {
            groups[7].push(rect);
          }
        }

        if (rect.top <= targetRect.bottom - height * threshold) {
          if (groupId === 6) {
            groups[3].push(rect);
          } else if (groupId === 8) {
            groups[5].push(rect);
          }
        }

        if (rect.bottom >= targetRect.top + height * threshold) {
          if (groupId === 0) {
            groups[3].push(rect);
          } else if (groupId === 2) {
            groups[5].push(rect);
          }
        }
      }
    });

    return groups;
  }

  private _getDistanceFunction(targetRect: Rect): { [name: string]: (rect: Rect) => number } {
    return {
      nearPlumbLineIsBetter: function (rect: Rect): number {
        let d: number;
        if (rect.center.x < targetRect.center.x) {
          d = targetRect.center.x - rect.right;
        } else {
          d = rect.left - targetRect.center.x;
        }
        return d < 0 ? 0 : d;
      },
      nearHorizonIsBetter: function (rect: Rect): number {
        let d: number;
        if (rect.center.y < targetRect.center.y) {
          d = targetRect.center.y - rect.bottom;
        } else {
          d = rect.top - targetRect.center.y;
        }
        return d < 0 ? 0 : d;
      },
      nearTargetLeftIsBetter: function (rect: Rect): number {
        let d: number;
        if (rect.center.x < targetRect.center.x) {
          d = targetRect.left - rect.right;
        } else {
          d = rect.left - targetRect.left;
        }
        return d < 0 ? 0 : d;
      },
      nearTargetTopIsBetter: function (rect: Rect): number {
        let d: number;
        if (rect.center.y < targetRect.center.y) {
          d = targetRect.top - rect.bottom;
        } else {
          d = rect.top - targetRect.top;
        }
        return d < 0 ? 0 : d;
      },
      topIsBetter: function (rect: Rect): number {
        return rect.top;
      },
      bottomIsBetter: function (rect: Rect): number {
        return -1 * rect.bottom;
      },
      leftIsBetter: function (rect: Rect): number {
        return rect.left;
      },
      rightIsBetter: function (rect: Rect): number {
        return -1 * rect.right;
      },
    };
  }

  private _prioritize(priorities: PrioritySet[]): Rect | null {
    let destPriority: PrioritySet | null = null;
    for (let i = 0; i < priorities.length; i++) {
      if (priorities[i].group.length) {
        destPriority = priorities[i];
        break;
      }
    }

    if (!destPriority) {
      return null;
    }

    const dist = destPriority.distance;
    destPriority.group.sort(function (a, b) {
      return dist.reduce(function (answer, distance) {
        return answer || distance(a) - distance(b);
      }, 0);
    });

    return destPriority.group[0];
  }

  // ---- collection ---------------------------------------------------

  setCollection(collection: HTMLElement[]): void {
    this.unfocus();
    this._collection = [];
    if (collection) {
      this.multiAdd(collection);
    }
  }

  add(elem: HTMLElement): boolean {
    const index = this._collection.indexOf(elem);
    if (index >= 0) {
      return false;
    }
    this._collection.push(elem);
    return true;
  }

  multiAdd(elements: HTMLElement[]): boolean {
    let all = true;
    for (let i = 0; i < elements.length; i++) {
      if (!this.add(elements[i])) {
        all = false;
      }
    }
    return all;
  }

  remove(elem: HTMLElement): boolean {
    const index = this._collection.indexOf(elem);
    if (index < 0) {
      return false;
    }
    if (this._focus === elem) {
      this.unfocus();
    }
    this._collection.splice(index, 1);
    return true;
  }

  focus(elem?: HTMLElement): boolean {
    if (!elem && this._focus && this._isNavigable(this._focus)) {
      elem = this._focus;
    }

    if (!this._collection) {
      return false;
    }

    if (!elem) {
      const navigableElems = this._collection.filter(this._isNavigable, this);
      if (!navigableElems.length) {
        return false;
      }
      elem = navigableElems[0];
    } else if (this._collection.indexOf(elem) < 0 || !this._isNavigable(elem)) {
      return false;
    }

    this.unfocus();
    this._focus = elem;

    if (!this.silent) {
      this.send('focus', { elem: this._focus });
    }
    return true;
  }

  unfocus(): boolean {
    if (this._focus) {
      const elem = this._focus;
      this._focus = null;
      if (!this.silent) {
        this.send('unfocus', { elem: elem });
      }
    }
    return true;
  }

  getFocusedElement(): HTMLElement | null {
    return this._focus;
  }

  // ---- movement -----------------------------------------------------

  move(direction: Direction): boolean {
    if (!this._focus) {
      this.focus();
    } else {
      const elem = this.navigate(this._focus, direction);
      if (!elem) {
        return false;
      }
      this.unfocus();
      this.focus(elem);
    }
    return true;
  }

  canmove(direction: Direction): HTMLElement | false {
    if (this._focus) {
      const elem = this.navigate(this._focus, direction);
      if (elem !== null) {
        return elem;
      }
    }
    return false;
  }

  navigate(target: HTMLElement, direction: Direction): HTMLElement | null {
    if (!target || !direction || !this._collection) {
      return null;
    }

    const rects = this._getAllRects(target);
    const targetRect = this._getRect(target);
    if (!targetRect || !rects.length) {
      return null;
    }

    const distanceFunction = this._getDistanceFunction(targetRect);
    const groups = this._partition(rects, targetRect);
    const internalGroups = this._partition(groups[4], targetRect.center);

    let priorities: PrioritySet[];

    switch (direction) {
      case 'left':
        priorities = [
          {
            group: internalGroups[0].concat(internalGroups[3]).concat(internalGroups[6]),
            distance: [distanceFunction.nearPlumbLineIsBetter, distanceFunction.topIsBetter],
          },
          {
            group: groups[3],
            distance: [distanceFunction.nearPlumbLineIsBetter, distanceFunction.topIsBetter],
          },
          {
            group: groups[0].concat(groups[6]),
            distance: [
              distanceFunction.nearHorizonIsBetter,
              distanceFunction.rightIsBetter,
              distanceFunction.nearTargetTopIsBetter,
            ],
          },
        ];
        break;
      case 'right':
        priorities = [
          {
            group: internalGroups[2].concat(internalGroups[5]).concat(internalGroups[8]),
            distance: [distanceFunction.nearPlumbLineIsBetter, distanceFunction.topIsBetter],
          },
          {
            group: groups[5],
            distance: [distanceFunction.nearPlumbLineIsBetter, distanceFunction.topIsBetter],
          },
          {
            group: groups[2].concat(groups[8]),
            distance: [
              distanceFunction.nearHorizonIsBetter,
              distanceFunction.leftIsBetter,
              distanceFunction.nearTargetTopIsBetter,
            ],
          },
        ];
        break;
      case 'up':
        priorities = [
          {
            group: internalGroups[0].concat(internalGroups[1]).concat(internalGroups[2]),
            distance: [distanceFunction.nearHorizonIsBetter, distanceFunction.leftIsBetter],
          },
          {
            group: groups[1],
            distance: [distanceFunction.nearHorizonIsBetter, distanceFunction.leftIsBetter],
          },
          {
            group: groups[0].concat(groups[2]),
            distance: [
              distanceFunction.nearPlumbLineIsBetter,
              distanceFunction.bottomIsBetter,
              distanceFunction.nearTargetLeftIsBetter,
            ],
          },
        ];
        break;
      case 'down':
        priorities = [
          {
            group: internalGroups[6].concat(internalGroups[7]).concat(internalGroups[8]),
            distance: [distanceFunction.nearHorizonIsBetter, distanceFunction.leftIsBetter],
          },
          {
            group: groups[7],
            distance: [distanceFunction.nearHorizonIsBetter, distanceFunction.leftIsBetter],
          },
          {
            group: groups[6].concat(groups[8]),
            distance: [
              distanceFunction.nearPlumbLineIsBetter,
              distanceFunction.topIsBetter,
              distanceFunction.nearTargetLeftIsBetter,
            ],
          },
        ];
        break;
      default:
        return null;
    }

    if (this.straightOnly) {
      priorities.pop();
    }

    const dest = this._prioritize(priorities);
    if (!dest) {
      return null;
    }

    return dest.element;
  }
}

// Single global instance, exactly like `var Navigator = new SpatialNavigator()`
// at the bottom of the original navigator.js.
export const Navigator = new SpatialNavigator();
