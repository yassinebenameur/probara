#!/bin/bash
# Start the Next.js UI with nvm

set -euo pipefail

WEB_DIR="$(cd "$(dirname "$0")/../web" && pwd)"

# Load nvm
export NVM_DIR="$HOME/.nvm"
if [ -s "$NVM_DIR/nvm.sh" ]; then
  set +u
  . "$NVM_DIR/nvm.sh"
  nvm use --lts >/dev/null
  set -u
fi

# Ensure nvm was available before continuing.
if ! command -v node >/dev/null 2>&1; then
  echo "Node.js is not available. Install nvm or Node before starting the UI." >&2
  exit 1
fi

# Stop any existing Probara Next dev servers before starting a new one.
pkill -f "$WEB_DIR/node_modules/.bin/next dev" || true

# Start Next.js dev server on the expected port.
cd "$WEB_DIR"
exec npm run dev -- --hostname 0.0.0.0 --port 3000



