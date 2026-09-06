#!/usr/bin/env bash
# Promin — one-command install on a bare VPS. See docs/deployment.md
#   curl -fsSL https://raw.githubusercontent.com/sviniabanditka/promin/main/install.sh | bash
set -euo pipefail

REPO="https://github.com/sviniabanditka/promin.git"
DIR="/opt/promin"

if ! command -v docker >/dev/null 2>&1; then
  echo "==> Docker not found, installing via get.docker.com"
  curl -fsSL https://get.docker.com | sh
fi
docker compose version >/dev/null 2>&1 || { echo "docker compose plugin is missing"; exit 1; }

if [ -d "$DIR/.git" ]; then
  echo "==> Updating $DIR"
  git -C "$DIR" pull --ff-only
else
  echo "==> Cloning into $DIR"
  git clone --depth 1 "$REPO" "$DIR"
fi
cd "$DIR"

if [ ! -f .env ]; then
  read -rp "Domain (its A record must already point at this server): " domain
  tmdb=""
  while [ -z "$tmdb" ]; do read -rp "TMDB API key (v3, required — https://www.themoviedb.org/settings/api): " tmdb; done
  read -rsp "Admin password for /admin (Enter to skip): " admin; echo
  {
    echo "DOMAIN=${domain}"
    echo "TMDB_API_KEY=${tmdb}"
    echo "OMDB_KEY="
    echo "ADMIN_PASSWORD=${admin}"
  } > .env
  chmod 600 .env
  echo "==> .env created"
fi

docker compose pull || docker compose build
docker compose up -d

# shellcheck disable=SC1091
. ./.env
echo
echo "Done."
echo "  Regular TV / MSX:        https://${DOMAIN}"
echo "  Old Samsung (Tizen):     https://${DOMAIN}:8443"
