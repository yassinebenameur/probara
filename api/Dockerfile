# Build stage
FROM golang:1.26.5-alpine AS builder

WORKDIR /build

# Copy go mod files
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build the API application
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o /app/api ./api/cmd/api

# Build probara-collector binaries for distribution (minimal OTel Collector,
# OCB manifest in collector/manifest.yaml; scripts/build-collector.sh is the
# same pipeline for local dev). The pinned builder version must match the
# manifest's component versions — bump together.
RUN BUILD_DIR=/app/static/collector sh scripts/build-collector.sh

# Runtime stage
FROM alpine:latest

RUN apk --no-cache add ca-certificates tzdata

WORKDIR /app

# Create non-root user
RUN addgroup -g 1000 appuser && \
    adduser -D -u 1000 -G appuser appuser

# Copy binary from builder
COPY --from=builder /app/api .

# Copy migrations
COPY --from=builder /build/shared/db/migrations ./shared/db/migrations

# Copy collector binaries for download
COPY --from=builder /app/static ./static

# Change ownership
RUN chown -R appuser:appuser /app

USER appuser

EXPOSE 8080 9090

HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
  CMD wget --no-verbose --tries=1 --spider http://localhost:8080/healthz || exit 1

CMD ["./api"]
