# Frontend

The Promin client is a vanilla TypeScript single-page app in `web/src`. It is served as static files from the Go binary and runs inside Smart-TV webviews (Tizen, webOS, Android TV via MSX), in phone webviews, and in desktop browsers. There is no framework and no virtual DOM: screens build their DOM with `createElement` (`web/src/ui/dom.ts`) and mutate it in place.

## Build pipeline

`web/scripts/build.js` produces everything the server embeds from `server/webdist/`:

| Step | Tool | Output |
|---|---|---|
| Bundle | esbuild, `src/app.ts` → single IIFE, `target: es2017`, minified, `charset: utf8` | in-memory |
| Transpile | `@swc/core` with `web/.swcrc` (`target: es5`, minify) — one pass over the whole bundle | `app.js` (+ `.gz`) |
| CSS | `inlineCssVars()` + forbidden-property guard + esbuild whitespace minify | `styles.css` (+ `.gz`) |
| Assets | `index.html` with `?v=<sha1>` on `app.js`/`styles.css`; `vendor/hls.min.js?v=` rewritten inside the bundle; `msx/start.json` and `web/assets/*` (brand: `logo.svg`, `icon-{32,192,512}.png` → favicon, touch icon, head/PIN logo; also used by the admin and onboarding pages and the Mini App header) copied to the bundle root | `index.html`, `vendor/hls.min.js` (+ `.gz`) |

Scripts: `npm run typecheck` (tsc, `noEmit`), `npm run build`, `npm run check:es5` (`es-check es5` on the built bundle). The build only overwrites its own artifacts; it never wipes `webdist/`.

`hls.js` is the **full** 1.5.x build (`web/vendor/hls.min.js`, ~415 KB) and is not part of `app.js`. `web/src/core/player/hls.ts` injects it as a `<script>` the first time an HLS source needs it (15 s timeout, retry on failure). The only bundled polyfill is `whatwg-fetch`; `Promise` is native on every target.

### ES5 / Chromium ~47 constraints

The floor is the pre-2022 Samsung webview (Chromium ~47) and webOS 3 (Chromium 38). Consequences for source code:

- **No `async/await`, `for…of`, spread, `Array.prototype.find/includes`, `Object.assign`** at runtime — swc lowers syntax but no `core-js` is bundled. Use plain loops and `.then()` chains.
- **`let`/`const` hoisting order matters.** swc turns block-scoped bindings into `var`s; a closure that runs during module/mount initialisation must not read a binding declared further down the same function. The player (`core/player/index.ts`) declares its shared state above the DOM builders for this reason.
- **CSS custom properties are compiled away.** `styles.css` keeps one `:root { --token: value }` block; the build replaces every `var(--token)` with the literal and strips the block. An unknown `--name`, a `var()` with a fallback, or any surviving `var(` **fails the build**. Nested `var()` inside a token value resolves one level deep.
- **Forbidden CSS** (build error): `display: grid`, `gap:`, `position: sticky`, `backdrop-filter`, `filter: blur`. Layouts are flexbox; shadows are flat.
- **Animate only `transform` and `opacity`.** Lists move with `translate3d`, backdrops cross-fade with opacity.
- **No `Intl`.** Dates, weekdays and months are hand-formatted per language (`core/screensaver.ts`, `ui/head.ts`).
- No `IntersectionObserver`, no `<script type="module">`, no `import()`; `isConnected` is absent (use `contains`).
- `scrollIntoView` only takes the boolean form.

## Boot sequence

`web/src/app.ts` runs `boot()` on `DOMContentLoaded`:

1. `initI18n()` — language from `localStorage['promin:lang']`, else `navigator.language` if it is `uk`/`ru`/`en`, else `uk`.
2. Phone detection (`isPhone()`: touch-capable and `min(screen.width, screen.height) < 540`) → viewport meta switched to `device-width`, `html.is-phone`, zoom gestures vetoed.
3. `applyViewportScale()` and a `resize` listener (see *Viewport scaling*).
4. `initSettings()` — read `localStorage['promin:settings']`, apply `?legacy=` / `?debug=` URL flags, apply `reduce-motion` body class and the legacy capability override.
5. `steerHost()` (`core/legacy.ts`) — `GET /api/v1/ping` returns `h1_host`/`main_host`; a device in legacy TV mode moves to the HTTP/1.1-only host, a device out of it moves back (see *Device-local settings*).
6. `installGlobalHooks()` + `report('boot', viewportInfo())` — diagnostics.
7. `Controller.initInput()` — global keydown/keyup; `initPointer()` — mouse/touch/wheel/pointer layers.
8. `setDeadSessionHook(gateToPin)` — any 401 on an authed call clears the token and remounts the PIN screen.
9. `router.init(root, exitToast)`, `screensaver.init()`, then `routeInitial()`: no token → PIN screen (the URL hash is left alone, so the deep link survives the gate and is opened after login); token → `sync.start()`, `openRoute(location.hash)`, `syncFromServer()` pulls server settings and reloads the page if the server language differs. Any language change (settings screen, bot, Mini App, another device) is a full `location.reload()`: every screen and cached card is language-dependent, and the route in the hash brings the same screen back.

## Navigation model

Three layers, all in `web/src/core`:

| Layer | File | Role |
|---|---|---|
| `Navigator` | `nav.ts` | Geometric spatial navigator. Holds the active **collection** of `.selector` elements, finds the next one in a direction by partitioning candidate rects into 9 regions around the focused rect (`straightOnly`, 50 % overlap threshold). Elements with a 0×0 box or `aria-hidden` are skipped, so `.hide` removes an element from navigation. Emits `focus`/`unfocus`. |
| `Controller` | `controller.ts` | Named **modes** (`menu`, `content`, `player`, `player_menu`, …). Exactly one mode is active; it receives `left/right/up/down/enter/back` plus media-key actions. `Controller.focus()` paints `.focus`, fires the element event `hover:focus`, and auto-scrolls every enclosing `Scroll`. `Controller.moveOr(dir, fallback)` is the standard "move the ring or leave the region" helper. Per-element events (`on(el, 'hover:enter', fn)`) are stored on the element itself. |
| `Scroll` | `scroll.ts` | A `.scroll > .scroll__content > .scroll__body` block. The body is moved with `translate3d` + `transition: transform 0.3s`, never native `scrollTop`. `update(el)` brings an element into view, `scrollBy(px, animate)` is the wheel/touch entry point, `stop()` halts a coasting animation by reading the computed matrix. A static registry (`Scroll.forBody`) lets `Controller.autoScrollTo` find the instance for any `.scroll__body` ancestor. |

Screens register modes with `Controller.add(name, calls)` and switch with `Controller.toggle(name)`; a mode's `toggle` handler typically calls `Controller.collectionSet(rootEl)` and `Controller.collectionFocus(el, rootEl)`. Modes are removed with `Controller.remove` when a modal or the player is destroyed.

Screens live on an **Activity stack** (`core/activity.ts`, `core/router.ts`): `push()` hides the previous screen (paused, DOM kept) and mounts a new container; `back()` destroys the top and resumes the one beneath; `replaceRoot()` tears everything down for a menu-level screen. At most 5 activities are kept; the second-oldest is evicted, never the root. Between `push()` and the new screen's first `toggle`, a throwaway `activity_pending` mode owns input so an OK during a spinner cannot reach the hidden screen. `back()` at the root shows an exit toast — the host platform handles real exit.

A screen that loads asynchronously and can end up hidden under a pushed screen before its data arrives (Home under a title opened from the bot or a deep link, Library under a deep-linked playlist, a title under a second title) must not touch the Controller from that late callback: it would steal focus from the visible screen and overwrite its mode names. Home, Library and Title keep a `paused` flag (set in `pause()`, cleared in `resume()`) and only remember what to activate; `resume()` activates it.

## Routes (URL hash)

Every screen carries a route; the visible screen's route is mirrored into `location.hash` with `history.replaceState` (never `pushState`, so the browser history stays flat and the platform Back key keeps driving the app's own stack). A reload or a shared link re-opens the same screen through `screens/nav.ts openRoute()`, which rebuilds the stack as the section's menu-level screen plus the detail screen on top (Back from a deep-linked title lands on Home).

| Route | Screen |
|---|---|
| `#/` | Home |
| `#/catalog`, `#/catalog/<category>` | Catalog (a home-lane category as the filter preset) |
| `#/search`, `#/search?q=<query>&type=movie\|tv` | Search; the query and filter are re-run on load and tracked as they change |
| `#/library`, `#/bookmarks`, `#/playlists`, `#/playlist/<id>`, `#/torrents` | Library and the screens it pushes |
| `#/settings`, `#/devices` | Settings and the device manager |
| `#/title/<movie|tv>/<tmdb_id>`, `…?s=<season>&e=<episode>` | Title page; with `s`/`e` the watch modal opens on that episode |
| `#/person/<id>` | Actor / director page (filmography grid), pushed over Home |

Unknown or malformed routes open Home. The Mini App at `/tg/` has its own hash router (docs/miniapp.md).

## Input layers

### Remote / keyboard (`core/controller.ts`)

- One `keydown` listener on `window`. Direction keys: `37/38/39/40`, plus `4/5` (left/right) and `29460/29461` (up/down) from TV remotes. Key repeat is throttled to one dispatch per 100 ms.
- **Enter** = `13`, `29443`, `117`, `65385`; dispatched on `keyup`. Holding OK ≥ 600 ms fires `hover:long` on the focused element instead (cards use it for bookmark toggling).
- **Back** is recognised by code — `8`, `27`, `461` (webOS), `10009` (Tizen), `88`, `166`, `10182`, `145` — by `e.key` (`GoBack`, `BrowserBack`, `XF86Back`, `Escape`, `Backspace`, `ScrollLock`), and by code `4` on Android hosts only.
- **Media keys** map to mode actions: `10252` playpause, `415` play, `19` pause, `413` stop, `412` rewind, `417` forward, `10233` next, `10232` prev. On Tizen they are requested with `tizen.tvinputdevice.registerKey`.
- A focused native `<input>`/`<textarea>` owns its keys; only the TV Back codes blur it, and an Enter that closed the field does not also fire `enter`.

### Mouse, wheel, touch, pointer (`core/pointer.ts`)

All listeners are delegated on `window` and feed the same Navigator/Controller entry points:

| Event | Behaviour |
|---|---|
| `mouseover` on a `.selector` | `Navigator.focus()` with `hoverNoScroll` armed, so the ring moves but nothing scrolls. Hovering a tile in another `.scroll__body` retargets the collection to that body. |
| `mousemove` / `keydown` | Sets `html[data-input="point"]`; touch gestures set `"touch"` (hides remote-key hints, shows `cursor: pointer`). |
| `click` (capture) | Element's own `hover:enter` if it has one, else `Controller.enter()`. A click on a dismissable overlay root (`modal-overlay`, `settings-modal`, `action-sheet-overlay`, `trailer-overlay`) is `Controller.back()`. |
| `wheel` (non-passive) | Axis-locks per gesture (horizontal only for real horizontal deltas or Shift), scrolls the nearest lane or page `Scroll` immediately, and freezes hover for 400 ms. |
| Touch drag | 10 px axis lock, finger-follow without transition, fling on release (`velocity × 180`), catch-on-touch stops a coasting list, rightward drag with no lane (or from the 24 px left edge) is Back. |
| Pointer events | Touch/pen pointers drive the **same gesture core** through a second adapter. The iOS WKWebView inside the MSX app sends only `pointerdown/up` — no touch events and no `click` — so `pointerup` after a pure tap activates the target directly and suppresses any late native click for 700 ms. Once a touch-type pointer is seen, the touch adapter goes quiet. |

Synthetic mouse events after a touch are ignored for 700 ms. In diagnostics mode the first 25 input events of a session are reported.

## Viewport scaling

The design canvas is 1280×720 with `1rem = 10px`; every size in `styles.css` is `rem`/`%`. `applyViewportScale()` in `app.ts`:

- **TV / desktop:** `root font-size = 10px × documentElement.clientWidth / 1280`. The layout viewport, not `innerWidth`, is used because some Android TV webviews lay out at the `<meta viewport width=1280>` but report `innerWidth` as the visual viewport.
- **Phone (`html.is-phone`):** baseline 390 px, scale from `min(innerWidth, innerHeight)` (rotation-stable), floor 0.75 so touch targets stay ≥ 48 px. Phone CSS is gated on the class, not `@media`: the rail becomes a bottom tab bar, sheets become bottom sheets.
- **Cropped viewport fix:** a webview that honours `width=1280` for layout but shows the page 1:1 (detected as `innerWidth < clientWidth − 2`) gets the viewport meta switched to `device-width` and `html.vp-cropped`; the whole UI re-scales from the new width.
- **Tall stage (`html.vp-tall`):** when a TV webview reports a viewport more than 3 % taller than 16:9, `#app` is confined to a 72 rem stage and every viewport-fixed layer (player, panel, toasts, screensaver, modals) is anchored to it. Desktop browsers (`isDesktopBrowser()` by UA: Windows/Mac/X11/CrOS and not Android/Tizen/webOS/MSX) never get the class — a 16:10 window simply fills its height.

## Settings

`core/settings.ts` is the single store (`localStorage['promin:settings']`, validated on read).

| Key | Values | Scope |
|---|---|---|
| `lang` | `uk` / `ru` / `en` | synced (owned by `core/i18n.ts`, mirrored via `PUT /settings/lang`) |
| `default_quality` | `auto` / `2160` / `1080` / `720` / `480` | synced |
| `player_engine` | `auto` / `hlsjs` / `native` | synced |
| `player_speed` | 0.25–4 | synced |
| `screensaver_min` | `0` / `3` / `5` / `10` | synced |
| `subtitle_size` | `small` / `medium` / `large` | synced |
| `legacy_tv_mode` | bool | **device-local** |
| `reduce_motion` | bool | **device-local** |
| `debug_mode` | bool | **device-local** |

Synced keys go to `PUT /api/v1/settings/{key}` when logged in and are pulled by `syncFromServer()` at boot. Device-local keys never leave the device:

- **Legacy TV mode** describes this webview's inability to play demuxed HLS and sustained media over HTTP/2. On: `capsQuery()` sends `demuxed_hls=false` and `max_http_version=h1` (the backend routes such HLS through `/remux`), `preferNativeHls()` is false (hls.js instead of native `<video>` HLS), and `steerHost()` moves the app to the server's `h1_host` with `?legacy=1` in the URL (the two hosts are separate origins with separate `localStorage`). Off on the h1 host: back to `main_host` with `?legacy=0`. There is no User-Agent autodetect.
- **Reduce motion** adds `body.reduce-motion`: all transitions/animations off except spinners.
- **Diagnostics mode** enables `core/diag.ts` reporting (below). `?debug=1` in the URL turns it on before login. The default is currently on for devices that have not chosen otherwise.

Device capabilities (`core/capabilities.ts`) are detected once: platform (`tizen`/`webos`/`androidtv`/`browser`), `hevc` (MSE probe), `hls_native` (`canPlayType`), `mkv`. They ride as query params on `/sources/online*` and drive torrent URL flags (`mkv=false`, `transcode=1`, `hdr=1`).

## i18n

`core/i18n.ts` holds three flat dictionaries in the bundle (`uk` default, `ru`, `en`). `t(key, params)` does `{param}` interpolation and returns the key itself when missing. `setLang()` persists to `localStorage['promin:lang']`; the settings screen re-renders the visible screen and mirrors the choice to the server.

## Screens

Menu-level screens are reached from the left icon rail (`ui/menu.ts`: Home, Catalog, Search, Library, Settings) via `replaceRoot`; detail screens are pushed.

| Screen | File | Notes |
|---|---|---|
| PIN | `screens/pin.ts` | 6-digit dialer, hard gate; D-pad, mouse, touch and number keys. |
| Home | `screens/home.ts` | Vertical stack of horizontal lanes from `GET /catalog/home`; focus tints the backdrop. |
| Catalog | `screens/catalog.ts` | Filter bar (type/genre/year/sort) + paged flex-wrap grid, next page prefetched near the end. |
| Search | `screens/search.ts` + `ui/keyboard.ts` | Query bar + on-screen keyboard (uk/ru, latin, digits) on the left, 4-column results on the right; 500 ms debounce. Empty field shows recent queries as chips (OK re-runs, long OK deletes; a query is remembered when a title is opened from its results; the list is the profile setting `search_history`, shared with the Mini App and other TVs) over the home trending row. With a query: an all / movies / series filter row (typed TMDB search), a People row (actors, directors → `screens/person.ts`), the card grid, further pages when focus nears the end. Physical keyboard types through a document listener; `html.is-phone` swaps the key grid for a native field. No host IME on TV — its confirm/blur timing differed per platform and left focus stranded. |
| Title | `screens/title.ts` | Full-height card; "Watch"/"Torrents" open an in-place action sheet that resolves online sources or adds a torrent and opens the player. |
| Library | `screens/library.ts` | Continue-watching, favourites, playlists lanes; links to `bookmarks.ts`, `playlists.ts`. |
| Torrents | `screens/torrents.ts` | Active torrents on the server; play or delete. |
| Settings | `screens/settings.ts` | Value-picker modals; legacy/reduce-motion/diagnostics toggle in place; shows server version from `/ping`. |
| Devices | `screens/devices.ts` | Active sessions, revoke. |

Shared UI: `ui/card.ts`, `ui/state.ts` (loading/empty/error with retry), `ui/confirm.ts`, `ui/toast.ts`, `ui/head.ts` (logo + minute-aligned clock), `ui/background.ts`.

Realtime data (`core/sync.ts`): bookmarks and timecodes are cached in `localStorage`, bootstrapped over HTTP and kept live over a WebSocket; writes are optimistic with rollback.

## Screensaver and weather

`core/screensaver.ts` arms an idle timer from `screensaver_min` (0 disables). Any key, mouse, touch or wheel event resets it; the wake press is swallowed (both halves) so it does not act on the UI. It never activates while a `<video>` is playing. On activation it waits for the first backdrop to decode (max 6 s) and then shows a full-screen overlay: two cross-fading backdrop layers rotated every 20 s from `GET /backdrops` (a random slice of the whole catalog, each image preloaded and skipped on failure), a clock, a hand-formatted date, a bottom-right caption with the title and year, and a weather strip. The forecast comes from `GET /weather` (fetched 20 s after boot and every 15 min, rendered synchronously from cache): current temperature and WMO-code label, place name, and a 7-day high/low row. Labels are plain text per language — no emoji, no `Intl`.

## Diagnostics

`core/diag.ts` is the one channel for "what does this device actually do". When `debug_mode` is on, `report(kind, data)` POSTs `{kind, seq, host, data}` to `POST /api/v1/diag` (fire-and-forget, min 40 ms apart); the server writes it to its log as `msg=diag`. Built-in probes: `boot` with `viewportInfo()` (inner/outer/screen/doc/visual sizes, dpr, root font, touch and PointerEvent support, html classes, UA), `viewport-fix`, every `key` (also shown as a toast), the first input events, `error`/`unhandledrejection`, and the player's `player:*` events (mount, engine, seek, resume, stall, errors).

## Night mode

TV webviews expose no backlight API, so "brightness 0" is imitated: a single
`#night-shade` div on `<body>` (black, `position: fixed`, `pointer-events: none`,
top z-index) with `opacity = night_dim / 100`. It covers everything — video,
menus, subtitles, toasts. `night_mode` (on/off) and `night_dim` (50–90 %, step
5) are synced settings; the toggle lives in Settings and in the player's "more"
menu, and a change made on another device is applied live through the
`settings_updated` sync event (`applyRemoteSetting` in `core/settings.ts`).

## Toasts

`ui/toast.ts` — one card at a time, bottom-centre, reused in place (no flash on
replace). `toast(text)` or `toast({ title?, text, icon?, kind?, duration? })`
with kinds `info | success | warning | error | progress` (accent bar/icon colour;
`progress` adds an indeterminate bar and is meant with `duration: 0` = sticky
until the next toast). Copy is split into a short title and a one-line hint
(`*_hint` i18n keys). Also `app.ts` polls `/api/v1/ping` every 10 minutes and
toasts once when the server version changes.

## Pre-resolve

Opening a title page starts resolving the remembered source for the episode
"Continue"/"Watch" would start with (`prefetchResolve` in `screens/title.ts`);
the watch modal's `resolveMemo` reuses that promise when the params match, so
the first play needs no extra round-trip.
