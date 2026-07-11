#!/usr/bin/env bash
# End-to-end verification of RBAC, scoped API keys, audit log and (optionally)
# OIDC SSO against a locally running stack.
#
# Prereqs: API on $API (default http://localhost:8080) with a bootstrapped
# superadmin ($ADMIN_USER/$ADMIN_PASS, default admin/change-me), jq, curl.
# For the SSO leg: docker compose --profile sso up dex + OIDC_ENABLED=true on
# the API, then run with VERIFY_SSO=1.
set -uo pipefail

API="${API:-http://localhost:8080}"
ADMIN_USER="${ADMIN_USER:-admin}"
ADMIN_PASS="${ADMIN_PASS:-change-me}"
VERIFY_SSO="${VERIFY_SSO:-0}"

PASS=0
FAIL=0
COOKIES_DIR="$(mktemp -d)"
trap 'rm -rf "$COOKIES_DIR"' EXIT

check() { # check <description> <expected> <actual>
  local desc="$1" expected="$2" actual="$3"
  if [[ "$expected" == "$actual" ]]; then
    echo "  ok   $desc"
    PASS=$((PASS + 1))
  else
    echo "  FAIL $desc (expected $expected, got $actual)"
    FAIL=$((FAIL + 1))
  fi
}

status_as() { # status_as <cookie-jar|-> <method> <path> [tenant] [body]
  local jar="$1" method="$2" path="$3" tenant="${4:-}" body="${5:-}"
  local args=(-s -o /dev/null -w '%{http_code}' -X "$method" "$API$path" -H 'Content-Type: application/json')
  [[ "$jar" != "-" ]] && args+=(-b "$jar")
  [[ -n "$tenant" ]] && args+=(-H "X-Tenant-ID: $tenant")
  [[ -n "$body" ]] && args+=(-d "$body")
  curl "${args[@]}"
}

status_key() { # status_key <api-key> <method> <path> [body]
  local key="$1" method="$2" path="$3" body="${4:-}"
  local args=(-s -o /dev/null -w '%{http_code}' -X "$method" "$API$path" -H "Authorization: Bearer $key" -H 'Content-Type: application/json')
  [[ -n "$body" ]] && args+=(-d "$body")
  curl "${args[@]}"
}

login() { # login <jar> <user> <pass> -> http code
  curl -s -o /dev/null -w '%{http_code}' -c "$1" -X POST "$API/api/v1/auth/login" \
    -H 'Content-Type: application/json' -d "{\"username\":\"$2\",\"password\":\"$3\"}"
}

echo "== Superadmin login =="
ADMIN_JAR="$COOKIES_DIR/admin"
check "superadmin login" 200 "$(login "$ADMIN_JAR" "$ADMIN_USER" "$ADMIN_PASS")"
# A deliberate failure for the audit log.
login "$COOKIES_DIR/junk" "$ADMIN_USER" "wrong-password-123" >/dev/null

TENANT=$(curl -s -b "$ADMIN_JAR" "$API/api/v1/tenants" | jq -r '.items[0].id')
echo "  using tenant $TENANT"

echo "== Create member users (editor / viewer) =="
SUFFIX="$RANDOM"
EDITOR_USER="e2e-editor-$SUFFIX"; VIEWER_USER="e2e-viewer-$SUFFIX"; USER_PASS="e2e-password-123456"
mk_user() { # mk_user <username> <role>
  curl -s -b "$ADMIN_JAR" -X POST "$API/api/v1/users" -H 'Content-Type: application/json' -d "{
    \"username\": \"$1\", \"password\": \"$USER_PASS\", \"platform_role\": \"member\",
    \"memberships\": [{\"tenant_id\": \"$TENANT\", \"role\": \"$2\"}]
  }" | jq -r '.id // empty'
}
EDITOR_ID=$(mk_user "$EDITOR_USER" editor)
VIEWER_ID=$(mk_user "$VIEWER_USER" viewer)
check "editor created" "nonempty" "$([[ -n "$EDITOR_ID" ]] && echo nonempty || echo empty)"
check "viewer created" "nonempty" "$([[ -n "$VIEWER_ID" ]] && echo nonempty || echo empty)"

echo "== Viewer permissions =="
VIEWER_JAR="$COOKIES_DIR/viewer"
check "viewer login" 200 "$(login "$VIEWER_JAR" "$VIEWER_USER" "$USER_PASS")"
check "viewer GET monitors" 200 "$(status_as "$VIEWER_JAR" GET /api/v1/monitors "$TENANT")"
check "viewer POST monitors -> 403" 403 "$(status_as "$VIEWER_JAR" POST /api/v1/monitors "$TENANT" '{"name":"x","type":"http"}')"
check "viewer POST monitors/test (allowlisted)" 400 "$(status_as "$VIEWER_JAR" POST /api/v1/monitors/test "$TENANT" '{}')" # 400 = passed RequireWrite, failed validation
check "viewer GET users -> 403" 403 "$(status_as "$VIEWER_JAR" GET /api/v1/users "$TENANT")"
check "viewer POST api-keys -> 403" 403 "$(status_as "$VIEWER_JAR" POST /api/v1/api-keys "$TENANT" '{"name":"nope"}')"
check "viewer GET audit-log -> 403" 403 "$(status_as "$VIEWER_JAR" GET /api/v1/audit-log "$TENANT")"
VIEWER_TENANTS=$(curl -s -b "$VIEWER_JAR" "$API/api/v1/tenants" | jq '.items | length')
check "viewer sees exactly 1 tenant" 1 "$VIEWER_TENANTS"
check "viewer denied foreign tenant" 403 "$(status_as "$VIEWER_JAR" GET /api/v1/monitors 00000000-0000-0000-0000-0000000000ff)"

echo "== Editor permissions =="
EDITOR_JAR="$COOKIES_DIR/editor"
check "editor login" 200 "$(login "$EDITOR_JAR" "$EDITOR_USER" "$USER_PASS")"
MONITOR_BODY='{"name":"e2e-auth-check","type":"http","interval_seconds":300,"timeout_seconds":10,"config":{"url":"https://example.com","method":"GET"}}'
MONITOR_CODE=$(status_as "$EDITOR_JAR" POST /api/v1/monitors "$TENANT" "$MONITOR_BODY")
check "editor POST monitors -> 201" 201 "$MONITOR_CODE"
check "editor POST api-keys -> 403 (not tenant admin)" 403 "$(status_as "$EDITOR_JAR" POST /api/v1/api-keys "$TENANT" '{"name":"nope"}')"
check "editor GET audit-log -> 403" 403 "$(status_as "$EDITOR_JAR" GET /api/v1/audit-log "$TENANT")"

echo "== Scoped API keys =="
mk_key() { # mk_key <name> <scope> [expires_at]
  local extra=""
  [[ -n "${3:-}" ]] && extra=", \"expires_at\": \"$3\""
  curl -s -b "$ADMIN_JAR" -H "X-Tenant-ID: $TENANT" -X POST "$API/api/v1/api-keys" \
    -H 'Content-Type: application/json' -d "{\"name\":\"$1\",\"scope\":\"$2\"$extra}" | jq -r '.key // empty'
}
READ_KEY=$(mk_key "e2e-read-$SUFFIX" read)
WRITE_KEY=$(mk_key "e2e-write-$SUFFIX" write)
if date -v+1d >/dev/null 2>&1; then EXP=$(date -u -v+2S '+%Y-%m-%dT%H:%M:%SZ'); else EXP=$(date -u -d '+2 seconds' '+%Y-%m-%dT%H:%M:%SZ'); fi
EXPIRING_KEY=$(mk_key "e2e-expiring-$SUFFIX" write "$EXP")
check "read key minted" "nonempty" "$([[ -n "$READ_KEY" ]] && echo nonempty || echo empty)"
check "write key minted" "nonempty" "$([[ -n "$WRITE_KEY" ]] && echo nonempty || echo empty)"

check "read key GET monitors" 200 "$(status_key "$READ_KEY" GET /api/v1/monitors)"
check "read key POST monitors -> 403" 403 "$(status_key "$READ_KEY" POST /api/v1/monitors "$MONITOR_BODY")"
check "write key POST monitors -> 201" 201 "$(status_key "$WRITE_KEY" POST /api/v1/monitors "${MONITOR_BODY/e2e-auth-check/e2e-auth-check-2}")"
sleep 3
check "expired key -> 401" 401 "$(status_key "$EXPIRING_KEY" GET /api/v1/monitors)"

echo "== Audit log =="
sleep 3 # allow the async recorder to flush (2s interval)
AUDIT=$(curl -s -b "$ADMIN_JAR" -H "X-Tenant-ID: $TENANT" "$API/api/v1/audit-log?page_size=200")
has_action() { echo "$AUDIT" | jq -e --arg a "$1" '[.items[] | select(.action == $a)] | length > 0' >/dev/null && echo yes || echo no; }
check "audit has auth.login" yes "$(has_action auth.login)"
check "audit has monitor.create" yes "$(has_action monitor.create)"
check "audit has apikey.create" yes "$(has_action apikey.create)"
check "audit has user.create" yes "$(has_action user.create)"
FAILED_LOGIN=$(echo "$AUDIT" | jq '[.items[] | select(.action == "auth.login" and .outcome == "failure")] | length > 0')
check "audit recorded failed login" true "$FAILED_LOGIN"
DENIED=$(echo "$AUDIT" | jq '[.items[] | select(.outcome == "denied")] | length > 0')
check "audit recorded denied mutations" true "$DENIED"

if [[ "$VERIFY_SSO" == "1" ]]; then
  echo "== OIDC SSO (dex) =="
  SSO_ENABLED=$(curl -s "$API/api/v1/auth/oidc/status" | jq -r '.enabled')
  check "oidc status enabled" true "$SSO_ENABLED"
  SSO_JAR="$COOKIES_DIR/sso"
  # Follow the full code flow: start -> dex login form -> approval -> callback.
  DEX_LOGIN=$(curl -s -L -c "$SSO_JAR" -b "$SSO_JAR" -o /dev/null -w '%{url_effective}' "$API/api/v1/auth/oidc/start")
  STATE_URL=$(echo "$DEX_LOGIN" | sed 's/&amp;/\&/g')
  FINAL=$(curl -s -L -c "$SSO_JAR" -b "$SSO_JAR" -o /dev/null -w '%{http_code} %{url_effective}' \
    -d 'login=admin%40example.com&password=password' "$STATE_URL")
  echo "  dex flow final: $FINAL"
  ME=$(curl -s -b "$SSO_JAR" "$API/api/v1/auth/me" | jq -r '.user.auth_method // empty')
  check "SSO session established (auth_method=oidc)" oidc "$ME"
fi

echo
echo "Results: $PASS passed, $FAIL failed"
exit $((FAIL > 0 ? 1 : 0))
