#!/usr/bin/env bash
# Checks scripts/install.sh's .env handling without starting anything:
#   - a fresh install generates every secret the release Compose file
#     requires, and the merged configuration renders;
#   - hand-edited forms Compose accepts (`export KEY=`, `KEY = v`, quotes,
#     inline comments) are recognized, so a re-run never appends a second
#     secret;
#   - a second run changes nothing, and --version changes only
#     PROBARA_VERSION.
# Needs the docker CLI (for `docker compose config`), not a running stack. The
# issuer pair is pre-seeded so no run pulls the nats-box image.
#
#   bash scripts/test-install.sh

set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

fail() { echo "FAIL: $*" >&2; exit 1; }

# A clean environment: an exported variable would win over .env in Compose.
clean() { env -i PATH="$PATH" HOME="$HOME" ${DOCKER_HOST:+DOCKER_HOST="$DOCKER_HOST"} "$@"; }
run() { (cd "$1" && clean bash "$ROOT/scripts/install.sh" --dir "$1" --source "$ROOT" --version "$2" --no-start >/dev/null); }
defined() { grep -cE "^[[:space:]]*(export[[:space:]]+)?$2[[:space:]]*=" "$1/.env" || true; }

required_secrets=(ADMIN_JWT_SECRET PROBARA_SECRETS_KEY POSTGRES_PASSWORD NATS_PLATFORM_PASSWORD
  LOCAL_AI_TOKEN STATUS_PAGE_PREVIEW_SECRET NATS_LOCATION_AUTH_ISSUER_SEED NATS_LOCATION_AUTH_ISSUER_PUBLIC_KEY)

# --- fresh install ----------------------------------------------------------
fresh="$work/fresh"
mkdir -p "$fresh"
printf 'NATS_LOCATION_AUTH_ISSUER_SEED=SAFAKESEED\nNATS_LOCATION_AUTH_ISSUER_PUBLIC_KEY=AFAKEPUBLIC\n' > "$fresh/.env"
run "$fresh" 2.1.0
for key in "${required_secrets[@]}" PROBARA_VERSION COMPOSE_FILE COMPOSE_PROFILES; do
  [[ "$(defined "$fresh" "$key")" == 1 ]] || fail "fresh: $key is defined $(defined "$fresh" "$key") times"
  grep -qE "^$key=.+" "$fresh/.env" || fail "fresh: $key is empty"
done
grep -qE '^ADMIN_JWT_SECRET=[0-9a-f]{64}$' "$fresh/.env" || fail "fresh: ADMIN_JWT_SECRET not generated as 32 hex bytes"
key_b64="$(sed -n 's/^PROBARA_SECRETS_KEY=//p' "$fresh/.env")"
[[ "$(printf '%s' "$key_b64" | openssl base64 -d -A | wc -c | tr -d ' ')" == 32 ]] || fail "fresh: PROBARA_SECRETS_KEY is not 32 base64 bytes"
[[ "$(stat -f %Lp "$fresh/.env" 2>/dev/null || stat -c %a "$fresh/.env")" == 600 ]] || fail "fresh: .env is not mode 600"

resolved="$(cd "$fresh" && clean docker compose config --format json)"
for want in '"image": "ghcr.io/yassinebenameur/probara-api:2.1.0"' '"image": "ghcr.io/yassinebenameur/probara-frontend:2.1.0"' \
  '"image": "ghcr.io/yassinebenameur/probara-local-ai:2.1.0"' '"TRUSTED_PROXY_CIDRS": "172.31.213.0/24"'; do
  grep -qF -- "$want" <<<"$resolved" || fail "fresh: compose did not resolve $want"
done
grep -qF '"build"' <<<"$resolved" && fail "fresh: a service still builds from source"
grep -qF 'your-secret-key-change-in-production' <<<"$resolved" && fail "fresh: the development JWT secret leaked into the release stack"
grep -qF 'probara-dex-secret' <<<"$resolved" && fail "fresh: the development OIDC secret leaked into the release stack"
grep -qF 'postgres://probara:probara@' <<<"$resolved" && fail "fresh: the development database password leaked into the release stack"

cp "$fresh/.env" "$fresh/.env.first"
run "$fresh" 2.1.0
diff "$fresh/.env.first" "$fresh/.env" >/dev/null || fail "fresh: a second run changed .env"

# An upgrade rewrites PROBARA_VERSION and nothing else.
run "$fresh" 2.2.0
diff <(grep -v '^PROBARA_VERSION=' "$fresh/.env.first") <(grep -v '^PROBARA_VERSION=' "$fresh/.env") >/dev/null \
  || fail "upgrade: --version changed more than PROBARA_VERSION"
grep -qx 'PROBARA_VERSION=2.2.0' "$fresh/.env" || fail "upgrade: PROBARA_VERSION not updated"

# --- hand-edited .env -------------------------------------------------------
edited="$work/edited"
mkdir -p "$edited"
cat > "$edited/.env" <<'EOF'
export ADMIN_JWT_SECRET=hand-edited-jwt
POSTGRES_PASSWORD = spaced-password
PROBARA_SECRETS_KEY="quoted+key/=="
NATS_PLATFORM_PASSWORD='single-quoted'
LOCAL_AI_TOKEN=token # trailing comment
STATUS_PAGE_PREVIEW_SECRET=
  export NATS_LOCATION_AUTH_ISSUER_SEED = SAFAKESEED
NATS_LOCATION_AUTH_ISSUER_PUBLIC_KEY=AFAKEPUBLIC
export COMPOSE_PROFILES=
EOF

run "$edited" 2.1.0
for key in "${required_secrets[@]}" COMPOSE_PROFILES PROBARA_VERSION; do
  [[ "$(defined "$edited" "$key")" == 1 ]] || fail "edited: $key is defined $(defined "$edited" "$key") times"
done
grep -qx 'export ADMIN_JWT_SECRET=hand-edited-jwt' "$edited/.env" || fail "edited: export form was rewritten"
grep -qx 'POSTGRES_PASSWORD = spaced-password' "$edited/.env" || fail "edited: spaced form was rewritten"
grep -qx 'LOCAL_AI_TOKEN=token # trailing comment' "$edited/.env" || fail "edited: commented value was rewritten"
grep -qx 'export COMPOSE_PROFILES=' "$edited/.env" || fail "edited: a deliberately empty setting was filled"
grep -qE '^STATUS_PAGE_PREVIEW_SECRET=[0-9a-f]{64}$' "$edited/.env" || fail "edited: an empty secret was not generated"
grep -qx 'PROBARA_VERSION=2.1.0' "$edited/.env" || fail "edited: PROBARA_VERSION not written"

# Compose reads the hand-edited forms to the same values.
resolved="$(cd "$edited" && clean docker compose config --format json)"
for want in hand-edited-jwt 'postgres://probara:spaced-password@' 'nats://probara-platform:single-quoted@' \
  '"PROBARA_SECRETS_KEY": "quoted+key/=="' '"LOCAL_AI_TOKEN": "token"'; do
  grep -qF -- "$want" <<<"$resolved" || fail "edited: compose did not resolve $want"
done

cp "$edited/.env" "$edited/.env.first"
run "$edited" 2.1.0
diff "$edited/.env.first" "$edited/.env" >/dev/null || fail "edited: a second run changed .env"

echo "install.sh .env handling: ok"
