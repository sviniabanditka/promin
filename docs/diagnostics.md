# Diagnostics

A TV has no devtools, and its toasts may be off-screen. Diagnostics mode makes
the client report what it actually sees to the server log, where `kubectl`
reads it. Every new "what does this device really do" probe hangs off this one
switch — no new flags.

## 1. The device setting

Settings → "Diagnostics mode" (`debug_mode` in `web/src/core/settings.ts`).
Device-local: kept in this device's `localStorage`, never mirrored to the
account. Turning it on in the settings screen immediately reports a `viewport`
snapshot and shows a hint toast (`web/src/screens/settings.ts`).

Off by default (`DEBUG_DEFAULT = false` in `settings.ts`); a device that never
chose a value follows the default.

URL override, read before login (`readLegacyFromURL()`): `?debug=1` turns the
mode on, `?debug=0` off — for a device whose settings screen cannot be operated.

## 2. What gets reported

`report(kind, data)` in `web/src/core/diag.ts` is fire-and-forget: it returns
immediately when the mode is off, drops events closer than 40 ms apart (key
repeat), increments a per-session `seq`, and POSTs
`{kind, seq, host: location.host, data}` to `/api/v1/diag`. Failures are
ignored.

| `kind` | Where | Payload |
|---|---|---|
| `boot` | `web/src/app.ts` at startup | `viewportInfo()`: `inner`, `outer`, `screen`, `avail`, `doc` (documentElement client size), `visual` (visualViewport), `dpr`, `rootFont`, `touch` (`ontouchstart`), `maxTouchPoints`, `pointerEvents`, `htmlClass`, `ua` |
| `viewport-fix` | `app.ts` | `inner`, `layout` — when the visual viewport is narrower than the layout viewport and the meta viewport is rewritten |
| `viewport` | settings toggle | same as `boot` |
| `key` | `web/src/core/controller.ts` keydown | `keyCode`, `key`, `code`, `mode` (active controller); also a toast `key <code> / <key>` |
| `input` | `web/src/core/pointer.ts` | first 25 `touchstart/touchend/pointerdown/pointerup/mousedown/mouseup/click` events of the session: `type`, `target` (tag.class), `touches`, `pointerType`, `x`, `y` |
| `error`, `unhandledrejection` | global hooks installed at boot (`installGlobalHooks`) | `message`, `source`, `line`, `col` / `reason` |
| `player:*` | `web/src/core/player/index.ts` (`diag()` helper) | `mount`, `engine` (hls/native/growing/startAt), `mux-from`, `seek-restart`, `seek-stash`, `resume-at-base`/`resume-ok`/`resume-retry`/`resume-failed`, `loadedmetadata`, `hls-fatal`, `stall`, `video-error`, `ended` — all with rounded times |

## 3. `POST /api/v1/diag`

`handleDiag` in `server/internal/httpapi/server.go`. Registered without
`requireAuth` — a device that cannot log in must still be able to report. The
body is capped at 16 KiB; invalid JSON → `400`; success → `204`. If the request
carries a valid bearer token the user id is attached, otherwise `user=0`.

One log line per probe:

```
level=INFO msg=diag kind=<kind> seq=<n> user=<id> ip=<client ip> host=<page host> ua=<User-Agent> data=<raw JSON>
```

`ip` comes from `clientIP(r)` (proxy headers honoured), `ua` from the request,
`host` from the page (tells h1 host from main host), `data` is the client's
JSON verbatim.

## 4. Reading it in production

```
kubectl -n promin logs deploy/promin | grep msg=diag
kubectl -n promin logs deploy/promin -f | grep msg=diag        # live
kubectl -n promin logs deploy/promin | grep msg=diag | grep 'kind=key'
```

Deployment `promin`, namespace `promin`, single replica (`k8s/promin.yaml`).
Filter by `ip=` or `ua=` to isolate one TV. The same log is also served by the
`/logs` page (Basic Auth, password from the `promin-logs` secret via
`PROMIN_LOGS_PASSWORD`; the page is disabled when the secret is absent).

Things already found this way: an Android TV WebView whose Back key arrives as
`keyCode 145` / `"ScrollLock"`, and a device whose `window.innerWidth` (960)
differs from the layout viewport `documentElement.clientWidth` (1280) — which
is why the root font scale reads `documentElement.clientWidth`.

## 5. Prod verification recipe

1. **Version** — `GET https://promin.club/api/v1/ping` returns
   `{"pong":true,"version":"<build>","h1_host":"h1.promin.club","main_host":"promin.club"}`;
   `version` is set from `-ldflags -X main.version=...` and confirms which build
   is live.
2. **Session** — `POST /api/v1/auth/pin` with `{"pin":"<6 digits>","device_name":"check"}`
   (`server/internal/httpapi/handlers_auth.go`) returns `{"token","user"}`.
   Use `Authorization: Bearer <token>` on `/api/v1/*`.
3. **Media routes** — `/relay`, `/stream`, `/remux`, `/api/v1/ws` accept the
   token only as `?t=<token>` when no header can be sent (`requireAuthMedia`,
   `middleware.go`). Manifests and redirects the server returns already carry it;
   a `401` on a child URL means some generated URL lost the token.
4. **Frontend bundle** — `promin.club` is behind Cloudflare, which caches
   `/app.js` for a day. Check the versioned URL that `index.html` references,
   never the bare one:
   ```
   v=$(curl -s https://promin.club/ | grep -oE 'app\.js\?v=[0-9a-f]+' | head -1)
   curl -s "https://promin.club/$v" | grep -c '<marker string>'
   ```
   Compare by markers (new strings present, removed ones absent), not by size
   or hash — CI's toolchain produces a different byte stream than a local build.
5. **Clean up** — `POST /api/v1/auth/logout` with the bearer token removes the
   check device's session so it does not linger in the device list.

## 6. Local dev verification

- **`go:embed` caches the bundle at compile time.** `npm run build` updates
  `server/webdist/` on disk, but a previously built binary keeps serving the
  embedded copy. Set `PROMIN_WEBDIR=<path to server/webdist>` and the server
  serves from disk (`server/internal/httpapi/static.go`); then only
  `npm run build` is needed between checks. Production leaves it unset.
- **Second instance beside a running one** — set `PROMIN_TORRENT_PORT` to avoid
  the BitTorrent listen-port clash.
- **Headless browser checks** (puppeteer):
  - `page.keyboard.press` does not reach the `window` keydown listener the
    Controller uses. Dispatch synthetic events instead:
    `page.evaluate(c => ['keydown','keyup'].forEach(t => window.dispatchEvent(new KeyboardEvent(t, {keyCode: c, which: c, bubbles: true}))), CODE)`.
    Codes: down 40, up 38, left 37, right 39, ok 13, back 8.
  - Page `console.log` is not reliably proxied. Write probe values to
    `window.__x` or a DOM attribute and read them back with `evaluate`
    (`JSON.stringify` drops `undefined` fields).
  - Log in by writing `promin:token` and `promin:user` to `localStorage`
    (`web/src/core/auth.ts`) with values from `POST /api/v1/auth/pin`.
  - Measure effects (`getComputedStyle(...)`, focused element
    `getBoundingClientRect()`), not log lines; clicks via
    `dispatchEvent(new MouseEvent(...))`.
- Diagnostics mode works locally the same way: the server logs `slog` text to
  stdout (`server/cmd/promin/main.go`), so `grep msg=diag` on it.
