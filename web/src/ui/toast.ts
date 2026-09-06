// Global toast, mounted on <body> so any screen can call it. Uses the
// `.toast` styles from styles.css. Transition on opacity/transform only.

import { el } from './dom';

export function toast(message: string): void {
  const parent = document.body;
  const existing = parent.querySelector('.toast');
  if (existing && existing.parentNode) {
    existing.parentNode.removeChild(existing);
  }

  const node = el('div', 'toast', message);
  parent.appendChild(node);

  // Force reflow so the transition runs from the hidden state.
  void node.offsetWidth;
  node.classList.add('toast-visible');

  window.setTimeout(function () {
    node.classList.remove('toast-visible');
    window.setTimeout(function () {
      if (node.parentNode) {
        node.parentNode.removeChild(node);
      }
    }, 300);
  }, Math.min(4000, 1800 + message.length * 30));
}
