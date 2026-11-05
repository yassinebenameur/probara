# Build stage
# Runs on the build host and cross-compiles for the target platform, so a
# multi-arch build never compiles Go under QEMU (only the runtime stage does).
FROM --platform=$BUILDPLATFORM golang:1.26.5-alpine AS builder

WORKDIR /build

# Copy go mod files
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build the application
# Release version stamped into shared/buildinfo (docker-build.yml passes the
# metadata action's version). Declared just before the build so a new version
# only invalidates this layer.
ARG VERSION=dev
ARG TARGETOS TARGETARCH
RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH} go build -a -installsuffix cgo -ldflags "-X github.com/yassinebenameur/probara/shared/buildinfo.Version=${VERSION}" -o /app/worker ./worker/cmd/worker

# Runtime stage
FROM alpine:3.24

RUN apk --no-cache add \
    ca-certificates \
    tzdata \
    chromium \
    nss \
    freetype \
    harfbuzz \
    ttf-freefont

WORKDIR /app

# Create non-root user
RUN addgroup -g 1000 appuser && \
    adduser -D -u 1000 -G appuser appuser

# Copy binary from builder
COPY --from=builder /app/worker .

# Change ownership
RUN chown -R appuser:appuser /app

USER appuser

ENV CHROME_BIN=/usr/bin/chromium-browser

EXPOSE 9090

HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
  CMD wget --no-verbose --tries=1 --spider http://localhost:9090/healthz || exit 1

CMD ["./worker"]
