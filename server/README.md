# Promin backend

```
cd server
go build -ldflags "-X main.version=$(git rev-parse HEAD)" -o bin/promin ./cmd/promin
PROMIN_HTTP_ADDR=:8080 PROMIN_TMDB_API_KEY=... ./bin/promin
go test ./...
```

The binary embeds the UI from `webdist/` (built by `web/`). Package map,
configuration and background loops: `docs/backend.md`; endpoints: `docs/api.md`.
