#!/usr/bin/env bash

set -euo pipefail

services=(api scheduler worker status-page alerter)

for service in "${services[@]}"; do
  pid_file="/tmp/probara-${service}.pid"
  if [[ -f "$pid_file" ]]; then
    pid="$(cat "$pid_file")"
    kill "$pid" 2>/dev/null || true
    sleep 1
    kill -9 "$pid" 2>/dev/null || true
    rm -f "$pid_file"
    echo "Stopped ${service} (${pid})"
  fi
done
