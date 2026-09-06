// Hash router: #/  #/search?q=  #/title/tv/1399  #/remote  #/library  #/settings

import { useEffect, useState } from 'preact/hooks';

export type Screen = 'home' | 'search' | 'title' | 'remote' | 'library' | 'settings';
export interface Route {
  screen: Screen;
  params: string[];
  query: URLSearchParams;
}

export const TABS: Screen[] = ['home', 'search', 'remote', 'library', 'settings'];

export function parse(hash: string): Route {
  const [pathPart, q = ''] = hash.replace(/^#\/?/, '').split('?');
  const segs = pathPart.split('/').filter(Boolean);
  const head = segs[0] || 'home';
  const screen: Screen = (['search', 'title', 'remote', 'library', 'settings'] as Screen[]).includes(head as Screen) ? (head as Screen) : 'home';
  return { screen, params: segs.slice(1), query: new URLSearchParams(q) };
}

export function useRoute(): Route {
  const [route, setRoute] = useState(() => parse(location.hash));
  useEffect(() => {
    const f = () => setRoute(parse(location.hash));
    window.addEventListener('hashchange', f);
    return () => window.removeEventListener('hashchange', f);
  }, []);
  return route;
}

let depth = 0;

export function navigate(path: string, replace = false): void {
  const hash = '#' + (path.startsWith('/') ? path : '/' + path);
  if (hash === location.hash) return;
  if (replace) location.replace(hash);
  else {
    depth++;
    location.hash = hash;
  }
}

export function back(): void {
  if (depth > 0) {
    depth--;
    history.back();
  } else navigate('/', true);
}

export const titlePath = (type: string, id: number) => '/title/' + type + '/' + id;
