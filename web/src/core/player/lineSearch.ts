// "Find a line" inside the film: the subtitle track is already loaded, so the
// viewer types a phrase and jumps to the moment it is said. Lives outside
// player/index.ts on purpose — it is a self-contained overlay with two
// controller modes (keyboard | results), and index.ts is big enough.

import Controller, { on } from '../controller';
import { t } from '../i18n';
import { el, empty } from '../../ui/dom';
import { buildKeyboard } from '../../ui/keyboard';
import { CueLine, displayLine, searchCues } from './cues';

const MAX_RESULTS = 40;
const DEBOUNCE_MS = 200;

export interface LineSearchOptions {
  // Player root; the overlay is appended here and removed on close.
  root: HTMLElement;
  // Cues of the subtitle currently shown, in play order.
  cues: CueLine[];
  fmtTime: (sec: number) => string;
  // Seek to this cue (the player adds its own timeBase / subtitle offset).
  onPick: (cue: CueLine) => void;
  // Overlay closed: restore the mode the player came from.
  onClose: () => void;
}

export interface LineSearch {
  close: () => void;
}

export function openLineSearch(opts: LineSearchOptions): LineSearch {
  const box = el('div', 'player__popup player__lines');
  box.appendChild(el('div', 'player__popup-title', t('player.line_search')));

  // The keyboard owns the text; without this line the viewer types blind.
  const queryEl = el('div', 'player__lines-query player__lines-query--empty', t('player.line_search_hint'));
  box.appendChild(queryEl);

  const cols = el('div', 'player__lines-cols');
  const left = el('div', 'player__lines-kb');
  const results = el('div', 'player__lines-results');
  cols.appendChild(left);
  cols.appendChild(results);
  box.appendChild(cols);

  let timer = 0;
  let closed = false;

  function close(): void {
    if (closed) return;
    closed = true;
    if (timer) window.clearTimeout(timer);
    if (box.parentNode) box.parentNode.removeChild(box);
    opts.onClose();
  }

  function hint(text: string): void {
    empty(results);
    results.appendChild(el('div', 'player__lines-hint', text));
  }

  function focusResults(): void {
    const first = results.querySelector('.selector');
    if (!first) return;
    Controller.toggle('player_lines_results');
  }

  function render(query: string): void {
    const found = searchCues(opts.cues, query, MAX_RESULTS);
    if (!query) {
      hint(t('player.line_search_hint'));
      return;
    }
    if (!found.length) {
      hint(t('player.line_search_empty'));
      return;
    }
    empty(results);
    for (let i = 0; i < found.length; i++) {
      (function (cue: CueLine) {
        const row = el('div', 'player__lines-row selector');
        row.appendChild(el('span', 'player__lines-time', opts.fmtTime(cue.start)));
        row.appendChild(el('span', 'player__lines-text', displayLine(cue.text)));
        on(row, 'hover:focus', function () {
          try {
            row.scrollIntoView(false);
          } catch (e) {
            /* Chromium 47 has the boolean form only; ignore when it throws */
          }
        });
        on(row, 'hover:enter', function () {
          close();
          opts.onPick(cue);
        });
        results.appendChild(row);
      })(found[i]);
    }
  }

  const keyboard = buildKeyboard({
    controllerName: 'player_lines_kb',
    onChange: function (v) {
      queryEl.textContent = v || t('player.line_search_hint');
      queryEl.className = 'player__lines-query' + (v ? '' : ' player__lines-query--empty');
      if (timer) window.clearTimeout(timer);
      timer = window.setTimeout(function () {
        timer = 0;
        render(v);
      }, DEBOUNCE_MS);
    },
    onRightEdge: focusResults,
    onBackEmpty: close,
  });
  left.appendChild(keyboard.el);

  Controller.add('player_lines_results', {
    toggle: function () {
      Controller.collectionSet(results);
      Controller.collectionFocus(false, results);
    },
    up: function () {
      Controller.moveOr('up');
    },
    down: function () {
      Controller.moveOr('down');
    },
    left: function () {
      keyboard.focus();
    },
    back: function () {
      keyboard.focus();
    },
  });

  opts.root.appendChild(box);
  hint(t('player.line_search_hint'));
  keyboard.register();
  keyboard.focus();

  return { close: close };
}
