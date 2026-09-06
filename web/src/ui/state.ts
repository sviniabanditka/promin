// One state block for every screen's empty / error / loading view. Before this
// each screen rolled its own (.state-message left-aligned, .state-message--soft
// centered, .sources-loading spinner, .full-episodes__loading bare text) — four
// looks for the same three states. buildState is the single source: a centered
// column with an optional spinner, a message, and an optional retry button.
//
// ES5 target: function expressions, no async/for-of/spread. The retry button is
// a plain .selector so the caller's Controller collection picks it up.

import { el } from './dom';
import { on } from '../core/controller';
import { t } from './../core/i18n';

export type StateKind = 'loading' | 'empty' | 'error';

export interface StateOptions {
  kind: StateKind;
  text?: string; // defaults per kind
  onRetry?: () => void; // shows an action button when provided
  actionLabel?: string; // button label; defaults to "Retry"
}

export function buildState(opts: StateOptions): HTMLElement {
  const box = el('div', 'state state--' + opts.kind);

  if (opts.kind === 'loading') {
    box.appendChild(el('div', 'state__spinner'));
  }

  const text =
    opts.text ||
    (opts.kind === 'error' ? t('error.load') : opts.kind === 'empty' ? t('common.empty') : t('common.loading'));
  box.appendChild(el('div', 'state__text', text));

  if (opts.onRetry) {
    const retry = el('div', 'button selector state__retry', opts.actionLabel || t('action.retry'));
    on(retry, 'hover:enter', opts.onRetry);
    box.appendChild(retry);
  }

  return box;
}
