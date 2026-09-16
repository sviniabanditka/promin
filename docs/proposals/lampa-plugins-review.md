---
title: Lampa plugins reviewed for Promin (2026-09-16)
---

# Lampa plugins — what is worth borrowing

The owner's list of Lampa plugins, read source by source (23 files, ~2 MB of
JS; `bwa`, `skaz`, `smotret24`, `lumio`, `alpac` are Lampac front-ends, three
sites were down: `arkmv.ru/vod`, `showwwy.com/m.js`, `z01.online/live`,
`wtch.ch/m` is 404). Verdicts are against what Promin already has.

## 1. Online balancers (BWA, Online Mod, MODS's, Filmix, Skaz, Z01, smotret24, Lumio, Alpac)

All of them are thin clients of a **Lampac** (or a fork) hosted by the plugin
author: the TV asks `<author's server>/lite/<balancer>` and gets stream URLs.
Promin deliberately does the opposite (`docs/sources.md`, native providers +
lampac fallback), so the plugins themselves are not reusable. What *is* worth
taking is the machinery around the balancers:

| Technique | Where | Promin |
|---|---|---|
| **RCH — the TV fetches the source page itself** and posts the HTML back to the server (`/rch/<uri>?id=`), so datacenter-blocked catalogs work from a residential IP without a paid proxy | Lumio (`nexusRch`), Lampac core | Not built. This is the answer to the "датацентр-стена" (kinotochka, kinoukr, rezka 503 on hop 2). Design: server queues a fetch job, the TV's `sync` socket receives it, does `fetch` with the source's headers, posts the body back; server continues the provider call. Same-origin/CORS limits mean only sources that allow cross-origin GETs (or plain JSON APIs) — Lampac's list of RCH-capable providers is the guide. **Best candidate.** |
| **User's own Filmix PRO via device code** (`fx.js`): the TV shows a code, the user enters it at `filmix.my/consoles`, the plugin polls and stores `user_dev_token` per user, `Обновить профиль`, days left | Filmix plugin | Promin has one global `PROMIN_FILMIX_TOKEN`. Per-profile Filmix account = the paywalled titles play for that user, legally. Flow identical to the YouTube one we built (poll until linked, stored in the profile). **Cheap and removes the `paywalled` outcome for subscribers.** |
| **Auto-switch to the next source after N s** when the chosen one fails, with a visible countdown ("Источник будет переключен автоматически через 10 секунд") | BWA, Skaz, Lampac | Promin shows "источников не найдено / поток не получен" and waits. Add: on resolve failure or first-bytes timeout, try the next source automatically with a cancellable countdown toast. |
| **Auto-pick a source with staged feedback** ("Подбираем варианты → Проверяем доступность → Готовим просмотр → Уточняем качество") instead of a source list | Lumio, Skaz "Onlyskaz 2.0" | Promin has source pre-resolve on the title page; the *staged* status line during a long resolve is a good UX borrow for the player's "preparing" spinner (we already show queued/preparing — add the stage). |
| **Subscribe to a translation**: "notify me when this voice appears for this series" | Online Mod (`Account.subscribeToTranslation`, cub account), BWA/Skaz | Fits the planned series calendar / bot notifications (backlog #1): a per-episode "new voice" check on the same schedule. Medium. |
| **Remember last balancer per title / prefer DASH / prefer HTTP / default quality** | Online Mod, BWA | Promin remembers the source per title already; default quality setting exists. Nothing new. |
| **Clarification search** ("Уточнить название") when the source's search misses | BWA/Skaz (`clarification_search`) | Promin matches via TMDB titles incl. RU title; a manual "искать на источнике по другому названию" on the source list would fix the residual misses (Kinotochka, Eneyida). Small. |
| CORS workers (`cors.*.workers.dev`), kinobase mirrors + cookies, HDrezka Premium | Online Mod | Client-side hacks for a browser without a server; irrelevant — Promin has the relay. |
| Showy PRO paywall, QR payments, trial | smotret24 | Monetisation of someone else's Lampac. Irrelevant. |

## 2. Torrents

| Plugin | Function | Promin |
|---|---|---|
| `etor` (cub.red) | 3 lines: `lampa_settings.torrents_use = true` — unlocks the Parser/TorrServer menu in the store build | N/A, Promin's torrents are built in. |
| `ts-preload` (rootu) | Before playing a torrent: a modal with **preload progress, speed, seeds/peers**, start when X % buffered | Promin prefetches but shows a spinner + "готовим / в очереди". Show what ts-preload shows: bytes, speed, peers, percent — the data is in the torrent manager already. **Small, visible win**, ties to backlog #13 ("torrent resume on Xiaomi"). |

## 3. Interface / fixes

| Plugin | Function | Promin |
|---|---|---|
| `want.js` | Old-style Home menu: Bookmarks / Like / Later as separate items | Promin has Library with sections; a matter of taste. Skip. |
| `anti.js`, `ot.js` (obfuscated) | Remove the Trailers section, add a "YouTube" menu item pointing at youtube.com; `ot` also removes Anime | Promin has a real YouTube section; trailers/anime shelves are our own rows — a per-user "hide row" toggle on Home would cover both `noanime.js` / `notrailer.js` needs. Small. |
| `t.js` (Skaz TMDB proxy) | Proxies TMDB API + images through the author's domain for regions where TMDB is blocked | Promin's server already proxies TMDB (`/img`, catalog) — solved by design. |
| `not_mobile.js`, `reset_subs.js`, `wsoff.js` | One-line Lampa storage resets: undo touch mode, reset WebOS subtitle params, disable the WS on Android 4–6 with expired root certs | Lampa-specific. The last one is a reminder: **old Android/Crosswalk with expired CA** cannot open our WSS either — the sync socket should fall back to polling (it does: `/sync/events` poll) — verify on a real old Android box (backlog #13). |
| `modss` (lampa.stream) | A plugin manager + seasonal snowfall | Nothing. |

## 4. Elsewhere in the plugin scene (catalogues: lampa-kit, levende, lampaplugins/store, nb557)

Ideas that show up repeatedly and map onto Promin:

- **Ratings on cards** (KP/IMDb/CUB colour-coded) — Promin shows TMDB on cards; an IMDb/KP number via OMDb is already fetched for titles, putting it on the card is small.
- **Profiles per household member** (levende "Profiles") — Promin has profiles (PIN) but no per-profile avatars/kids mode; backlog #6 (kids profile).
- **Trash filter / history filter** (levende "Filters") — hide low-rated / already-watched from Home rows; "unfinished this week" is backlog #7.
- **TMDB Networks** (browse by Netflix/HBO/…) — one more catalog filter; backlog "catalog filters by country / language" neighbours it.
- **Random / mood roulette** — "what to watch" shuffle; trivial on our catalog service.
- **Kinopoisk "Буду смотреть" import / rate from Lampa** — needs a KP account; out of scope.
- **IPTV** (maks-mk/lampatv, Online MODS radio) — a different product; skip unless asked.

## Recommended order

1. **RCH** (residential fetch through the TV) — unlocks half the online sources from the VPS; the one thing the Lampa world has that we do not.
2. **Filmix account per profile** by device code — reuse the YouTube link flow; ends the paywall stub for subscribers.
3. **Auto-switch to the next source with a countdown** + staged "preparing" text.
4. **Torrent preload panel** (percent, speed, peers).
5. **Hide-a-row toggle on Home** (trailers / anime / any row) and ratings on cards.
6. Translation subscriptions — together with the series calendar.
