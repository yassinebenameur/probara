#!/bin/bash

# Build script for cross-compiling the Probara agent

set -e

VERSION=${VERSION:-"1.0.0"}
BUILD_DIR=${BUILD_DIR:-"./static/agent"}

echo "Building Probara agent v${VERSION}..."

# Create build directory
mkdir -p "${BUILD_DIR}"
rm -f "${BUILD_DIR}"/probara-agent-* "${BUILD_DIR}/checksums.txt"

cd agent

# Build for Linux (amd64)
echo "Building for Linux amd64..."
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w -X main.Version=${VERSION}" -o "../${BUILD_DIR}/probara-agent-linux-amd64" ./cmd/agent

# Build for Linux (arm64)
echo "Building for Linux arm64..."
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -ldflags="-s -w -X main.Version=${VERSION}" -o "../${BUILD_DIR}/probara-agent-linux-arm64" ./cmd/agent

# Build for macOS (amd64)
echo "Building for macOS amd64..."
CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build -ldflags="-s -w -X main.Version=${VERSION}" -o "../${BUILD_DIR}/probara-agent-darwin-amd64" ./cmd/agent

# Build for macOS (arm64)
echo "Building for macOS arm64..."
CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -ldflags="-s -w -X main.Version=${VERSION}" -o "../${BUILD_DIR}/probara-agent-darwin-arm64" ./cmd/agent

# Build for Windows (amd64)
echo "Building for Windows amd64..."
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -ldflags="-s -w -X main.Version=${VERSION}" -o "../${BUILD_DIR}/probara-agent-windows-amd64.exe" ./cmd/agent

cd ..

echo "Build complete! Binaries are in ${BUILD_DIR}/"
ls -lh "${BUILD_DIR}/"

# Optional: Create checksums
echo ""
echo "Generating checksums..."
cd "${BUILD_DIR}"
sha256sum probara-agent-* > checksums.txt
cat checksums.txt
cd ../..

echo ""
echo "Done!"
