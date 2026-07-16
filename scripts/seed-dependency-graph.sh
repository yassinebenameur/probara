#!/bin/bash

# Seeds a realistic dependency topology into the local dev tenant so the
# /dependencies page can be exercised at scale:
#   - one hub with 40 dependents (the high fan-in case)
#   - a 5-deep gateway chain, its head also depending on the hub
#   - a 12-node fan-in cluster and a 10-node fan-out
#   - a handful of cross links
# Idempotent: re-running re-posts the same edges (server upserts, cycles 409).
#
# Usage: API_URL=http://localhost:8080 ADMIN_USER=admin ADMIN_PASS=change-me \
#        ./scripts/seed-dependency-graph.sh

set -euo pipefail

API_URL="${API_URL:-http://localhost:8080}"
ADMIN_USER="${ADMIN_USER:-admin}"
ADMIN_PASS="${ADMIN_PASS:-change-me}"

command -v jq >/dev/null || { echo "jq is required" >&2; exit 1; }

COOKIES=$(mktemp)
trap 'rm -f "$COOKIES"' EXIT

echo "Logging in as ${ADMIN_USER}…"
LOGIN_STATUS=$(curl -s -o /dev/null -w '%{http_code}' -c "$COOKIES" \
  -H 'Content-Type: application/json' \
  -d "{\"username\":\"${ADMIN_USER}\",\"password\":\"${ADMIN_PASS}\"}" \
  "${API_URL}/api/v1/auth/login")
[ "$LOGIN_STATUS" = "200" ] || { echo "Login failed (HTTP ${LOGIN_STATUS})" >&2; exit 1; }

TENANT_ID="${TENANT_ID:-$(curl -s -b "$COOKIES" "${API_URL}/api/v1/tenants" | jq -r '(.items // .)[0].id')}"
[ -n "$TENANT_ID" ] && [ "$TENANT_ID" != "null" ] || { echo "No tenant found" >&2; exit 1; }
echo "Tenant: ${TENANT_ID}"

api() { # method path [json-body] -> prints http status, body to /tmp/seed-body
  local method=$1 path=$2 body=${3:-}
  if [ -n "$body" ]; then
    curl -s -o /tmp/seed-dep-body -w '%{http_code}' -b "$COOKIES" \
      -H 'Content-Type: application/json' -H "X-Tenant-ID: ${TENANT_ID}" \
      -X "$method" -d "$body" "${API_URL}${path}"
  else
    curl -s -o /tmp/seed-dep-body -w '%{http_code}' -b "$COOKIES" \
      -H "X-Tenant-ID: ${TENANT_ID}" -X "$method" "${API_URL}${path}"
  fi
}

echo "Fetching monitors…"
MONITORS_FILE=$(mktemp)
trap 'rm -f "$COOKIES" "$MONITORS_FILE"' EXIT
: > "$MONITORS_FILE"
for page in 1 2 3 4 5; do
  STATUS=$(api GET "/api/v1/monitors?page=${page}&page_size=100")
  [ "$STATUS" = "200" ] || { echo "Monitor list failed (HTTP ${STATUS})" >&2; exit 1; }
  COUNT=$(jq '.items | length' /tmp/seed-dep-body)
  jq -r '.items[] | select(.type != "group") | .id' /tmp/seed-dep-body >> "$MONITORS_FILE"
  [ "$COUNT" -lt 100 ] && break
done
M=()
while IFS= read -r line; do M+=("$line"); done < "$MONITORS_FILE"
echo "Found ${#M[@]} non-group monitors"
[ "${#M[@]}" -ge 80 ] || { echo "Need at least 80 monitors to seed the topology" >&2; exit 1; }

ADDED=0 SKIPPED=0
add_dep() { # add_dep <monitor-idx> <depends-on-idx>: M[i] depends on M[j]
  local from="${M[$1]}" to="${M[$2]}"
  local status
  status=$(api POST "/api/v1/monitors/${from}/dependencies" "{\"depends_on_id\":\"${to}\"}")
  case "$status" in
    200|201|204) ADDED=$((ADDED + 1)) ;;
    409) SKIPPED=$((SKIPPED + 1)) ;;
    *) echo "  edge $1 -> $2 failed (HTTP ${status}): $(cat /tmp/seed-dep-body)" >&2; SKIPPED=$((SKIPPED + 1)) ;;
  esac
}

echo "Hub: monitors 1..40 depend on monitor 0…"
for i in $(seq 1 40); do add_dep "$i" 0; done

echo "Gateway chain: 41 -> 42 -> 43 -> 44 -> 45, and 41 -> hub…"
add_dep 41 42; add_dep 42 43; add_dep 43 44; add_dep 44 45; add_dep 41 0

echo "Fan-in cluster: 46..57 depend on 58…"
for i in $(seq 46 57); do add_dep "$i" 58; done

echo "Fan-out: 59 depends on 60..69…"
for i in $(seq 60 69); do add_dep 59 "$i"; done

echo "Cross links…"
add_dep 46 59; add_dep 47 60; add_dep 48 61; add_dep 70 58; add_dep 71 58
add_dep 72 0;  add_dep 73 41; add_dep 74 42; add_dep 75 76; add_dep 77 78

echo "Cycle check: hub depending on one of its dependents must 409…"
STATUS=$(api POST "/api/v1/monitors/${M[0]}/dependencies" "{\"depends_on_id\":\"${M[1]}\"}")
if [ "$STATUS" = "409" ]; then
  echo "  OK: got 409 dependency_cycle"
else
  echo "  UNEXPECTED: got HTTP ${STATUS}: $(cat /tmp/seed-dep-body)" >&2
fi

echo "Done: ${ADDED} edges added, ${SKIPPED} already present/skipped."
