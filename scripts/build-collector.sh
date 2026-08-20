#!/bin/bash

# Build script for probara-collector — the minimal OpenTelemetry Collector
# distribution (OCB manifest: collector/manifest.yaml) served from
# /static/collector/ and installed on hosts by the generated install scripts.
#
# OCB_VERSION is pinned and MUST match the component versions in
# collector/manifest.yaml — bump them together (see CLAUDE.md gotchas).

set -e

OCB_VERSION=${OCB_VERSION:-"v0.159.0"}
BUILD_DIR=${BUILD_DIR:-"./static/collector"}
DIST_DIR="collector/dist"

echo "Generating probara-collector sources (OCB ${OCB_VERSION})..."
(cd collector && go run go.opentelemetry.io/collector/cmd/builder@${OCB_VERSION} --config manifest.yaml --skip-compilation)

mkdir -p "${BUILD_DIR}"
rm -f "${BUILD_DIR}"/probara-collector-* "${BUILD_DIR}/checksums.txt"

build() {
  local goos=$1 goarch=$2 suffix=$3
  echo "Building for ${goos} ${goarch}..."
  (cd "${DIST_DIR}" && CGO_ENABLED=0 GOOS=${goos} GOARCH=${goarch} \
    go build -ldflags="-s -w" -o "../../${BUILD_DIR}/probara-collector-${goos}-${goarch}${suffix}" .)
}

build linux amd64 ""
build linux arm64 ""
build darwin amd64 ""
build darwin arm64 ""
build windows amd64 ".exe"

echo "Build complete! Binaries are in ${BUILD_DIR}/"
ls -lh "${BUILD_DIR}/"

echo ""
echo "Generating checksums..."
(cd "${BUILD_DIR}" && sha256sum probara-collector-* > checksums.txt && cat checksums.txt) || \
(cd "${BUILD_DIR}" && shasum -a 256 probara-collector-* > checksums.txt && cat checksums.txt)

echo ""
echo "Done!"
