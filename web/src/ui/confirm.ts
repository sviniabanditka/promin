// Yes/no confirm sheet shared by torrents / playlists / settings. Each screen
// used to carry an identical 40-line copy (overlay + two buttons + a mode with
// left/right/back), each with its own double-OK bug surface.

import Controller, { on } from '../core/controller';
import { el } from './dom';
import { t } from '../core/i18n';

export interface ConfirmOptions {
  text: string;
  yesLabel: string;
  // Controller mode name for the sheet (unique per screen so a stale
  // registration can never be confused with another screen's).
  mode: string;
  // Mode to return to on cancel/back (default 'content').
  returnMode?: string;
  // Runs once on Yes. The overlay is already gone; the caller decides what
  // happens to focus next (reload, optimistic removal, navigation).
  onYes: () => void;
}

export function openConfirm(container: HTMLElement, o: ConfirmOptions): void {
  const returnMode = o.returnMode || 'content';
  const overlay = el('div', 'settings-modal');
  const inner = el('div', 'player__confirm');
  inner.appendChild(el('div', 'player__confirm-text', o.text));
  const actions = el('div', 'player__confirm-actions');
  const yes = el('div', 'button button--accent selector', o.yesLabel);
  const no = el('div', 'button selector', t('action.cancel'));
  actions.appendChild(yes);
  actions.appendChild(no);
  inner.appendChild(actions);
  overlay.appendChild(inner);
  container.appendChild(overlay);

  let done = false;
  function dismiss(): void {
    if (overlay.parentNode) overlay.parentNode.removeChild(overlay);
  }
  function cancel(): void {
    if (done) return;
    done = true;
    dismiss();
    Controller.toggle(returnMode);
    Controller.remove(o.mode);
  }
  on(yes, 'hover:enter', function () {
    if (done) return; // a TV remote's double OK must not fire the action twice
    done = true;
    dismiss();
    Controller.remove(o.mode);
    o.onYes();
  });
  on(no, 'hover:enter', cancel);

  Controller.add(o.mode, {
    toggle: function () {
      Controller.collectionSet(overlay);
      Controller.collectionFocus(no, overlay); // Cancel is the safe default
    },
    left: function () {
      Controller.moveOr('left');
    },
    right: function () {
      Controller.moveOr('right');
    },
    back: cancel,
  });
  Controller.toggle(o.mode);
}
