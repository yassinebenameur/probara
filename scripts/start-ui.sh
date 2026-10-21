#!/usr/bin/env bash
# Start the Next.js UI with nvm

set -euo pipefail

WEB_DIR="$(cd "$(dirname "$0")/../web" && pwd)"

# Load nvm
export NVM_DIR="$HOME/.nvm"
if [ -s "$NVM_DIR/nvm.sh" ]; then
  set +u
  . "$NVM_DIR/nvm.sh"
  # Prefer the newest installed LTS, then the default alias, then the newest
  # installed version. When none resolves (nvm use exits non-zero), keep
  # whatever node is already on PATH rather than aborting under set -e.
  nvm use --lts >/dev/null 2>&1 || nvm use default >/dev/null 2>&1 || nvm use node >/dev/null 2>&1 || true
  set -u
fi

if ! command -v node >/dev/null 2>&1; then
  echo "Node.js is not available. Install Node.js 20.9 or newer (or nvm) before starting the UI." >&2
  exit 1
fi

# Next.js 16 needs Node >= 20.9 (web/package.json engines).
if ! node -e 'const [a,b]=process.versions.node.split(".").map(Number); process.exit(a>20||(a===20&&b>=9)?0:1)'; then
  echo "Node.js $(node --version) is too old for the UI; install 20.9 or newer." >&2
  exit 1
fi

# Stop any existing Probara Next dev servers before starting a new one
# without matching this shell's own command line.
pkill -f "$WEB_DIR/node_modules/.bin/[n]ext dev" || true

# Start Next.js dev server on the expected port.
cd "$WEB_DIR"
exec npm run dev -- --hostname 0.0.0.0 --port 3000
