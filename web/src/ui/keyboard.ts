// Reusable on-screen keyboard (remote-driven), extracted verbatim from the
// search screen so both Search and Login share one grid-navigation
// implementation. Layouts: uk/ru ЙЦУКЕН, latin QWERTY, a digits row, and
// space / delete / clear / layout-toggle control keys.
//
// Navigation is index-based over the key grid (row/col), NOT the geometric
// Navigator — rows have different lengths and key widths, which made the
// geometric navigator skip rows. Up/down snap to the nearest key by X.
//
// The owner supplies edge handlers (what happens when Left is pressed at
// column 0, or Right at the last column) and an onChange callback fired after
// every edit. The keyboard owns the text value.
//
// ES5 target (swc): no async/await, no for-of, no spread, no Array.find.

import { Navigator } from '../core/nav';
import Controller, { on } from '../core/controller';
import { t } from '../core/i18n';
import { el, empty } from './dom';

type LayoutId = 'uk' | 'ru' | 'lat';

const LAYOUTS: { [id: string]: string[][] } = {
  uk: [
    ['Й', 'Ц', 'У', 'К', 'Е', 'Н', 'Г', 'Ш', 'Щ', 'З', 'Х', 'Ї'],
    ['Ф', 'І', 'В', 'А', 'П', 'Р', 'О', 'Л', 'Д', 'Ж', 'Є', 'Ґ'],
    ['Я', 'Ч', 'С', 'М', 'И', 'Т', 'Ь', 'Б', 'Ю'],
  ],
  ru: [
    ['Й', 'Ц', 'У', 'К', 'Е', 'Н', 'Г', 'Ш', 'Щ', 'З', 'Х', 'Ъ'],
    ['Ф', 'Ы', 'В', 'А', 'П', 'Р', 'О', 'Л', 'Д', 'Ж', 'Э', 'Ё'],
    ['Я', 'Ч', 'С', 'М', 'И', 'Т', 'Ь', 'Б', 'Ю'],
  ],
  lat: [
    ['Q', 'W', 'E', 'R', 'T', 'Y', 'U', 'I', 'O', 'P'],
    ['A', 'S', 'D', 'F', 'G', 'H', 'J', 'K', 'L'],
    ['Z', 'X', 'C', 'V', 'B', 'N', 'M'],
  ],
};

const LAYOUT_ORDER: LayoutId[] = ['uk', 'ru', 'lat'];
const LAYOUT_NEXT_LABEL: { [id: string]: string } = { uk: 'РУС', ru: 'QWERTY', lat: 'УКР' };
const DIGITS: string[] = ['1', '2', '3', '4', '5', '6', '7', '8', '9', '0'];

function loadLayout(): LayoutId {
  try {
    const v = window.localStorage.getItem('promin_kb_layout');
    if (v === 'uk' || v === 'ru' || v === 'lat') return v;
  } catch (e) {
    /* localStorage может быть недоступен */
  }
  return 'uk';
}

function saveLayout(id: LayoutId): void {
  try {
    window.localStorage.setItem('promin_kb_layout', id);
  } catch (e) {
    /* ignore */
  }
}

export interface KeyboardOptions {
  // Controller mode name this keyboard registers under (Search uses 'content').
  controllerName: string;
  // Called after every edit with the current value (character/space/delete/clear).
  onChange: (value: string) => void;
  // Left pressed at the first column (Search → menu). Optional.
  onLeftEdge?: () => void;
  // Right pressed at the last column (Search → results). Optional.
  onRightEdge?: () => void;
  // Back pressed while the value is already empty (Search/Login → router.back).
  onBackEmpty?: () => void;
  initialValue?: string;
}

export interface Keyboard {
  el: HTMLElement;
  // Register the controller mode (call again on screen resume).
  register(): void;
  // Toggle focus into this keyboard's controller mode.
  focus(): void;
  getValue(): string;
  setValue(v: string): void;
  clear(): void;
}

export function buildKeyboard(opts: KeyboardOptions): Keyboard {
  const keyboard = el('div', 'keyboard');
  const keysBox = el('div', 'keyboard__keys');
  keyboard.appendChild(keysBox);

  let value = opts.initialValue || '';
  let layout: LayoutId = loadLayout();
  let firstKey: HTMLElement | false = false;

  let keyGrid: HTMLElement[][] = [];
  let keyPos = { r: 0, c: 0 };

  function emitChange(): void {
    opts.onChange(value);
  }

  function keyCenterX(k: HTMLElement): number {
    const rect = k.getBoundingClientRect();
    return rect.left + rect.width / 2;
  }

  function focusKey(r: number, c: number): void {
    keyPos = { r: r, c: c };
    Navigator.focus(keyGrid[r][c]);
  }

  function keyboardMove(dir: 'left' | 'right' | 'up' | 'down'): boolean {
    const r = keyPos.r;
    const c = keyPos.c;
    if (dir === 'left') {
      if (c > 0) {
        focusKey(r, c - 1);
        return true;
      }
      return false; // край — уходим к onLeftEdge
    }
    if (dir === 'right') {
      if (c < keyGrid[r].length - 1) {
        focusKey(r, c + 1);
        return true;
      }
      return false; // край — уходим к onRightEdge
    }
    const nr = dir === 'up' ? r - 1 : r + 1;
    if (nr < 0 || nr >= keyGrid.length) return dir === 'down' ? true : false;
    const x = keyCenterX(keyGrid[r][c]);
    let best = 0;
    let bestDist = Infinity;
    for (let i = 0; i < keyGrid[nr].length; i++) {
      const d = Math.abs(keyCenterX(keyGrid[nr][i]) - x);
      if (d < bestDist) {
        bestDist = d;
        best = i;
      }
    }
    focusKey(nr, best);
    return true;
  }

  function addKeyToRow(rowEls: HTMLElement[], k: HTMLElement): void {
    const r = keyGrid.length;
    const c = rowEls.length;
    on(k, 'hover:focus', function () {
      keyPos = { r: r, c: c };
    });
    rowEls.push(k);
  }

  function key(label: string, cls: string, action: () => void): HTMLElement {
    const k = el('div', 'key selector ' + cls, label);
    on(k, 'hover:enter', action);
    return k;
  }

  function appendChar(ch: string): void {
    value += ch;
    emitChange();
  }

  function deleteChar(): void {
    if (value.length) {
      value = value.slice(0, value.length - 1);
      emitChange();
    }
  }

  function clearValue(): void {
    value = '';
    emitChange();
  }

  function rebuildKeyboard(): void {
    empty(keysBox);
    firstKey = false;
    keyGrid = [];
    keyPos = { r: 0, c: 0 };

    const rows = LAYOUTS[layout];
    for (let r = 0; r < rows.length; r++) {
      const rowBox = el('div', 'keyboard__row');
      const rowEls: HTMLElement[] = [];
      for (let i = 0; i < rows[r].length; i++) {
        const ch = rows[r][i];
        const k = key(
          ch,
          'key--char',
          (function (c: string) {
            return function () {
              appendChar(c);
            };
          })(ch)
        );
        if (!firstKey) firstKey = k;
        addKeyToRow(rowEls, k);
        rowBox.appendChild(k);
      }
      keyGrid.push(rowEls);
      keysBox.appendChild(rowBox);
    }

    const digitsBox = el('div', 'keyboard__row keyboard__grid--digits');
    const digitEls: HTMLElement[] = [];
    for (let i = 0; i < DIGITS.length; i++) {
      const d = DIGITS[i];
      const k = key(
        d,
        'key--char',
        (function (c: string) {
          return function () {
            appendChar(c);
          };
        })(d)
      );
      addKeyToRow(digitEls, k);
      digitsBox.appendChild(k);
    }
    keyGrid.push(digitEls);
    keysBox.appendChild(digitsBox);

    const controls = el('div', 'keyboard__row keyboard__grid--controls');
    const controlEls: HTMLElement[] = [];
    let k = key(LAYOUT_NEXT_LABEL[layout], 'key--wide', function () {
      layout = LAYOUT_ORDER[(LAYOUT_ORDER.indexOf(layout) + 1) % LAYOUT_ORDER.length];
      saveLayout(layout);
      rebuildKeyboard();
      Controller.collectionSet(keyboard);
      Controller.collectionFocus(firstKey || false, keyboard);
    });
    addKeyToRow(controlEls, k);
    controls.appendChild(k);

    k = key(t('search.space'), 'key--space', function () {
      appendChar(' ');
    });
    addKeyToRow(controlEls, k);
    controls.appendChild(k);

    k = key('⌫', 'key--wide', function () {
      deleteChar();
    });
    addKeyToRow(controlEls, k);
    controls.appendChild(k);

    k = key(t('search.clear'), 'key--wide', function () {
      clearValue();
    });
    addKeyToRow(controlEls, k);
    controls.appendChild(k);

    keyGrid.push(controlEls);
    keysBox.appendChild(controls);
  }

  const controller = {
    toggle: function () {
      Controller.collectionSet(keyboard);
      Controller.collectionFocus(firstKey || false, keyboard);
    },
    left: function () {
      if (!keyboardMove('left') && opts.onLeftEdge) opts.onLeftEdge();
    },
    right: function () {
      if (!keyboardMove('right') && opts.onRightEdge) opts.onRightEdge();
    },
    up: function () {
      keyboardMove('up');
    },
    down: function () {
      keyboardMove('down');
    },
    back: function () {
      if (value.length) clearValue();
      else if (opts.onBackEmpty) opts.onBackEmpty();
    },
  };

  function register(): void {
    Controller.add(opts.controllerName, controller);
  }

  function focus(): void {
    Controller.toggle(opts.controllerName);
  }

  rebuildKeyboard();
  register();

  return {
    el: keyboard,
    register: register,
    focus: focus,
    getValue: function () {
      return value;
    },
    setValue: function (v: string) {
      value = v || '';
    },
    clear: clearValue,
  };
}
