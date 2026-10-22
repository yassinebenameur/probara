#!/usr/bin/env bash

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "$0")/.." && pwd)"
STARTUP_LOG="/tmp/probara-local-start.log"
ARTIFACTS_DIR="${SYNTHETIC_BROWSER_ARTIFACTS_DIR:-/tmp/probara/synthetic-browser-artifacts}"
BIN_DIR="/tmp/probara-bin"
POSTGRES_URL="${POSTGRES_URL:-postgres://probara:probara@localhost:5432/probara?sslmode=disable}"
NATS_URL="${NATS_URL:-nats://localhost:4222}"
REQUIRED_GO_VERSION="$(awk '/^go / { print $2; exit }' "$ROOT_DIR/go.mod")"
REQUIRED_GO_MINOR="${REQUIRED_GO_VERSION%.*}"

# Dev encryption key for monitor/channel config secrets. Persisted next to the
# repo (gitignored) so envelopes written by one run stay readable after a
# restart or reboot — a lost key makes stored secrets undecryptable.
SECRETS_KEY_FILE="$ROOT_DIR/.dev-secrets.key"
if [[ -z "${PROBARA_SECRETS_KEY:-}" ]]; then
  if [[ ! -f "$SECRETS_KEY_FILE" ]]; then
    openssl rand -base64 32 > "$SECRETS_KEY_FILE"
  fi
  PROBARA_SECRETS_KEY="$(cat "$SECRETS_KEY_FILE")"
fi
export PROBARA_SECRETS_KEY

mkdir -p "$ARTIFACTS_DIR"
mkdir -p "$BIN_DIR"
: > "$STARTUP_LOG"

log() {
  local message="$1"
  echo "$message" | tee -a "$STARTUP_LOG"
}

fail() {
  local message="$1"
  log "ERROR: $message"
  bash "$ROOT_DIR/scripts/stop-local-services.sh" >/dev/null 2>&1 || true
  exit 1
}

ensure_port_available() {
  local name="$1"
  local port="$2"

  local listener_pid
  listener_pid="$(lsof -nP -iTCP:"$port" -sTCP:LISTEN -t 2>/dev/null | sed -n '1p' || true)"
  if [[ -z "$listener_pid" ]]; then
    return 0
  fi

  local listener_cmd
  listener_cmd="$(ps -p "$listener_pid" -o command= 2>/dev/null || true)"
  if [[ "$listener_cmd" == *"/tmp/probara-bin/$name"* ]]; then
    log "Port $port already in use by stale ${name} process ($listener_pid); stopping it"
    kill "$listener_pid" 2>/dev/null || true
    sleep 1
    kill -9 "$listener_pid" 2>/dev/null || true
    return 0
  fi

  fail "Port $port is already in use by pid $listener_pid ($listener_cmd). Stop that process and retry."
}

ensure_go_toolchain() {
  if ! command -v go >/dev/null 2>&1; then
    fail "Go is not installed. Install Go $REQUIRED_GO_MINOR.x. See $STARTUP_LOG"
  fi

  local installed
  installed="$(go version | awk '{print $3}')"
  if [[ "$installed" != go${REQUIRED_GO_MINOR}* ]]; then
    fail "Unsupported Go toolchain: found $installed, expected go${REQUIRED_GO_MINOR}.x"
  fi

  log "Using $installed"
}

start_service() {
  local name="$1"
  local package_path="$2"
  local http_port="$3"
  local metrics_port="$4"
  shift 4

  local log_file="/tmp/probara-${name}.log"
  local pid_file="/tmp/probara-${name}.pid"
  local bin_file="$BIN_DIR/${name}"

  ensure_port_available "$name" "$http_port"
  ensure_port_available "$name" "$metrics_port"

  if [[ -f "$pid_file" ]]; then
    local existing_pid
    existing_pid="$(cat "$pid_file")"
    if kill -0 "$existing_pid" 2>/dev/null; then
      log "Stopping existing ${name} process ($existing_pid)"
      kill "$existing_pid" 2>/dev/null || true
      sleep 1
      kill -9 "$existing_pid" 2>/dev/null || true
    fi
    rm -f "$pid_file"
  fi

  : > "$log_file"
  log "Starting ${name} locally. Log: $log_file"

  (
    cd "$ROOT_DIR"
    env \
      HTTP_PORT="$http_port" \
      METRICS_PORT="$metrics_port" \
      LOG_LEVEL="${LOG_LEVEL:-info}" \
      POSTGRES_URL="$POSTGRES_URL" \
      NATS_URL="$NATS_URL" \
      SYNTHETIC_BROWSER_ARTIFACTS_DIR="$ARTIFACTS_DIR" \
      "$@" \
      go build -o "$bin_file" "$package_path" >>"$log_file" 2>&1
  )

  cd "$ROOT_DIR"
  nohup env \
    HTTP_PORT="$http_port" \
    METRICS_PORT="$metrics_port" \
    LOG_LEVEL="${LOG_LEVEL:-info}" \
    POSTGRES_URL="$POSTGRES_URL" \
    NATS_URL="$NATS_URL" \
    SYNTHETIC_BROWSER_ARTIFACTS_DIR="$ARTIFACTS_DIR" \
    "$@" \
    "$bin_file" >>"$log_file" 2>&1 </dev/null &
  local pid=$!
  disown "$pid" 2>/dev/null || true
  echo "$pid" > "$pid_file"

  sleep 2

  pid="$(cat "$pid_file")"
  if ! kill -0 "$pid" 2>/dev/null; then
    log "Startup failed for ${name}. Last log lines:"
    tail -n 20 "$log_file" | tee -a "$STARTUP_LOG"
    fail "${name} exited during startup"
  fi
}

ensure_go_toolchain

if [[ "${1:-}" == "check-go" ]]; then
  exit 0
fi

# Local AI is default-on: inference runs in its CPU-only container (Compose
# service local-ai, loopback-bound on :8090) even when the Go services run as
# local processes. The bearer token is per checkout, kept in the gitignored
# .dev-local-ai-token next to .dev-secrets.key, and exported so both the
# container (Compose interpolation) and the api/worker processes (inherited
# env) agree on it. LOCAL_AI_DISABLED=true skips the container and turns the
# feature off in api/worker. The token and sizing are exported here, before
# the api and worker start; the container itself starts after them, in the
# background (start_local_ai below): a first run with no pullable image builds
# it from source for many minutes, and the clients already treat a model that
# is not up yet as unavailable. A failed start is logged and never blocks the
# platform.
if [[ "${LOCAL_AI_DISABLED:-false}" != "true" ]]; then
  AI_TOKEN_FILE="$ROOT_DIR/.dev-local-ai-token"
  if [[ -z "${LOCAL_AI_TOKEN:-}" ]]; then
    if [[ ! -s "$AI_TOKEN_FILE" ]]; then
      (umask 077; openssl rand -hex 32 > "$AI_TOKEN_FILE")
    fi
    LOCAL_AI_TOKEN="$(cat "$AI_TOKEN_FILE")"
  fi
  export LOCAL_AI_TOKEN
  export LOCAL_AI_URL="${LOCAL_AI_URL:-http://127.0.0.1:8090}"
  # Docker refuses a --cpus above the host's CPU count; unless set explicitly,
  # size the container to min(host CPUs, 4) and threads to min(CPUs, 4), the
  # same defaults the Makefile's local_ai macro applies.
  ai_cpus="${LOCAL_AI_CPUS:-$(docker info --format '{{.NCPU}}' 2>/dev/null || true)}"
  [[ "$ai_cpus" =~ ^[0-9]+$ && "$ai_cpus" -ge 1 ]] || ai_cpus=4
  [[ -n "${LOCAL_AI_CPUS:-}" || "$ai_cpus" -le 4 ]] || ai_cpus=4
  ai_threads="${LOCAL_AI_THREADS:-$ai_cpus}"
  [[ -n "${LOCAL_AI_THREADS:-}" || "$ai_threads" -le 4 ]] || ai_threads=4
  export LOCAL_AI_CPUS="$ai_cpus" LOCAL_AI_THREADS="$ai_threads"
fi

# Pull or build and start the local-ai container. Without a local or pullable
# image Compose builds from ai-service/, which needs the Git LFS head weights:
# a pointer file would fail only after torch and the ~650 MB base downloaded,
# so that case is refused up front (pull only, no build).
start_local_ai() {
  local model head image
  model="$(awk -F= '/^ARG MODEL_VERSION=/ { print $2; exit }' "$ROOT_DIR/ai-service/Dockerfile")"
  head="$ROOT_DIR/ai-service/artifacts/${model:-probara-laya-v4}/head.safetensors"
  image="$(cd "$ROOT_DIR" && docker compose config --images local-ai 2>/dev/null || true)"
  if ! docker image inspect "$image" >/dev/null 2>&1 && head -c 40 "$head" 2>/dev/null | grep -q '^version https://git-lfs'; then
    echo "local-ai: $head is a Git LFS pointer, so $image is pulled but never built. To build it: git lfs install && git lfs pull --include 'ai-service/artifacts/*/head.safetensors'"
    (cd "$ROOT_DIR" && docker compose up -d --no-build local-ai)
    return
  fi
  # An existing image is reused as is, even a stale local build: after an
  # ai-service/ or MODEL_VERSION change run `docker compose build local-ai`.
  (cd "$ROOT_DIR" && docker compose up -d local-ai)
}

start_service api ./api/cmd/api 8080 9090 \
  ADMIN_JWT_SECRET="${ADMIN_JWT_SECRET:-your-secret-key-change-in-production}" \
  ADMIN_ACCESS_TTL_MINUTES="${ADMIN_ACCESS_TTL_MINUTES:-15}" \
  ADMIN_REFRESH_TTL_DAYS="${ADMIN_REFRESH_TTL_DAYS:-30}" \
  ADMIN_COOKIE_SECURE="${ADMIN_COOKIE_SECURE:-false}"

start_service scheduler ./scheduler/cmd/scheduler 8081 9091 \
  CHECK_JOB_STREAM="${CHECK_JOB_STREAM:-CHECK_JOBS}" \
  CHECK_JOB_SUBJECT="${CHECK_JOB_SUBJECT:-check.jobs}" \
  SCHEDULE_INTERVAL_SECONDS="${SCHEDULE_INTERVAL_SECONDS:-5}" \
  RETENTION_CLEANUP_ENABLED="${RETENTION_CLEANUP_ENABLED:-true}" \
  RETENTION_CLEANUP_HOUR_UTC="${RETENTION_CLEANUP_HOUR_UTC:-2}" \
  RETENTION_CLEANUP_BATCH_SIZE="${RETENTION_CLEANUP_BATCH_SIZE:-5000}" \
  RETENTION_CLEANUP_MAX_ROWS_PER_RUN="${RETENTION_CLEANUP_MAX_ROWS_PER_RUN:-200000}"

start_service worker ./worker/cmd/worker 8083 9092 \
  CHECK_JOB_STREAM="${CHECK_JOB_STREAM:-CHECK_JOBS}" \
  CHECK_JOB_SUBJECT="${CHECK_JOB_SUBJECT:-check.jobs}" \
  NATS_CONSUMER_NAME="${NATS_CONSUMER_NAME:-worker}" \
  WORKER_CONCURRENCY="${WORKER_CONCURRENCY:-10}" \
  SIP_LOCALHOST_AS_HOST_GATEWAY="${SIP_LOCALHOST_AS_HOST_GATEWAY:-true}"

# Visitor browser notifications need a VAPID keypair; without one the feature
# is simply off. Mint a pair with `go run ./cmd/admin/gen_vapid_keys` and
# export the three vars before running this script.
#
# http://localhost is a secure context, so Web Push works locally even without
# TLS -- a plain-HTTP host that is NOT localhost will not.
start_service status-page ./status-page/cmd/status-page 8082 9093 \
  STATUS_PAGE_BASE_URL="${STATUS_PAGE_BASE_URL:-http://localhost:8082}" \
  STATUS_PAGE_API_BASE_URL="${STATUS_PAGE_API_BASE_URL:-http://localhost:8080}" \
  STATUS_PAGE_VAPID_PUBLIC_KEY="${STATUS_PAGE_VAPID_PUBLIC_KEY:-}" \
  STATUS_PAGE_VAPID_PRIVATE_KEY="${STATUS_PAGE_VAPID_PRIVATE_KEY:-}" \
  STATUS_PAGE_VAPID_SUBJECT="${STATUS_PAGE_VAPID_SUBJECT:-mailto:dev@localhost}"

start_service alerter ./alerter/cmd/alerter 8084 9094 \
  ALERT_STREAM="${ALERT_STREAM:-ALERTS}" \
  ALERT_SUBJECT="${ALERT_SUBJECT:-alerts}" \
  ALERT_EVAL_INTERVAL_SECONDS="${ALERT_EVAL_INTERVAL_SECONDS:-30}" \
  ALERT_REMINDER_INTERVAL_SECONDS="${ALERT_REMINDER_INTERVAL_SECONDS:-3600}" \
  ALERT_GROUP_WINDOW_SECONDS="${ALERT_GROUP_WINDOW_SECONDS:-60}" \
  ALERT_GROUP_MAX_CHILDREN="${ALERT_GROUP_MAX_CHILDREN:-5}"

log "All local Go services started successfully."

if [[ "${LOCAL_AI_DISABLED:-false}" != "true" ]]; then
  log "Starting local AI container in the background (docker compose service local-ai; progress in $STARTUP_LOG)"
  (
    if start_local_ai >>"$STARTUP_LOG" 2>&1; then
      echo "local-ai: container started" >>"$STARTUP_LOG"
    else
      echo "WARNING: local AI container failed to start; AI analysis will report the model unavailable. Set LOCAL_AI_DISABLED=true to skip it." >>"$STARTUP_LOG"
    fi
  ) </dev/null &
  disown 2>/dev/null || true
fi
