#!/bin/bash

# Script to create test tenant and API key
# This requires psql to be installed and the database to be running

set -e

POSTGRES_URL="${POSTGRES_URL:-postgres://probara:probara@localhost:5432/probara?sslmode=disable}"

echo "Creating test tenant and API key..."

# Create a test tenant
TENANT_ID=$(psql "$POSTGRES_URL" -t -c "INSERT INTO tenants (name) VALUES ('test-tenant') RETURNING id;" | tr -d ' ')

if [ -z "$TENANT_ID" ]; then
    echo "Failed to create tenant"
    exit 1
fi

echo "Created tenant with ID: $TENANT_ID"

# Generate a test API key (in production, this would be generated securely)
TEST_API_KEY="test-api-key-$(date +%s)"

# Hash the API key using bcrypt (we'll need to do this via a Go script or use a simple approach)
# For now, let's create a simple Go script to hash the key
cat > /tmp/hash_key.go << 'EOF'
package main

import (
	"fmt"
	"os"
	"golang.org/x/crypto/bcrypt"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, "Usage: %s <api-key>\n", os.Args[0])
		os.Exit(1)
	}
	key := os.Args[1]
	hash, err := bcrypt.GenerateFromPassword([]byte(key), 12)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	fmt.Print(string(hash))
}
EOF

KEY_HASH=$(go run /tmp/hash_key.go "$TEST_API_KEY")

# Insert the API key
psql "$POSTGRES_URL" -c "INSERT INTO api_keys (tenant_id, name, key_hash) VALUES ('$TENANT_ID', 'test-key', '$KEY_HASH');"

echo ""
echo "========================================="
echo "Test API Key created successfully!"
echo "========================================="
echo "Tenant ID: $TENANT_ID"
echo "API Key: $TEST_API_KEY"
echo ""
echo "Use this header in your API requests:"
echo "Authorization: Bearer $TEST_API_KEY"
echo "========================================="

