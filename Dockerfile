# Promin — multi-stage: web (TS→ES5) → go build (embed) → runtime
FROM node:22-alpine AS web
WORKDIR /app
COPY web/package.json web/package-lock.json web/
RUN cd web && npm ci
COPY web/ web/
# Gate the image build on typecheck + es5 (build.js already asserts 0 var()).
RUN cd web && npm run typecheck && npm run typecheck:tg && npm run build && npm run check:es5

FROM golang:1.26-alpine AS build
ARG VERSION=dev
# GO_TAGS=providers compiles in the private online-source providers
# (git submodule server/providers); empty = catalog + torrents only.
ARG GO_TAGS=""
WORKDIR /app
# Modules first: a Go source edit must not invalidate the download layer
# (anacrolix/torrent pulls a large graph).
COPY server/go.mod server/go.sum server/
RUN cd server && go mod download
COPY server/ server/
COPY --from=web /app/server/webdist/ server/webdist/
# Gate the image on vet + tests (CI had no Go test step at all).
RUN cd server && CGO_ENABLED=0 go vet ./... && CGO_ENABLED=0 go test ./...
RUN cd server && CGO_ENABLED=0 go build -trimpath -tags "${GO_TAGS}" \
      -ldflags "-s -w -X main.version=${VERSION}" \
      -o /promin ./cmd/promin

FROM alpine:3.20
# Pin ffmpeg to the tested version — HLS EVENT/inline-audio args are version-
# sensitive, so an unpinned `apk add ffmpeg` could silently change behavior.
RUN apk add --no-cache ca-certificates wget ffmpeg=6.1.1-r8
COPY --from=build /promin /usr/local/bin/promin
# Non-root: ffmpeg and the torrent client parse attacker-influenced media.
# Every write goes under /data (owned by this uid via the pod's fsGroup and
# the chown init step in k8s/promin.yaml).
USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["promin"]
