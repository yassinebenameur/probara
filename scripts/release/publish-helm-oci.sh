#!/usr/bin/env bash
set -euo pipefail

VERSION="${1:-}"
if [[ -z "$VERSION" ]]; then
  echo "usage: $0 <version>" >&2
  exit 1
fi

OWNER="${GHCR_OWNER:-${GITHUB_REPOSITORY_OWNER:-}}"
if [[ -z "$OWNER" ]]; then
  echo "GHCR owner not set; set GHCR_OWNER or GITHUB_REPOSITORY_OWNER" >&2
  exit 1
fi

CHART_DIR="helm/monitoring-platform"
DIST_DIR="dist/helm"
OCI_REPO="${GHCR_HELM_REPO:-oci://ghcr.io/${OWNER}/charts}"
CHART_NAME="$(awk '/^name:/ {print $2; exit}' "${CHART_DIR}/Chart.yaml")"
if [[ -z "$CHART_NAME" ]]; then
  echo "unable to read chart name from ${CHART_DIR}/Chart.yaml" >&2
  exit 1
fi
PACKAGE_FILE="${DIST_DIR}/${CHART_NAME}-${VERSION}.tgz"

mkdir -p "$DIST_DIR"
helm package "$CHART_DIR" --destination "$DIST_DIR"

if [[ ! -f "$PACKAGE_FILE" ]]; then
  echo "expected chart package not found: $PACKAGE_FILE" >&2
  exit 1
fi

helm push "$PACKAGE_FILE" "$OCI_REPO"

echo "Published Helm chart to ${OCI_REPO}/${CHART_NAME}:${VERSION}"
