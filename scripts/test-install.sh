#!/usr/bin/env bash
# Checks scripts/install.sh's .env handling without starting anything:
# hand-edited forms Compose accepts (`export KEY=`, `KEY = v`, quotes, inline
# comments) are recognized, so a re-run never appends a second secret, and a
# second run changes nothing. Needs the docker CLI (for `docker compose
# config`), not a running stack.
#
#   bash scripts/test-install.sh

set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

fail() { echo "FAIL: $*" >&2; exit 1; }

# The issuer pair is pre-seeded so the run never pulls the nats-box image.
cat > "$work/.env" <<'EOF'
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

# A clean environment: an exported variable would win over .env in Compose.
clean() { env -i PATH="$PATH" HOME="$HOME" ${DOCKER_HOST:+DOCKER_HOST="$DOCKER_HOST"} "$@"; }
run() { (cd "$work" && clean bash "$ROOT/scripts/install.sh" --dir "$work" --source "$ROOT" --version 2.0.0 --no-start >/dev/null); }

run
for key in ADMIN_JWT_SECRET POSTGRES_PASSWORD PROBARA_SECRETS_KEY NATS_PLATFORM_PASSWORD LOCAL_AI_TOKEN \
  STATUS_PAGE_PREVIEW_SECRET NATS_LOCATION_AUTH_ISSUER_SEED NATS_LOCATION_AUTH_ISSUER_PUBLIC_KEY COMPOSE_PROFILES PROBARA_VERSION; do
  count="$(grep -cE "^[[:space:]]*(export[[:space:]]+)?$key[[:space:]]*=" "$work/.env" || true)"
  [[ "$count" == 1 ]] || fail "$key is defined $count times"
done
grep -qx 'export ADMIN_JWT_SECRET=hand-edited-jwt' "$work/.env" || fail "export form was rewritten"
grep -qx 'POSTGRES_PASSWORD = spaced-password' "$work/.env" || fail "spaced form was rewritten"
grep -qx 'LOCAL_AI_TOKEN=token # trailing comment' "$work/.env" || fail "commented value was rewritten"
grep -qx 'export COMPOSE_PROFILES=' "$work/.env" || fail "a deliberately empty setting was filled"
grep -qE '^STATUS_PAGE_PREVIEW_SECRET=[0-9a-f]{64}$' "$work/.env" || fail "an empty secret was not generated"
grep -qx 'PROBARA_VERSION=2.0.0' "$work/.env" || fail "PROBARA_VERSION not written"

# Compose reads the hand-edited forms to the same values.
resolved="$(cd "$work" && clean docker compose config --format json)"
for want in hand-edited-jwt 'postgres://probara:spaced-password@' 'nats://probara-platform:single-quoted@' '"LOCAL_AI_TOKEN": "token"'; do
  grep -qF -- "$want" <<<"$resolved" || fail "compose did not resolve $want"
done

cp "$work/.env" "$work/.env.first"
run
diff "$work/.env.first" "$work/.env" >/dev/null || fail "a second run changed .env"

echo "install.sh .env handling: ok"
