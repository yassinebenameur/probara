#!/bin/bash
# Start the Next.js UI with nvm

# Load nvm
export NVM_DIR="$HOME/.nvm"
[ -s "$NVM_DIR/nvm.sh" ] && \. "$NVM_DIR/nvm.sh"

# Use LTS version
nvm use --lts

# Start Next.js dev server
cd "$(dirname "$0")/../web" && npm run dev





