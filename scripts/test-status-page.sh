#!/bin/bash

# Script to test the modernized status page

set -e

echo "🚀 Testing Status Page Implementation"
echo "======================================"
echo ""

# Check if PostgreSQL is running
echo "1️⃣  Checking PostgreSQL..."
if ! docker ps | grep -q postgres; then
    echo "⚠️  PostgreSQL is not running. Starting it..."
    docker compose -f infra/docker-compose.yml up -d postgres
    echo "⏳ Waiting for PostgreSQL to be ready..."
    sleep 5
else
    echo "✅ PostgreSQL is running"
fi

# Set environment variables
export POSTGRES_URL="postgres://probara:probara@localhost:5432/probara?sslmode=disable"
export HTTP_PORT=8082
export METRICS_PORT=9093
export LOG_LEVEL=info

# Run migrations
echo ""
echo "2️⃣  Running database migrations..."
go run ./cmd/migrate || echo "⚠️  Migrations may have already been applied"

# Build status-page service
echo ""
echo "3️⃣  Building status-page service..."
go build -o bin/status-page ./status-page/cmd/status-page

# Check if we have a test tenant and status page
echo ""
echo "4️⃣  Checking for test data..."
echo "If you don't have test data, run: go run ./scripts/setup-test-data.go"
echo ""

# Start the status-page service
echo "5️⃣  Starting status-page service on port 8082..."
echo ""
echo "📍 Service will be available at: http://localhost:8082"
echo ""
echo "🔗 To view a status page, visit:"
echo "   http://localhost:8082/public/status/{your-status-page-slug}"
echo ""
echo "Example: http://localhost:8082/public/status/my-services"
echo ""
echo "Press Ctrl+C to stop the service"
echo "======================================"
echo ""

./bin/status-page

