#!/usr/bin/env bash

set -euo pipefail

services=(api scheduler worker status-page alerter)

for service in "${services[@]}"; do
  pid_file="/tmp/probara-${service}.pid"
  bin_path="/tmp/probara-bin/${service}"
  if [[ -f "$pid_file" ]]; then
    pid="$(cat "$pid_file")"
    kill "$pid" 2>/dev/null || true
    sleep 1
    kill -9 "$pid" 2>/dev/null || true
    rm -f "$pid_file"
    echo "Stopped ${service} (${pid})"
  fi

  orphan_pids="$(pgrep -f "$bin_path" || true)"
  if [[ -n "$orphan_pids" ]]; then
    while IFS= read -r orphan_pid; do
      [[ -z "$orphan_pid" ]] && continue
      kill "$orphan_pid" 2>/dev/null || true
      sleep 1
      kill -9 "$orphan_pid" 2>/dev/null || true
      echo "Stopped orphan ${service} (${orphan_pid})"
    done <<< "$orphan_pids"
  fi
done
