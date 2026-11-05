#!/usr/bin/env bash
# Install or upgrade Probara with Docker Compose from prebuilt release images.
#
#   curl -fsSL https://raw.githubusercontent.com/yassinebenameur/probara/dev/scripts/install.sh | bash
#   scripts/install.sh --version 2.0.0 --dir /opt/probara
#
# It downloads docker-compose.yml and docker-compose.release.yml at the
# release tag into the install directory, writes .env there with generated
# secrets, and starts the stack. Re-running it is safe: a value already in
# .env is never changed (only --version updates PROBARA_VERSION, which is how
# you upgrade), and secrets are generated only for keys that are missing or
# empty. Keep .env: losing PROBARA_SECRETS_KEY makes stored credentials
# unreadable, and the database password is fixed when the volume is created.
#
# Options:
#   --version X     release to install (default: the newest GitHub release,
#                   prereleases included; or PROBARA_VERSION already in .env)
#   --dir DIR       install directory (default: ./probara)
#   --source DIR    copy the Compose files from a checkout instead of downloading
#   --no-start      write the files and .env, then stop before `docker compose up`
#
# Needs: docker with Compose 2.24.4+, curl, openssl.

set -euo pipefail

REPO="${PROBARA_REPO:-yassinebenameur/probara}"
# Generates the NATS auth-callout issuer (an nkey account pair); pinned.
NATS_BOX_IMAGE="${NATS_BOX_IMAGE:-natsio/nats-box:0.19.2}"

version=""
dir="probara"
source_dir=""
start=true

die() { echo "install.sh: $*" >&2; exit 1; }
log() { echo "==> $*"; }

while [[ $# -gt 0 ]]; do
  case "$1" in
    --version) [[ $# -ge 2 ]] || die "--version needs a value"; version="${2#v}"; shift 2 ;;
    --dir) [[ $# -ge 2 ]] || die "--dir needs a value"; dir="$2"; shift 2 ;;
    --source) [[ $# -ge 2 ]] || die "--source needs a value"; source_dir="$2"; shift 2 ;;
    --no-start) start=false; shift ;;
    -h|--help) echo "usage: install.sh [--version X] [--dir DIR] [--source DIR] [--no-start]"; exit 0 ;;
    *) die "unknown option: $1 (see --help)" ;;
  esac
done

for tool in docker openssl; do
  command -v "$tool" >/dev/null 2>&1 || die "$tool is required"
done
docker compose version >/dev/null 2>&1 || die "Docker Compose v2 is required (docker compose version)"

mkdir -p "$dir"
cd "$dir"
env_file=".env"
umask 077
touch "$env_file"
chmod 600 "$env_file"

# env_value KEY prints KEY's value in .env (empty when absent).
env_value() {
  local line
  line="$(grep -E "^$1=" "$env_file" | tail -n 1 || true)"
  printf '%s' "${line#*=}"
}

# env_set KEY VALUE writes KEY=VALUE in place of an existing KEY line, or
# appends it.
env_set() {
  local tmp
  tmp="$(mktemp "${env_file}.XXXXXX")"
  KEY="$1" VALUE="$2" awk '
    index($0, ENVIRON["KEY"] "=") == 1 { if (!done) print ENVIRON["KEY"] "=" ENVIRON["VALUE"]; done = 1; next }
    { print }
    END { if (!done) print ENVIRON["KEY"] "=" ENVIRON["VALUE"] }
  ' "$env_file" > "$tmp"
  mv "$tmp" "$env_file"
}

# env_default KEY VALUE sets KEY only when it is missing or empty (secrets:
# an empty one is never valid).
env_default() {
  [[ -n "$(env_value "$1")" ]] || env_set "$1" "$2"
}

# env_init KEY VALUE sets KEY only when .env has no KEY line at all, so a
# setting deliberately left empty (COMPOSE_PROFILES=) stays empty.
env_init() {
  grep -qE "^$1=" "$env_file" || env_set "$1" "$2"
}

# --- version ---------------------------------------------------------------
if [[ -z "$version" ]]; then
  version="$(env_value PROBARA_VERSION)"
fi
if [[ -z "$version" ]]; then
  command -v curl >/dev/null 2>&1 || die "curl is required to find the latest release (or pass --version)"
  version="$(curl -fsSL "https://api.github.com/repos/$REPO/releases?per_page=1" \
    | grep -m1 '"tag_name"' | sed -E 's/.*"tag_name": *"v?([^"]+)".*/\1/' || true)"
  [[ -n "$version" ]] || die "could not determine the latest release; pass --version"
fi
log "Probara $version in $(pwd)"

# --- compose files ---------------------------------------------------------
for file in docker-compose.yml docker-compose.release.yml; do
  if [[ -n "$source_dir" ]]; then
    cp "$source_dir/$file" "$file"
  else
    command -v curl >/dev/null 2>&1 || die "curl is required to download $file (or pass --source)"
    curl -fsSL "https://raw.githubusercontent.com/$REPO/v$version/$file" -o "$file.tmp" \
      || die "could not download $file for v$version"
    mv "$file.tmp" "$file"
  fi
  chmod 644 "$file"
done

# --- .env ------------------------------------------------------------------
env_set PROBARA_VERSION "$version"
env_init COMPOSE_FILE "docker-compose.yml:docker-compose.release.yml"
env_init COMPOSE_PROJECT_NAME "probara"

# Secrets: hex (or base64 for the 32-byte key) so they are safe inside the
# URLs Compose builds from them (postgres://, nats://).
env_default ADMIN_JWT_SECRET "$(openssl rand -hex 32)"
env_default PROBARA_SECRETS_KEY "$(openssl rand -base64 32)"
env_default POSTGRES_PASSWORD "$(openssl rand -hex 24)"
env_default NATS_PLATFORM_PASSWORD "$(openssl rand -hex 24)"
env_default LOCAL_AI_TOKEN "$(openssl rand -hex 32)"
env_default STATUS_PAGE_PREVIEW_SECRET "$(openssl rand -hex 32)"

seed="$(env_value NATS_LOCATION_AUTH_ISSUER_SEED)"
public_key="$(env_value NATS_LOCATION_AUTH_ISSUER_PUBLIC_KEY)"
if [[ -z "$seed" && -z "$public_key" ]]; then
  log "Generating the NATS location auth issuer key ($NATS_BOX_IMAGE)"
  pair="$(docker run --rm "$NATS_BOX_IMAGE" nk -gen account -pubout)" \
    || die "could not run $NATS_BOX_IMAGE to generate the issuer key"
  seed="$(printf '%s\n' "$pair" | sed -n 1p)"
  public_key="$(printf '%s\n' "$pair" | sed -n 2p)"
  [[ "$seed" == SA* && "$public_key" == A* ]] || die "unexpected nk output"
  env_set NATS_LOCATION_AUTH_ISSUER_SEED "$seed"
  env_set NATS_LOCATION_AUTH_ISSUER_PUBLIC_KEY "$public_key"
elif [[ -z "$seed" || -z "$public_key" ]]; then
  die "$env_file has only one of NATS_LOCATION_AUTH_ISSUER_SEED / _PUBLIC_KEY; set both (nk -gen account -pubout) or remove both"
fi

# Local AI ships for linux/amd64 only: other hosts run without it.
arch="$(docker info --format '{{.Architecture}}' 2>/dev/null || uname -m)"
case "$arch" in
  x86_64|amd64)
    env_init COMPOSE_PROFILES "local-ai"
    env_init LOCAL_AI_DISABLED "false" ;;
  *)
    log "Docker host is $arch: local AI (linux/amd64 only) stays off"
    env_init LOCAL_AI_DISABLED "true" ;;
esac

# Off until the UI is served over HTTPS (a Secure cookie never comes back over
# plain http). Set it to true behind TLS.
env_init ADMIN_COOKIE_SECURE "false"
for key in PUBLIC_BASE_URL PUBLIC_NATS_URL APP_BASE_URL SMTP_HOST SMTP_PORT SMTP_USE_TLS SMTP_USERNAME SMTP_PASSWORD SMTP_FROM; do
  env_init "$key" ""
done

docker compose config --quiet || die "the Compose configuration does not render; check $(pwd)/$env_file"

if [[ "$start" != true ]]; then
  log "Files written. Start with: cd $(pwd) && docker compose up -d"
  exit 0
fi

log "Pulling images"
docker compose pull
log "Starting"
docker compose up -d

cat <<EOF

Probara $version is starting.
  UI:           http://localhost:${PROBARA_UI_PORT:-3000}  (create the first administrator there)
  Status pages: http://localhost:${PROBARA_STATUS_PAGE_PORT:-8082}/public/status/<slug>

Back up $(pwd)/$env_file: it holds every generated secret.
Before exposing this host, put HTTPS in front of the UI, then set
PUBLIC_BASE_URL and ADMIN_COOKIE_SECURE=true in $env_file and run
\`docker compose up -d\` again.
EOF
