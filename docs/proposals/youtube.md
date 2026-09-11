# Proposal: YouTube in Promin (SmartTube-style, ad-free, SponsorBlock)

Status: analysis + working extraction prototype, 2026-09-12. **Verdict: viable from the VPS with a signed-in TV client + SABR (see the last two sections). The original anonymous yt-dlp design is dead; the design below is what to build.**

## The gap we would fill

SmartTube is an Android TV app: its own UI over YouTube's internal InnerTube
API, streams played by ExoPlayer, no ad player anywhere in the code, community
SponsorBlock segments skipped automatically, DeArrow titles. It cannot run on
Samsung Tizen or LG webOS, and those are exactly the sets Promin already runs
on. For the Xiaomi (Android TV) box SmartTube itself is the better tool; the
value of a Promin integration is YouTube without ads on the Samsung and LG
panels, plus one remote/one bot for films, series and YouTube together.

## What "no ads" actually means

YouTube inserts ads through its own player and ad-signalling endpoints. A
third-party client that fetches the media streams directly never receives an
ad — there is nothing to block. The whole problem is therefore *stream
extraction*, not ad removal. Server-side ad insertion (ads stitched into the
video stream) has been trialled by YouTube; if it becomes universal, every
third-party client including SmartTube loses, and SponsorBlock-style timing
data would be the only mitigation.

## How extraction works today (and why it hurts)

- The reference implementation is **yt-dlp**. Since late 2025 it needs an
  external JavaScript runtime (Deno) to solve the player's signature/`n`
  challenges; without one, formats are limited and the wiki calls that mode
  deprecated ([announcement](https://github.com/yt-dlp/yt-dlp/issues/15012)).
- YouTube is rolling out **PO tokens** (proof of origin) for stream access;
  missing tokens show up as missing formats, 403s on the media URL, or "Sign
  in to confirm you're not a bot" ([PO Token Guide](https://github.com/yt-dlp/yt-dlp/wiki/PO-Token-Guide)).
  The standard answer is a token-provider sidecar (`bgutil-ytdlp-pot-provider`,
  Node) or cookies of a throwaway account.
- Media URLs are bound to the IP that requested them. Extraction and download
  must leave from the same egress. Our VPS is a Contabo datacenter IP — the
  class of address YouTube challenges most. Our residential proxy has a 1 GB
  monthly package: fine for extraction requests, useless for video bytes.
- Breakage cadence: YouTube changes something every few weeks; yt-dlp ships a
  fix within days. The integration must be able to update the extractor
  independently of a Promin release (sidecar container with `yt-dlp -U`, or a
  pinned image bumped weekly).

## Fit with Promin's architecture

Everything after extraction already exists:

| Need | Existing piece |
|---|---|
| Video and audio come as separate DASH streams (1080p has no muxed format) | `/remux` copy jobs: a two-input `ffmpeg -i video -i audio -c copy` HLS job is a new *kind*, not a new pipeline; offset jobs give seeking |
| Live streams | YouTube serves an HLS manifest → `/relay` already rewrites manifests |
| Old webviews (Chromium 47, no working alternate-audio in hls.js) | the mux above yields a single-variant HLS the TV player plays today |
| SponsorBlock | public API `sponsor.ajay.app/api/skipSegments/<sha256 prefix>` (privacy-preserving hash lookup) → server proxies + caches; the player already has `seek_to`, timecode hooks, toasts |
| Search UI | the new search screen: a "YouTube" filter chip, same grid |
| Phone → TV | `open_title` gains a media kind (`youtube:<videoId>`), Mini App and bot reuse "open on TV" and the D-pad |
| Subscriptions without Google login | channel RSS `youtube.com/feeds/videos.xml?channel_id=` — official, stable, no auth; store subscriptions in our DB, build the feed by polling |
| Watch later, history, continue watching | queue, timecodes and library already keyed by (type, id) — add type `yt` |

Login to a Google account is deliberately out: yt-dlp's OAuth (TV device-code)
support was revoked by Google in 2024; SmartTube keeps its own client
identifiers alive by chasing YouTube's TV client. We would do subscriptions
locally instead.

## Proposed shape (MVP)

1. **Extractor sidecar**: container with yt-dlp + Deno (+ PO token provider),
   HTTP API `GET /extract?id=` → `{formats, hls, subtitles}`. Auto-updates.
   Promin talks to it over the cluster network only.
2. **Server package `youtube`**: search (yt-dlp `ytsearch`/InnerTube browse,
   no signatures needed), video info, channel RSS feeds, SponsorBlock
   segments with a 6 h cache. Source kind `yt` in the sources listing.
3. **Remux kind `mux2`**: two inputs, copy, HLS — reuse queue, caps, offsets.
4. **TV UI**: YouTube filter in search, video card, channel page, player
   segment-skip with a toast and an "unskip" button; settings for SponsorBlock
   categories.
5. **Bot / Mini App**: search results include YouTube hits, "open on TV".

Rough effort: 1–2 weeks to MVP; ongoing maintenance is the real cost.

## Risks, honestly

- **Bot detection from the VPS IP** is the make-or-break item. Test first:
  run yt-dlp with Deno + PO provider from the node for a week against 50
  random videos before writing any UI. If it fails, the fallbacks are a
  throwaway-account cookie jar (ban risk), a larger residential package (money
  per GB of video), or an extractor at home behind a residential IP with a
  tunnel to the cluster.
- **Terms of service**: this is against YouTube's ToS; for a household
  instance the realistic consequence is IP-level blocking, not more.
- **Server bandwidth**: every YouTube byte crosses the VPS twice. Fine for one
  household, but Contabo traffic caps apply.
- **Maintenance**: expect to bump the extractor weekly; without that the
  feature silently dies, which is worse than not having it.

## Decision needed

Go/no-go after the one-week extraction test from the node. If it goes,
build in the order above; SponsorBlock and search first, subscriptions second.

## Why not extract and play on the TV itself?

That is what SmartTube does, and it would sidestep the datacenter-IP problem
entirely: media URLs are bound to the IP that called `/player`, so if the TV
calls it, the TV can download. Three walls stand in the way *in our runtime*
— a web page inside Media Station X on Chromium ~47:

1. **CORS.** InnerTube does not send `Access-Control-Allow-Origin` for foreign
   origins; the browser withholds the response from a promin.club page. No
   JSONP, no media-element trick returns JSON. Proxying `/player` through our
   server defeats the purpose (URLs then bind to the server IP → 403 on the
   TV). MSX does not relax the browser's origin policy.
2. **Player challenge + BotGuard.** The `n`/signature solver must execute
   pieces of YouTube's `base.js`; yt-dlp now ships those solvers as separate
   JS scripts, which a browser could run. BotGuard (PO token) is designed for
   browsers. But a 2016 engine lacks APIs both may need — unverified.
3. **Playback.** 1080p is separate video and audio; without a server mux
   that means DASH: MSE with two source buffers (old shaka/dash.js on
   Chromium 47) or the TV's native pipeline (Tizen AVPlay and webOS both play
   DASH natively — but only from a packaged app).

All three disappear if Promin ships as a **packaged Tizen (.wgt) / webOS
(.ipk) app**: packaged web apps declare `access origin="*"` and make
cross-origin requests freely, and get AVPlay/DASH. That is how every
third-party YouTube client on Samsung/LG works. The price is sideloading:
Samsung developer mode with a yearly certificate, LG Developer Mode with its
50-hour timer (or Homebrew with root) — the same bargain SmartTube users
accept by installing an APK. A packaged app would also give Promin its own
icon without MSX, which is a separate large project.

Middle path without changing platform: a small **home box** (old phone,
router, Pi) behind the household's residential IP running the extractor,
tunnelled to the cluster; extraction and download leave from home, the TV
stays in MSX, the server only muxes and serves. One more device to keep alive.

Order of cheap checks: (1) the one-week yt-dlp test from the node; if the
datacenter IP is challenged, (2) compare home box vs packaged app — the
packaged app carries the bigger product upside.

## Extraction test, 2026-09-12

Same 20 video ids (a `ytsearch20` result), yt-dlp 2026.08.19 with Deno 2.9.6,
default player clients, no cookies, no PO-token provider.

| From | Result |
|---|---|
| Laptop, residential IP (node as JS runtime) | 6/6 tried: 1080p video + opus audio (`616+251`, `399+251`) |
| VPS node 109.199.115.31 (Contabo) | 13/13 tried: `Sign in to confirm you're not a bot` at the `/player` call — no formats at all |
| VPS, forced clients `tv`, `mweb`, `android_vr`, `ios` | all four: the same bot challenge |

Search/metadata (`ytsearch`, flat playlists) works from the VPS; only the
player/stream call is challenged. This is an IP-reputation block, not a
missing JS runtime or a missing token: the same binary from a home IP is fine.

Conclusion: the "extractor on the VPS" MVP is dead as designed. Remaining
options, in order of cost: (a) cookies of a throwaway Google account on the
VPS (works until the account is flagged; account ban risk, IP may still be
challenged), (b) a residential egress for *both* extraction and video bytes —
the existing 1 GB/month proxy package cannot carry video, a larger package is
money per GB, (c) a home extractor box behind the household IP, tunnelled to
the cluster, (d) the packaged Tizen/webOS app doing everything on the TV.

## OAuth device-flow test (YouTube TV client identity), 2026-09-12

Prototype: device code → `google.com/device` → token, then InnerTube from the
VPS with `Authorization: Bearer`.

| Step | Result |
|---|---|
| `o/oauth2/device/code` with the TV client id | 200, user code issued |
| Approval with a throwaway account, token exchange | 200, access token (19 h) + refresh token, scope `youtube` |
| `youtubei/v1/player` with the token, client TVHTML5 (current version from youtube.com/tv) | 200 but `UNPLAYABLE — The page needs to be reloaded`, zero formats |
| same, clients IOS / ANDROID | 400 `INVALID_ARGUMENT` |
| yt-dlp 2026.08.19 + the youtube-oauth2 plugin fed with our token | every API call (even `next`) → 400 Bad Request |

Reading: Google still *issues* tokens to the old TV client identity but
InnerTube rejects them — the same symptom that ended yt-dlp's OAuth support
in November 2024 ("400 using OAuth, cookies work"). SmartTube keeps working
because it impersonates the current Android TV app far more completely
(client identity, versions, device attestation) and repairs it after every
change. Rebuilding that is a SmartTube reimplementation, not a feature.

**Closed paths:** anonymous extraction from the VPS (bot check), OAuth via the
known TV client (rejected by InnerTube).

**Still open, in order of cost:**
1. **Cookies of a throwaway account** on the VPS — the one method yt-dlp's
   wiki still recommends; a 30-minute test once the owner exports a cookie
   file. Expect periodic re-export and account-flag risk.
2. **Home extractor box** behind the household IP (yt-dlp + Deno, tunnel to
   the cluster).
3. **Packaged Tizen/webOS app** that logs into youtube.com/tv inside its own
   webview and plays from the TV's IP — the real "SmartTube for Samsung/LG",
   a separate product.

## OAuth follow-up: SmartTube's exact request, 2026-09-12 (later)

Read SmartTube's engine (MediaServiceCore, commit 0365be3 of the same day):
- Its YouTube login is our flow exactly (youtube.com/o/oauth2 device code,
  scope gdata + paid-content, model `ytlr::`), and the client identity is
  parsed at runtime from the TV app's JS bundle — the same
  `861556708454-d6dlm3lh05idd8npek18k6be8ba3oc68` we used. (The identity in
  its `constants.json` release file is a separate Google project for Drive /
  sign-in and refuses the YouTube scopes.) So our token was fine.
- Auth is supported only on TV clients. The player request pins
  `TVHTML5 7.20260707.07.00`, a Cobalt 4 user agent, no visitorData, region
  fields, a `user` safety chunk, a `cpn`, and a `signatureTimestamp` read from
  `tv-player-ias.js` named by the Cobalt-served youtube.com/tv page. Its own
  comment: a wrong/missing timestamp yields "The page needs to be reloaded".

Reproducing that request from the laptop with our token: **`status=OK`, 31
formats up to 1080p** — the "unplayable" wall was the request shape, not the
token or the IP. **From the VPS the same request also returns `status=OK`**
(31 and 37 formats, up to 2160p60): once signed in through the TV client,
the datacenter IP is no longer challenged at the player call.

But the adaptive formats carry **no URL and no signatureCipher**: the TV
client now delivers adaptive media only through **SABR**
(`streamingData.serverAbrStreamingUrl`, a POST/protobuf/UMP streaming
protocol). Only progressive itag 18 (360p) has a URL, and fetching it gave 403
— it still needs the `n`-parameter transform (nsig) from the player JS.

What this means for a build:
- Extraction = login (done) + player (done) + **SABR client** + **nsig/sig
  solver**. Both exist in the open-source world: YouTube.js with its
  `googlevideo` package implements SABR; yt-dlp's ejs scripts solve nsig in
  Deno. Writing them ourselves is a project; wrapping YouTube.js as a Deno
  sidecar is the realistic route (its TV flow needs the same request fixes
  SmartTube applies — the stock v18 `getBasicInfo(id,'TV')` returned
  UNPLAYABLE for us).
- SmartTube itself marks SABR as TODO and keeps working on URL-bearing
  variants; expect YouTube to finish the SABR migration, at which point every
  third-party client needs the SABR path anyway.

## SABR prototype, 2026-09-12 (end of day) — the last risk closed

`scratchpad/ytoauth/sabr_test.mjs`: YouTube.js 18 (TV client, signed in with
our device-flow token) → player request with the web player's
`signatureTimestamp` (`status=OK`, 30 formats) → decipher
`serverAbrStreamingUrl` → `googlevideo` 4.1 `SabrStream` → 1080p H.264
(itag 137) + opus (251) → first 6 MB to disk → ffmpeg copy-mux → ffprobe
`h264 1920x1080 + opus`.

| From | first byte | 6 MB of video |
|---|---|---|
| laptop, residential | 2.4 s | 8.1 s |
| **VPS node (Contabo)** | **0.4 s** | **0.75 s** |

No PO token was needed for the signed-in TV client. The errors in the log
are the prototype's own byte-limit abort, not YouTube's.

## Build plan (what the result implies)

Sidecar `yt-extractor` (Deno or Node, container next to promin):
- device-code login per Promin profile (bot / TV flow), refresh tokens in the
  DB encrypted with a server key; the same identity SmartTube uses is read
  from the TV app bundle at runtime;
- `GET /stream?id=&quality=` → player request → SABR → **one muxed HLS or
  fMP4 output** fed to promin's remux queue (`-c copy`), so the TV player
  path stays what it is today; seeking via SABR `startTimeMs`;
- search / channel / subscriptions feed via InnerTube (browse endpoints,
  signed in) — the account's real home feed and subscriptions;
- SponsorBlock segments proxied and cached; DeArrow optional;
- weekly dependency bumps (youtubei.js, googlevideo) are the maintenance.

Promin side: source kind `yt`, "YouTube" filter in search, video cards,
channel page, player segment-skip, bot/Mini App "open on TV". Estimate:
2–3 weeks to a first watchable version; the extractor is the part that
breaks and gets fixed.
