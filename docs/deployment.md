# Deployment

Part of the Promin documentation, see [docs/README.md](README.md).

Promin ships as a single container: one Go binary with the web UI embedded,
plus `ffmpeg`. It needs a persistent `/data` directory (SQLite database,
image cache, torrent cache, remux temp files, backups) and a TLS-terminating
reverse proxy in front of it. Everything else — catalog metadata, torrent
search, online sources — is fetched from the internet at runtime.

There are two supported ways to run it:

| | Bare VPS | Production |
|---|---|---|
| Runtime | Docker Compose | k3s |
| Edge / TLS | Caddy (automatic Let's Encrypt) | Traefik + cert-manager, Cloudflare in front of the main host |
| HTTP/1.1-only entry for old Samsung Tizen | `https://<domain>:8443` | `https://h1.promin.club` (SNI-selected TLSOption) |
| Installer | `install.sh` | GitHub Actions (`.github/workflows/promin.yml`) |
| Files | `docker-compose.yml`, `Caddyfile`, `install.sh`, `.env` | `k8s/promin.yaml`, `k8s/promin-h1.yaml` |

## 1. Prerequisites

Common:

- Linux x86_64 host with a public IP. Reference: ~3 vCPU / 6 GB RAM / 50 GB
  disk for the Promin pod (torrent cache is an LRU bounded by
  `PROMIN_TORRENT_CACHE_LIMIT_GB`, default 80 GB — lower it on a small disk).
- A domain whose A record points at the host. The certificate is issued via
  HTTP-01, so DNS must resolve before the first start.
- Inbound TCP 80 (ACME challenge + redirect) and 443.

Bare VPS additionally:

- Docker Engine + Compose plugin (`install.sh` installs them if missing).
- Inbound TCP 8443 for the HTTP/1.1-only listener.

k3s additionally:

- k3s with the bundled Traefik ingress and `local-path` storage class.
- cert-manager with a `ClusterIssuer` named `letsencrypt-prod`.
- Root SSH access to the node (the CI runner imports the image and runs
  `kubectl` over SSH).
- For the main host: a Cloudflare-proxied DNS record. For the HTTP/1.1 host: a
  DNS-only (grey cloud) A record pointing straight at the node, otherwise the
  Cloudflare edge negotiates HTTP/2 again and the old TV breaks.

## 2. Bare VPS: Docker Compose + Caddy

One command:

```bash
curl -fsSL https://raw.githubusercontent.com/sviniabanditka/promin/main/install.sh | bash
```

`install.sh` does the following:

1. Installs Docker via `get.docker.com` if `docker` is missing; exits if the
   Compose plugin is missing.
2. Clones the repository into `/opt/promin` (or `git pull --ff-only` when it is
   already there).
3. If `/opt/promin/.env` does not exist, asks for the domain, an optional TMDB
   API key and an optional admin password, and writes `.env` (mode 600). An
   existing `.env` is never rewritten.
4. `docker compose pull` (falls back to `docker compose build` when the image
   cannot be pulled) and `docker compose up -d`. The `ghcr.io` package is
   private on purpose — the image built by CI has the private providers
   compiled in — so a public install always takes the build path (5–8 min).
5. Prints the two URLs to enter in Media Station X: `https://<domain>` and
   `https://<domain>:8443` for old Samsung Tizen.

Manual equivalent:

```bash
git clone https://github.com/sviniabanditka/promin.git /opt/promin
cd /opt/promin
printf 'DOMAIN=%s\nTMDB_API_KEY=\nOMDB_KEY=\nADMIN_PASSWORD=\n' promin.example.com > .env
docker compose up -d
```

### `.env`

`.env` is gitignored. Keys:

| Key | Used by | Meaning |
|---|---|---|
| `DOMAIN` | caddy | Site name Caddy serves and obtains a certificate for. |
| `TMDB_API_KEY` | promin (`PROMIN_TMDB_API_KEY`) | Required; compose refuses to start without it. |
| `OMDB_KEY` | promin (`PROMIN_OMDB_KEY`) | Empty disables IMDb ratings. |
| `ADMIN_PASSWORD` | promin (`PROMIN_ADMIN_PASSWORD`) | Empty leaves `/admin` unconfigured. |

Any other `PROMIN_*` variable from section 5 can be added to the `promin`
service's `environment:` list in `docker-compose.yml`.

### `docker-compose.yml`

Two services on a private network `internal`; only `caddy` publishes ports.

- `promin` — image `ghcr.io/sviniabanditka/promin:latest` (`build: .` for a
  local build), volume `promin_data:/data`, healthcheck `GET /healthz`, limits
  2 CPU / 4 GB.
- `caddy` — `caddy:2`, ports `80`, `443` (TCP+UDP for HTTP/3), `8443`, mounts
  `./Caddyfile`, volumes `caddy_data` (certificates) and `caddy_config`,
  limits 0.25 CPU / 512 MB.

### `Caddyfile`

```caddyfile
{
	servers :8443 {
		protocols h1
	}
}

{$DOMAIN} {
	reverse_proxy promin:8080
}

{$DOMAIN}:8443 {
	reverse_proxy promin:8080
}
```

- The global options block is first and unique; `servers :8443 { protocols h1 }`
  restricts ALPN on that listener to HTTP/1.1 only.
- `{$DOMAIN}` (port 443, plus 80 for ACME/redirect) is the normal site with
  Caddy defaults (h1/h2/h3).
- `{$DOMAIN}:8443` is the same site on the HTTP/1.1-only listener; Caddy
  reuses the certificate by host name.

## 3. Production: k3s

Everything Promin runs in namespace `promin`, defined by two manifests.

### `k8s/promin.yaml` — the application

| Resource | Name | Notes |
|---|---|---|
| Namespace | `promin` | Created by this manifest. |
| PersistentVolumeClaim | `promin-data` | 40 Gi, `local-path`, RWO. |
| Deployment | `promin` | 1 replica, `strategy: Recreate` (RWO volume), image `ghcr.io/sviniabanditka/promin:latest` with `imagePullPolicy: IfNotPresent` (the image is imported into containerd by CI, see section 6), readiness `GET /healthz`, requests 100m/128Mi, limit 2Gi memory. |
| Service | `promin` | ClusterIP 8080. |
| Ingress | `promin` | Host `promin.club` → `promin:8080`. TLS secret `promin-tls` covers `promin.club` and `promin.sviniabanditka.com`. Annotation `cert-manager.io/cluster-issuer: letsencrypt-prod`. |
| TLSOption (`traefik.io/v1alpha1`) | `h1only` | `alpnProtocols: ["http/1.1"]`. |
| Ingress | `promin-h1only` | Hosts `h1.promin.club` and `promin.sviniabanditka.com` → `promin:8080`, annotation `traefik.ingress.kubernetes.io/router.tls.options: promin-h1only@kubernetescrd`, TLS secret `promin-h1-tls`. |

Traefik picks the TLSOption by SNI, so `promin.club` keeps HTTP/2 while
`h1.promin.club` is served over HTTP/1.1 only. The app knows both names via
`PROMIN_MAIN_HOST` / `PROMIN_H1_HOST`; a device with the "old TV mode" switch
on moves itself to the H1 host and back when the switch is off. There is no
User-Agent detection.

Environment set in the manifest (values in git): `PROMIN_HTTP_ADDR=:8080`,
`PROMIN_DATA_DIR=/data`, `PROMIN_H1_HOST=h1.promin.club`,
`PROMIN_MAIN_HOST=promin.club`, `PROMIN_JACRED_BASE_URL=http://jac.red`,
`PROMIN_TORRENT_CACHE_LIMIT_GB=80`, `PROMIN_TORRENT_MAX_ACTIVE=3`,
`PROMIN_REMUX_MAX_TRANSCODES=1`, `PROMIN_NATIVE_SOURCES=true`,
`PROMIN_OMDB_KEY` (public demo key). The rest come from secrets (section 4).

### `k8s/promin-h1.yaml` — legacy HTTP/1.1 front (fallback)

An nginx `1.27-alpine` Deployment `promin-h1` with `http2` off, listening on
`:8444` with the `promin-tls` certificate, proxying to
`promin.promin.svc.cluster.local:8080`. Service `promin-h1` is `type:
LoadBalancer`, so k3s ServiceLB binds node port 8444 directly, bypassing
Traefik. It serves `https://promin.sviniabanditka.com:8444` for devices that
have not moved to `h1.promin.club`. CI applies and restarts it together with
the app.

### Cloudflare and DNS

| Name | DNS | Path |
|---|---|---|
| `promin.club` | Cloudflare proxied | Cloudflare edge → Traefik `:443` → `promin` |
| `h1.promin.club` | DNS-only A record to the node | Traefik `:443` (TLSOption `h1only`) → `promin` |
| `promin.sviniabanditka.com` | DNS-only A record to the node | Traefik `:443` (h1only) or nginx `:8444` → `promin` |

Both certificates are issued by cert-manager via HTTP-01 through Traefik.

### First-time setup on a fresh cluster

```bash
export KUBECONFIG=/etc/rancher/k3s/k3s.yaml
kubectl apply -f k8s/promin.yaml            # creates the namespace too
kubectl -n promin create secret generic promin-secrets \
  --from-literal=admin-password='...' \
  --from-literal=tmdb-api-key='...' \
  --from-literal=native-source-base-url='...'
kubectl -n promin create secret generic lampac-proxy --from-literal=url='...'
kubectl -n promin create secret generic promin-logs  --from-literal=password='...'   # optional
kubectl apply -f k8s/promin-h1.yaml
```

Then trigger the `promin` workflow (or push to `main`) so the image gets
imported into the node; until then the pod stays `ImagePullBackOff`.

## 4. Secrets

Never committed. Names and keys only:

| Secret (ns `promin`) | Key | Consumed as | Required |
|---|---|---|---|
| `promin-secrets` | `admin-password` | `PROMIN_ADMIN_PASSWORD` | yes |
| `promin-secrets` | `tmdb-api-key` | `PROMIN_TMDB_API_KEY` | yes — the binary exits without a TMDB key |
| `promin-secrets` | `native-source-base-url` | `PROMIN_NATIVE_SOURCE_BASE_URL` | optional — without it the corresponding provider is off |
| `lampac-proxy` | `url` | `PROMIN_NATIVE_PROXY_URL` — residential HTTP proxy URL used only for provider catalog pages that block datacenter IPs | optional — without it those providers are off |
| `promin-logs` | `password` | `PROMIN_LOGS_PASSWORD` | optional — without it `/logs` is disabled |

GitHub Actions repository secrets: `SSH_PRIVATE_KEY` (deploy key for
`root@ssh.sviniabanditka.com`), `BACKUP_PASSPHRASE` (section 8).
`GITHUB_TOKEN` is used for `ghcr.io` login.

## 5. Environment variables

All variables read by `server/internal/config/config.go`. Unset or empty means
default. Durations use Go syntax (`30s`, `24h`).

| Variable | Default | Meaning |
|---|---|---|
| `PROMIN_HTTP_ADDR` | `:8080` | Listen address. |
| `PROMIN_DATA_DIR` | `/data` | Root for images, torrents, remux, backups. |
| `PROMIN_DB_PATH` | `$PROMIN_DATA_DIR/promin.db` | SQLite file. |
| `PROMIN_JACRED_BASE_URL` | `http://jac.red` | Torrent indexer (JacRed API). |
| `PROMIN_JACRED_APIKEY` | empty | Indexer `apikey`. |
| `PROMIN_TMDB_API_KEY` | — (required) | TMDB v3 key; from secret `promin-secrets/tmdb-api-key` in k3s. |
| `PROMIN_TMDB_BASE_URL` | `https://api.themoviedb.org/3` | TMDB API base. |
| `PROMIN_TMDB_FALLBACK_URLS` | `-` (none) | Comma-separated full TMDB mirrors tried after the primary fails. |
| `PROMIN_OMDB_KEY` | empty | OMDb key for IMDb ratings; empty disables. |
| `PROMIN_LOG_LEVEL` | `info` | `debug`, `info`, `warn`, `error`. |
| `PROMIN_LOGS_PASSWORD` | empty | Basic Auth password for the `/logs` web UI; empty disables (404). |
| `PROMIN_ADMIN_PASSWORD` | empty | Bootstraps/resets the admin (user 1) password on boot. |
| `PROMIN_PIN_SECRET` | empty → random, stored in `$PROMIN_DATA_DIR/pin_secret` | Keys the PIN lookup hash; rotating invalidates every PIN. |
| `PROMIN_FFMPEG_PATH` | `ffmpeg` | ffmpeg binary. |
| `PROMIN_FFPROBE_PATH` | `ffprobe` | ffprobe binary. |
| `PROMIN_REMUX_MAX_TRANSCODES` | `1` | Concurrent HEVC/AV1→H264 transcodes (~2–3 vCPU each). |
| `PROMIN_REMUX_MAX_COPY` | `4` | Concurrent copy-remux ffmpeg jobs. |
| `PROMIN_REMUX_JOB_TTL` | `30m` | Idle time before a remux job's temp dir is removed. |
| `PROMIN_TORRENT_PORT` | `0` (library default 42069) | BitTorrent peer port. |
| `PROMIN_TORRENT_MAX_ACTIVE` | `5` | Torrents kept on disk; oldest inactive is evicted. |
| `PROMIN_TORRENT_CACHE_LIMIT_GB` | `80` | On-disk torrent LRU cache bound. |
| `PROMIN_TORRENT_METADATA_TIMEOUT` | `30s` | Wait for magnet metadata. |
| `PROMIN_WEATHER_PLACE` | empty → by visitor country (`CF-IPCountry`) | Screensaver forecast city. |
| `PROMIN_H1_HOST` | empty | HTTP/1.1-only host devices in old-TV mode move to. |
| `PROMIN_MAIN_HOST` | empty | Main host they move back to. Both empty → no steering. |
| `PROMIN_BACKUP_DIR` | `$PROMIN_DATA_DIR/backups` | Where SQLite snapshots land. |
| `PROMIN_BACKUP_INTERVAL` | `24h` | Snapshot interval; `0` disables. |
| `PROMIN_BACKUP_KEEP` | `7` | Snapshots retained. |
| `PROMIN_NATIVE_SOURCES` | `false` | Enable the built-in online-source providers. |
| _provider-specific `PROMIN_*` variables_ | see the private `server/providers` submodule README | Each source reads its own base URL / token there; not part of the public config package. |
| `PROMIN_NATIVE_PROXY_URL` | empty | `http://user:pass@host:port` residential proxy for providers that block datacenter IPs; empty disables them. |

## 6. CI/CD

Workflow `.github/workflows/promin.yml`, job `build-and-deploy`.

Triggers: push to `main` touching `server/**`, `web/**`, `Dockerfile`,
`k8s/promin.yaml`, `k8s/promin-h1.yaml` or the workflow file itself; or
`workflow_dispatch`. Documentation, compose, Caddyfile and `install.sh` changes
do not deploy.

Steps:

1. `docker/build-push-action` builds the multi-stage `Dockerfile` (web
   typecheck + ES5 build → `go vet` + `go test` → static binary → `alpine` with
   pinned `ffmpeg`) with `--build-arg VERSION=<git sha>` and pushes
   `ghcr.io/sviniabanditka/promin:latest` and `:<sha>`. Build cache is GHA.
2. The runner pulls `:latest`, `docker save | gzip`, streams it over SSH to the
   node and runs `k3s ctr images import -`. This is why the Deployment uses
   `imagePullPolicy: IfNotPresent`: the repository is private and the node has
   no registry credentials.
3. `scp` of `k8s/promin.yaml` and `k8s/promin-h1.yaml` to `/tmp/` on the node,
   then `kubectl apply -f` both, `kubectl -n promin rollout restart
   deploy/promin deploy/promin-h1`, and `rollout status` with 120 s / 90 s
   timeouts. Nothing outside namespace `promin` is applied or restarted.

The `Recreate` strategy means a deploy has a few seconds of downtime while the
old pod releases the RWO volume.

## 7. Verifying a deploy

```bash
curl -s https://promin.club/api/v1/ping
# {"pong":true,"version":"<git sha>","h1_host":"h1.promin.club","main_host":"promin.club"}
```

`version` is the commit the image was built from (`-X main.version`); compare
it with the SHA of the workflow run. `dev` means a local build without
`VERSION`. `GET /healthz` returns `{"status":"ok"}` and is what the readiness
probe and the Compose healthcheck use.

```bash
kubectl -n promin get pods
kubectl -n promin rollout status deploy/promin
curl -sI https://h1.promin.club/ --http1.1 | head -1     # h1 host answers
```

Compose: `docker compose ps` shows `promin` as `healthy`;
`curl -s https://$DOMAIN/api/v1/ping`.

## 8. Backups

Two layers.

**On-box snapshots (in-app, `server/internal/store/backup.go`).** Every
`PROMIN_BACKUP_INTERVAL` (default 24 h, first one 90 s after boot) the app runs
`VACUUM INTO` — a consistent copy while serving traffic — into
`PROMIN_BACKUP_DIR` (default `/data/backups`) as `promin-<UTC
yyyymmdd-hhmmss>.db`, then prunes to `PROMIN_BACKUP_KEEP` (default 7) files.
Only files matching that name pattern are ever deleted. The database is small
(~13 MB); the image and torrent caches are regenerable and are not backed up.

**Off-site copy (`.github/workflows/backup.yml`).** Daily at 03:17 UTC (and on
`workflow_dispatch`) the runner SSHes to the node, finds the newest snapshot in
the pod (or takes one if none exists), streams it through
`openssl enc -aes-256-cbc -pbkdf2 -iter 200000` and uploads the ciphertext as
a workflow artifact `promin-db-<stamp>` with 90-day retention. The passphrase
is the GitHub Actions secret `BACKUP_PASSPHRASE`; when it is not set the job
exits without uploading anything. Uploads smaller than 10 KB fail the job.

Restore (either layer):

```bash
# off-site artifact only:
openssl enc -d -aes-256-cbc -pbkdf2 -iter 200000 -in promin-<ts>.db.enc -out promin.db

# k3s (local-path volumes live on the node under /var/lib/rancher/k3s/storage/)
kubectl -n promin scale deploy/promin --replicas=0
ssh root@ssh.sviniabanditka.com 'cd /var/lib/rancher/k3s/storage/pvc-*_promin_promin-data && cp backups/promin-<ts>.db promin.db'
kubectl -n promin scale deploy/promin --replicas=1

# compose
docker compose stop promin
docker run --rm -v promin_promin_data:/data -v "$PWD":/restore alpine cp /restore/promin.db /data/promin.db
docker compose start promin
```

## 9. Logs

- k3s: `kubectl -n promin logs -f deploy/promin` (`deploy/promin-h1` for the
  nginx front). Structured `slog` lines; level via `PROMIN_LOG_LEVEL`.
- Compose: `docker compose logs -f promin`, `docker compose logs -f caddy`
  (Caddy writes JSON access logs to stdout).
- Web: `https://<host>/logs` shows the in-memory ring buffer behind Basic Auth
  when `PROMIN_LOGS_PASSWORD` is set (secret `promin-logs` in k3s).
- Client-side probes posted by the UI are logged under `msg=diag`:
  `kubectl -n promin logs deploy/promin | grep diag`.

## 10. Upgrading

- k3s: merge to `main` (or run the `promin` workflow manually). Confirm with
  `/api/v1/ping` (section 7). Manifest-only changes to `k8s/promin.yaml` or
  `k8s/promin-h1.yaml` also go through the workflow.
- Compose:

  ```bash
  cd /opt/promin && git pull --ff-only && (docker compose pull || docker compose build) && docker compose up -d
  ```

  Volumes are untouched; only changed containers are recreated. Re-running
  `install.sh` does the same and keeps the existing `.env`.

Schema migrations run automatically on boot; a snapshot is taken 90 s after
start, so take a manual one before a risky upgrade if the last nightly is old:
`kubectl -n promin exec deploy/promin -- cp /data/promin.db /data/backups/manual-$(date -u +%Y%m%d-%H%M%S).db`.

## 11. Rollback

The Deployment always references `:latest`, so `kubectl rollout undo` restarts
the same image and does not roll anything back. Options:

1. **Revert in git** — `git revert <bad sha> && git push`; CI builds and
   deploys the previous code. Preferred: history and the live image stay in
   sync.
2. **Re-import an older image** — every build is also tagged `:<sha>` in
   `ghcr.io`. From a machine logged in to `ghcr.io`:

   ```bash
   docker pull ghcr.io/sviniabanditka/promin:<good sha>
   docker tag  ghcr.io/sviniabanditka/promin:<good sha> ghcr.io/sviniabanditka/promin:latest
   docker save ghcr.io/sviniabanditka/promin:latest | gzip | \
     ssh root@ssh.sviniabanditka.com 'gunzip | k3s ctr images import -'
   ssh root@ssh.sviniabanditka.com 'KUBECONFIG=/etc/rancher/k3s/k3s.yaml kubectl -n promin rollout restart deploy/promin'
   ```

   The next push to `main` overwrites it again.
3. **Compose** — `git checkout <good sha> && docker compose up -d --build`
   (or `docker compose pull` after retagging as above).

If a migration changed the schema in a way the older binary rejects, restore
the pre-upgrade snapshot from section 8 first.


## Providers submodule

Online-source providers are a private git submodule (`server/providers` →
`sviniabanditka/promin-providers`). Clone with `git clone --recurse-submodules`
(or `git submodule update --init`) and build with `-tags providers`
(`GO_TAGS=providers` for Docker / compose). CI checks out the submodule with the
repository secret `SUBMODULES_TOKEN` — a fine-grained personal access token with
read access to `promin-providers` — and passes `GO_TAGS=providers` to the image
build. A checkout without the submodule still builds and runs: catalog, torrents,
accounts and sync work, the sources tab is empty.

## Monitoring

`k8s/monitoring.yaml` (applied by the same workflow) plugs Promin into the
cluster's kube-prometheus-stack (namespace `monitoring`, Grafana at
`grafana.sviniabanditka.com`):

- **blackbox-exporter** probes the public URLs the TVs use —
  `https://promin.club/healthz`, `/api/v1/ping` and `https://h1.promin.club/healthz`
  (the last one asserts HTTP/1.1, so a broken ALPN option is caught).
- **Probe** objects carry `release: kube-prometheus`, which is the selector the
  operator's Prometheus uses.
- **PrometheusRule `promin`**: `ProminDown` (probe failing 3 min, critical),
  `ProminSlow`, `ProminPodRestarting`, `ProminMemoryHigh`, `ProminDataDiskFilling`.
- **Dashboard "Promin"** is provisioned from the ConfigMap labelled
  `grafana_dashboard=1`: availability, up/down per host, deployed image,
  restarts, probe latency, memory vs limit, CPU, data volume, network.

Alerts are delivered to Telegram. `k8s/alerting.yaml` is an `AlertmanagerConfig`
used as the **global** Alertmanager configuration (Helm value
`alertmanager.alertmanagerSpec.alertmanagerConfiguration.name=promin-alerts`,
set once with `helm upgrade --reuse-values`); the bot token is the secret
`monitoring/alertmanager-telegram` (key `token`), the chat id is in the manifest.
`Watchdog` and `InfoInhibitor` are routed to `null`; everything else, firing
and resolved, goes to the chat with a 4 h repeat.
