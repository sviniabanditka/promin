# Proposal: YouTube in Promin (SmartTube-style, ad-free, SponsorBlock)

Status: analysis only, 2026-09-12. Nothing built.

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
