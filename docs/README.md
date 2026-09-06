# Promin documentation

Reference for the system as it runs today. History, audits and plans are not
kept here; open work lives in [backlog.md](backlog.md).

| Document | Covers |
|---|---|
| [architecture.md](architecture.md) | Components, the two content pipelines, request flow, decisions that still hold |
| [backend.md](backend.md) | Go package map (`server/internal/*`), configuration (`PROMIN_*`), background loops, logging |
| [data-model.md](data-model.md) | SQLite tables, per-user vs per-device data, sync semantics |
| [api.md](api.md) | Every REST and WebSocket endpoint: auth requirement, params, responses |
| [frontend.md](frontend.md) | ES5 build pipeline, D-pad navigation model, input layers, viewport scaling, settings, i18n |
| [player.md](player.md) | Player UI, seek/resume model, quality/voice/subtitle menus, torrent playback |
| [streaming.md](streaming.md) | `/relay`, `/remux`, `/stream`, client capabilities, HTTP/1.1 host for old Samsung, transcode |
| [sources.md](sources.md) | Native online-source providers, TMDB→catalog matching, the residential proxy, adding a provider |
| [torrents.md](torrents.md) | Search (JacRed API), engine, packs and episodes, resume |
| [auth.md](auth.md) | PIN profiles, tokens, media token, devices, admin, sync |
| [deployment.md](deployment.md) | docker-compose + Caddy and k3s, secrets, env table, CI/CD, backups, rollback |
| [diagnostics.md](diagnostics.md) | Diagnostics mode, `/api/v1/diag`, reading logs, verifying a deploy |
| [backlog.md](backlog.md) | Open work and unplanned ideas |

Conventions: English, present tense, code referenced by path. A document
describes behaviour; the reason behind a non-obvious choice goes next to it in
one or two sentences, not in a separate decision log.
