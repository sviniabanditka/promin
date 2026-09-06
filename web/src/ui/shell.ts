// Shared screen chrome. The bottom D-pad hint strip ("Arrows navigate · OK
// select · Back exit") was removed — standard TV navigation is self-evident and
// the strip only ate vertical space. buildFooter stays as a no-op returning a
// hidden node so the many callers keep working without per-screen edits.

import { el } from './dom';

export function buildFooter(): HTMLElement {
  return el('div', 'footer footer--empty');
}
