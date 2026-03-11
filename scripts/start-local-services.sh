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

start_service status-page ./status-page/cmd/status-page 8082 9093 \
  STATUS_PAGE_BASE_URL="${STATUS_PAGE_BASE_URL:-http://localhost:8082}" \
  STATUS_PAGE_API_BASE_URL="${STATUS_PAGE_API_BASE_URL:-http://localhost:8080}"

start_service alerter ./alerter/cmd/alerter 8084 9094 \
  ALERT_STREAM="${ALERT_STREAM:-ALERTS}" \
  ALERT_SUBJECT="${ALERT_SUBJECT:-alerts}" \
  ALERT_EVAL_INTERVAL_SECONDS="${ALERT_EVAL_INTERVAL_SECONDS:-30}" \
  ALERT_REMINDER_INTERVAL_SECONDS="${ALERT_REMINDER_INTERVAL_SECONDS:-3600}" \
  ALERT_GROUP_WINDOW_SECONDS="${ALERT_GROUP_WINDOW_SECONDS:-60}" \
  ALERT_GROUP_MAX_CHILDREN="${ALERT_GROUP_MAX_CHILDREN:-5}"

log "All local Go services started successfully."
