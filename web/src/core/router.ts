// Global router: a singleton Activity stack plus the navigation verbs the
// screens call. push() stacks a detail screen (title) with instant back();
// replaceRoot() swaps the whole stack for a menu-level screen (home /
// catalog / search). back() falls through to a caller-supplied handler
// (exit toast) when the stack can't pop.

import { Activity, RenderFn } from './activity';

let activity: Activity | null = null;
let onExit: (() => void) | null = null;

export function init(root: HTMLElement, exitHandler: () => void): void {
  activity = new Activity(root);
  onExit = exitHandler;
}

export function push(render: RenderFn): void {
  if (activity) {
    activity.push(render);
  }
}

export function replaceRoot(render: RenderFn): void {
  if (activity) {
    activity.replaceRoot(render);
  }
}

export function back(): void {
  if (activity && activity.back()) {
    return;
  }
  if (onExit) {
    onExit();
  }
}
