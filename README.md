# Promin

Self-hosted media portal for Smart TVs. One Go binary serves a D-pad-first UI,
a TMDB-backed catalog of movies, series, cartoons and anime, online sources
resolved by built-in providers, torrent streaming, PIN-based profiles and
cross-device sync (bookmarks, history, continue watching). Runs on a single VPS;
opens in Media Station X on Samsung Tizen, LG webOS and Android TV, and in any
browser or phone.

## Highlights

- **Catalog** — TMDB with a local cache, Ukrainian/Russian/English UI, search,
  filters, personalized home (recommendations from history, themed shelves).
- **Online sources** — native providers (Ukrainian and Russian dubs, anime)
  matched to TMDB titles server-side; streams are relayed or re-muxed through
  the server so the TV only ever talks to one origin.
- **Torrents** — search via a public JacRed API, an embedded torrent engine that
  streams while downloading, HLS from any offset, audio-track selection,
  season packs with next/previous episode.
- **Player** — resume from position, seek ladder, quality/voice/subtitle menus,
  playback speed, episode strip with stills, next-episode prompt, stats.
- **Old TVs** — a manual "Legacy TV mode" that routes pre-2022 Samsung Tizen
  through an HTTP/1.1-only host and re-muxed streams, ES5 bundle for
  Chromium ~47 webviews, reduce-motion option.
- **Accounts** — 6-digit PIN profiles, device management, admin panel, WebSocket
  sync, encrypted off-site backups.
- **Telegram companion** — search titles from the phone and open them on a TV;
  night mode with adjustable dimming and a sleep timer for late viewing.

## Quick start (bare VPS, docker compose)

Requirements: a domain with an A record pointing at the server, ports 80/443
(and 8443 for legacy TVs) open.

```bash
curl -fsSL https://raw.githubusercontent.com/sviniabanditka/promin/main/install.sh | bash
```

The script installs Docker, clones the repository, asks for the domain, TMDB
key and admin password, and starts `promin` + `caddy` with automatic TLS. Then
point Media Station X on the TV at `https://<domain>` (legacy Samsung:
`https://<domain>:8443`, or switch on "Legacy TV mode" in settings).

Production runs on k3s; both paths are described in
[docs/deployment.md](docs/deployment.md).

## Repository layout

| Path | Contents |
|---|---|
| `server/` | Go backend: HTTP API, catalog, source aggregator, torrents, remux, auth, sync (`server/internal/*`), entry point `server/cmd/promin` |
| `server/providers/` | Online-source providers — private git submodule, compiled in with `-tags providers` |
| `web/` | Frontend: vanilla TypeScript → ES5 bundle embedded into the binary |
| `k8s/` | Production manifests (`promin.yaml`, `promin-h1.yaml`) |
| `docker-compose.yml`, `Caddyfile`, `install.sh` | Single-VPS install |
| `docs/` | Documentation (see below) |

## Documentation

| Document | What it covers |
|---|---|
| [architecture](docs/architecture.md) | Components, content pipelines, request flow, standing decisions |
| [backend](docs/backend.md) | Go package map, configuration, background loops |
| [data-model](docs/data-model.md) | SQLite schema, per-user vs per-device data, sync semantics |
| [api](docs/api.md) | REST/WebSocket reference |
| [frontend](docs/frontend.md) | Build pipeline, navigation model, input layers, scaling, settings |
| [player](docs/player.md) | Player UI, seek and resume model, menus, torrent playback |
| [streaming](docs/streaming.md) | `/relay`, `/remux`, `/stream`, capabilities, legacy TV path |
| [sources](docs/sources.md) | Native online-source providers, matching, adding a provider |
| [torrents](docs/torrents.md) | Search, engine, packs, resume |
| [auth](docs/auth.md) | PIN profiles, tokens, devices, admin, sync |
| [deployment](docs/deployment.md) | Compose and k3s, secrets, env vars, CI/CD, backups |
| [telegram](docs/telegram.md) | Companion bot: search from the phone, open on a TV |
| [diagnostics](docs/diagnostics.md) | Diagnostics mode, `/api/v1/diag`, verifying a deploy |
| [backlog](docs/backlog.md) | Open work |

## Development

```bash
cd web && npm install && npm run build      # → server/webdist/app.js (ES5-checked)
git submodule update --init                 # private providers (optional)
cd server && go test -tags providers ./... && go run -tags providers ./cmd/promin
```

Environment variables are listed in [docs/backend.md](docs/backend.md);
`PROMIN_TMDB_API_KEY` is the only one you need to get started.

## License

MIT — see [LICENSE](LICENSE). Not affiliated with TMDB or any content source.
