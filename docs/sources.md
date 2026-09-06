# Online sources

Promin plays online video through its own scraper providers. Each provider knows
one third-party catalog; the aggregator in `server/internal/sources` maps a TMDB
title onto that catalog, asks the provider for a stream, and hands the client a
URL on Promin's own origin (`/relay` or `/remux`). The TV never talks to a
source or its CDN directly.

Code map:

| Piece | Path |
|---|---|
| Provider interface, egress (`DirectFetcher`, `NewProxyFetcher`) | `server/internal/sources/provider/provider.go` |
| One package per provider | `server/internal/sources/provider/<id>/` |
| Aggregator: matching, cache, resolve | `server/internal/sources/native.go` |
| Service facade (`Online`, `Resolve`) | `server/internal/sources/service.go` |
| Wire DTOs (`OnlineSource`, `ResolveResponse`, …) | `server/internal/sources/types.go` |
| `/relay` + `/remux` URL wrapping | `server/internal/sources/relay.go`, `parse.go` |
| Registration and config wiring | `server/cmd/promin/main.go`, `server/internal/config/config.go` |
| HTTP handlers | `server/internal/httpapi/handlers_sources.go` |

## 1. Provider interface

```go
type Provider interface {
    ID() string
    Search(ctx, query string, page int, pc Ctx) ([]SearchItem, error)
    Title(ctx, sourceID string, pc Ctx) (TitleDetails, error)
    Resolve(ctx, req ResolveRequest, pc Ctx) (ResolvedStream, error)
}
// optional
type Lookuper interface {
    Lookup(ctx, ref ExternalRef, pc Ctx) (SearchItem, bool, error)
}
```

- `ID()` is the registry key. Source ids are prefixed with it
  (`<provider>:<catalog id>`); the id is also the value the
  client sends back as `balanser`.
- `Search` returns normalized `SearchItem`s (`Title`, optional `OriginalTitle`,
  `Year`, `Type` movie|tv). The aggregator ranks them; a provider never guesses.
- `Title` returns `TitleDetails`: seasons/episodes in the *source's* numbering
  and the list of `AudioTrack`s (dubs).
- `Resolve` returns one `ResolvedStream`: raw upstream `URL`, `Kind` hls|mp4,
  optional `Variants` (one URL per quality, best first), `Subtitles`, `Audio`
  (id of the dub carried) and `AudioNames` (labels for HLS audio renditions
  whose manifest names are placeholders).
- `Lookuper` is an exact lookup by external id. Providers that have an id map
  (an id map) or are keyed by an external id implement it; the
  aggregator tries it before any name search when the request carries `imdb_id`.

Rules every provider follows:

- All egress goes through `Ctx.Fetch` (`Get`, `PostForm`, `PostJSON`). Never
  `http.DefaultClient`. This is what makes the proxy swap possible without
  touching a provider.
- Errors are typed: `ErrSourceUnavailable` (network / non-2xx, retryable),
  `ErrParse` (markup changed, needs a fix), `ErrResolveFailed` (reached and
  parsed, no stream for the selection).
- Parsing is a pure function of fetched bytes, so it is unit-tested from saved
  fixtures under `testdata/`.

`Ctx` carries `Fetch`, `BaseURL`, an optional `Referer` and a `Log` hook.
`DirectFetcher` (15 s timeout, 8 MiB body cap, TV-like UA) is the default.
`NewProxyFetcher(url)` is the same fetcher over an HTTP proxy with a desktop
Chrome UA and a 25 s timeout.

## 2. Aggregator

### Listing: `GET /api/v1/sources/online`

`Service.Online` asks every enabled provider concurrently whether it carries
the title (`nativeSources`, budget `nativeListBudget` = 6 s) and returns the
providers that matched as `OnlineSource{id, name, balanser}`. A provider that
errors or does not carry the title is simply absent; the listing never fails.
No stream is resolved at this stage.

The handler enriches the request with `TitleRU` — the Russian TMDB title,
fetched from the catalog cache (`sourcesHandlers.ruTitle`). Several catalogs
are Russian-language and know neither the Ukrainian UI title nor the original.

### Matching a TMDB title to a catalog id — `matchSource`

1. **Cache.** Key `nsrc:match:v<matchLogicVersion>:<provider>:<type>:<tmdb_id>`
   in the shared KV cache table (`NewStoreCache` over `store.TMDBCacheRepo`).
   A hit stores the source id for `matchTTL` (7 days); a miss stores an empty
   id for `matchMissTTL` (15 min). A network or parse failure is **not**
   cached, so a transient outage does not hide a source for hours.
   `matchLogicVersion` is part of the key: bump it whenever matching or query
   building changes, otherwise cached negative verdicts from the old logic
   survive and mask the fix.
2. **Lookup by imdb id** when the provider is a `Lookuper` and the request
   carries `imdb_id`.
3. **Search variants** — `searchQueries` builds, for each of `Title`,
   `TitleRU`, `OriginalTitle`: the title as is; the title without the
   "Episode N" segment (`dropEpisodeSegment`); each punctuation-flattened
   (`normalizeTitle`); the franchise name alone and the subtitle alone
   (`splitSubtitle`, subtitle only if ≥ 4 chars). Deduped, tried in order,
   each with `nativeBudget` = 10 s; the loop stops at the first accepted match.
4. **Ranking** — `pickMatch` scans every hit and keeps the best `rankCandidate`:
   - `rankExactTitleAndYear` — normalized titles equal (any of the request's
     names vs the hit's title or original title, with and without the episode
     segment), year within ±1. Cannot be beaten, stops the scan.
   - `rankExactTitleNoYear` — titles equal, no year on one side; or both sides
     are `tv` (a season page carries the season's year, not the premiere).
   - `rankNumeralVariantExactYear` — titles differ only by numeric tokens
     ("Дюна 2: Часть вторая" vs "Дюна: Часть вторая"), years identical.
   - Same title, clearly different year → rejected (remake/reboot).
   `normalizeTitle` lowercases, folds ё→е, і/ї→и, drops apostrophes, turns
   punctuation into spaces; `titlesMatch` also compares space-free forms so
   "Місто 2.0" equals "Місто 20".
5. **Fallback by year** — `pickByOriginalQueryYear`: the query was the original
   title (as typed or flattened), the catalog returned ≤ 6 items and exactly one
   carries the release year → accept it. This is how a Russian-only catalog
   matches a title whose localized name cannot be compared.

Matching is deliberately strict: a loose match plays the wrong film, which is
worse than showing no source.

### Resolve: `GET /api/v1/sources/online/resolve` — `resolveNative`

1. `matchSource` again (usually a cache hit). No match →
   `{streams: [], unresolved: "not_found"}`.
2. `Title()` for the dub list → `voices[]` (so the player can offer dubs on the
   first resolve).
3. `Resolve()` with `season`, `episode`, `voice`. Failure →
   `{streams: [], voices, unresolved: "resolve_failed"}`. The whole step runs
   under `navigationBudget` = 25 s.
4. Streams: if the provider returned `Variants`, one `Stream` per quality
   (`quality: "1080p"`, `label`); otherwise a single `{quality: "auto", label:
   <provider name>}`. `voice` echoes the requested dub or the provider's default
   (`ResolvedStream.Audio`); `audio_names` passes through; `type` is the stream
   kind (default `hls`). Subtitles are wrapped in `/relay` too — the relay
   converts SRT to WebVTT on the fly (`httpapi/relay_subtitle.go`).

Resolved stream URLs are **never cached**: CDN links carry expiring tokens.

### `wrapStream` — `/relay` vs `/remux`

`parse.go`: an `.m3u8` URL for a client that sent `demuxed_hls=false`
(`PreferMuxed`, old Samsung webviews that cannot play HLS with a separate audio
group) is wrapped as `/remux?u=<b64>&kind=copy_hls&audio=0`; everything else
becomes `/relay?u=<base64url(raw)>`. `/relay` rewrites every URI in an HLS
manifest back through itself and stamps the caller's `?t=` media token on each
child (`httpapi/relay.go`).

## 3. Registered providers

The scraper implementations are not part of this repository. They live in the
private repository `sviniabanditka/promin-providers`, mounted as the git
submodule `server/providers`, and are compiled in with `go build -tags providers`
(`cmd/promin/providers_private.go`); without the tag `providers_none.go`
returns an empty set and Promin runs with the catalog and torrents only.

`providers.Build(Deps)` in the submodule reads each source's `PROMIN_*` base
URL/token from the environment and returns the enabled `sources.NativeProvider`
list plus an optional background task (an id-map refresh). The README of the
submodule lists the sources, what each is keyed by (search vs. kinopoisk id)
and which ones fetch their catalog pages through the residential proxy
(`PROMIN_NATIVE_PROXY_URL`, k8s secret `lampac-proxy/url`). Video never goes
through the proxy: streams are relayed directly by `/relay`.

## 4. Adding a provider

1. **Probe from the VPS.** The production IP is what matters: `curl` the
   search endpoint, a title page and the player page from the server (or through
   the proxy with `-x` when the catalog blocks datacenters). Record which of
   pages / player / CDN need the proxy. Live checks in
   `server/internal/sources/*_live_test.go` are skipped unless an env var is set
   (env-gated `*_LIVE` variables in the submodule); use the same pattern.
2. **Save fixtures** under `server/internal/sources/provider/<id>/testdata/`:
   a search page, a movie page, a series page, a player/playlist payload. Keep
   them small and real.
3. **Write the package** `provider/<id>/<id>.go`: `const ID = "<id>"`,
   `New()`, and exported pure parsers (`ParseCards`, `ParsePage`,
   `ParsePlaylist`, …) that take bytes/strings. `Search`/`Title`/`Resolve` only
   fetch via `pc.Fetch` and call the parsers. Prefix source ids with `ID + ":"`.
   Return `provider.ErrParse` when a regexp or JSON shape does not match,
   `provider.ErrResolveFailed` when the selection has no stream. Implement
   `Lookuper` if the source is keyed by an external id. Fill `OriginalTitle`
   when the catalog exposes it — it improves matching.
4. **Parse tests** `<id>_test.go` over the fixtures: card count, title, year,
   type, id, episode count, variant URLs. These are what catch a markup change.
5. **Config**: add `<Name>BaseURL` (and token if any) to `config.Config` and
   `Load()` as `PROMIN_<NAME>_BASE_URL` with the public host as default; secrets
   never get a default.
6. **Wire in `main.go`** inside the `cfg.NativeSourcesEnable` block: append a
   `sources.NativeProvider{P: <id>.New(...), Name: "<Display>", Enabled: true,
   Ctx: provider.Ctx{Fetch: fetcher | pf, BaseURL: ..., Log: plog}}`. Use `pf`
   (the proxy fetcher) only for providers whose catalog blocks the VPS, and put
   them inside the `cfg.NativeProxyURL != ""` branch.
7. **Bump `matchLogicVersion`** in `native.go` if you touched anything in
   query building or ranking. Run `go test ./internal/sources/...`.
8. Deploy, open a title the source is known to carry, and check the server log:
   `native: no catalog match` means the matcher ran and rejected;
   `native: search failed` means the fetch or parse failed; silence means a
   cache hit.
