// Global router: a singleton Activity stack plus the navigation verbs the
// screens call. push() stacks a detail screen (title) with instant back();
// replaceRoot() swaps the whole stack for a menu-level screen (home /
// catalog / search). back() falls through to a caller-supplied handler
// (exit toast) when the stack can't pop.
//
// Every screen carries a route path ("/", "/catalog/trending", "/title/tv/
// 1399", …). The visible screen's path is mirrored into location.hash with
// history.replaceState — never pushState, so the browser's own history stays
// flat and the platform Back key keeps mapping to the app's back(). A reload
// or a shared link then re-opens the same screen (screens/nav.ts openRoute).

import { Activity, RenderFn } from './activity';

let activity: Activity | null = null;
let onExit: (() => void) | null = null;

export function init(root: HTMLElement, exitHandler: () => void): void {
  activity = new Activity(root);
  onExit = exitHandler;
}

export function push(render: RenderFn, path?: string): void {
  if (activity) {
    activity.push(render, path);
    syncHash();
  }
}

export function replaceRoot(render: RenderFn, path?: string): void {
  if (activity) {
    activity.replaceRoot(render, path);
    syncHash();
  }
}

export function back(): void {
  if (activity && activity.back()) {
    syncHash();
    return;
  }
  if (onExit) {
    onExit();
  }
}

// The visible screen's route ('' for the PIN gate).
export function currentPath(): string {
  return activity ? activity.topPath() : '';
}

// A screen changed its own route (search query). No-op unless `container`
// is the visible screen.
export function setPath(container: HTMLElement, path: string): void {
  if (activity && activity.setTopPath(container, path)) syncHash();
}

// Mirror the top route into the hash. A screen without a path (PIN entry)
// leaves the hash alone so a deep link survives the login gate.
function syncHash(): void {
  const path = currentPath();
  if (!path) return;
  const want = '#' + path;
  if (window.location.hash === want) return;
  try {
    window.history.replaceState(null, '', window.location.pathname + window.location.search + want);
  } catch (e) {
    /* no History API — the route just isn't reflected in the URL */
  }
}
