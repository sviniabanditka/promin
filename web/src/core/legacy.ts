// Host steering for "Режим старого ТВ" (docs/streaming.md).
//
// The main host (promin.club) sits behind Cloudflare, which always negotiates
// HTTP/2 — and the pre-2022 Samsung webview breaks sustained media over h2. The
// server names an HTTP/1.1-only host (PROMIN_H1_HOST, Traefik TLSOption
// alpnProtocols=[http/1.1]) and the main host (PROMIN_MAIN_HOST). This device's
// legacy switch decides where the app should live:
//   legacy ON  and not on the h1 host  → go to the h1 host   (?legacy=1)
//   legacy OFF and on the h1 host      → go back to the main host (?legacy=0)
// The flag rides in the URL because the two hosts are separate origins with
// separate localStorage. No User-Agent detection anywhere.
//
// ES5 target: no async/await.

import { isLegacyTv } from './settings';

interface Ping {
  h1_host?: string;
  main_host?: string;
}

function withLegacy(search: string, on: boolean): string {
  const cleaned = search.replace(/([?&])legacy=[01]&?/, '$1').replace(/[?&]$/, '');
  const sep = cleaned ? '&' : '?';
  return cleaned + sep + 'legacy=' + (on ? '1' : '0');
}

export function steerHost(): void {
  fetch('/api/v1/ping')
    .then(function (r) {
      return r.json();
    })
    .then(function (p: Ping) {
      const h1 = (p && p.h1_host) || '';
      const main = (p && p.main_host) || '';
      const here = window.location.hostname;
      const legacy = isLegacyTv();
      let target = '';
      if (legacy && h1 && here !== h1) target = h1;
      else if (!legacy && h1 && main && here === h1 && here !== main) target = main;
      if (!target) return;
      window.location.replace(
        'https://' + target + window.location.pathname + withLegacy(window.location.search, legacy) + window.location.hash
      );
    })
    ['catch'](function () {
      /* offline or old build: stay where we are */
    });
}
