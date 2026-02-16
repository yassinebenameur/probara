#!/bin/bash

# Script to run the API service with proper environment variables

set -e

# Default values
HTTP_PORT=${HTTP_PORT:-8080}
METRICS_PORT=${METRICS_PORT:-9090}
LOG_LEVEL=${LOG_LEVEL:-info}
POSTGRES_URL=${POSTGRES_URL:-postgres://probara:probara@localhost:5432/probara?sslmode=disable}
NATS_URL=${NATS_URL:-nats://localhost:4222}

echo "Starting API service with configuration:"
echo "  HTTP_PORT: $HTTP_PORT"
echo "  METRICS_PORT: $METRICS_PORT"
echo "  LOG_LEVEL: $LOG_LEVEL"
echo "  POSTGRES_URL: $POSTGRES_URL"
echo ""

export HTTP_PORT
export METRICS_PORT
export LOG_LEVEL
export POSTGRES_URL
export NATS_URL

# Run the API service
go run ./api/cmd/api

