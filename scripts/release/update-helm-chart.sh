#!/usr/bin/env bash
set -euo pipefail

VERSION="${1:-}"
if [[ -z "$VERSION" ]]; then
  echo "usage: $0 <version>" >&2
  exit 1
fi

CHART_FILE="helm/monitoring-platform/Chart.yaml"
if [[ ! -f "$CHART_FILE" ]]; then
  echo "chart file not found: $CHART_FILE" >&2
  exit 1
fi

TMP_FILE="$(mktemp)"
awk -v version="$VERSION" '
  /^version:/ {
    print "version: " version
    next
  }
  /^appVersion:/ {
    print "appVersion: \"" version "\""
    next
  }
  {
    print $0
  }
' "$CHART_FILE" > "$TMP_FILE"

mv "$TMP_FILE" "$CHART_FILE"

echo "Updated Helm chart version to $VERSION"
