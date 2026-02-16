# Build stage
FROM golang:1.21-alpine AS builder

WORKDIR /app

# Copy go modules
COPY agent/go.mod agent/go.sum* ./
COPY shared/ ../shared/

# Download dependencies
RUN go mod download

# Copy source code
COPY agent/ ./

# Build the agent
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o agent ./cmd/agent

# Final stage
FROM alpine:latest

RUN apk --no-cache add ca-certificates

WORKDIR /root/

# Copy the binary from builder
COPY --from=builder /app/agent .

# Set default environment variables (can be overridden)
ENV BACKEND_URL=""
ENV AGENT_ID=""
ENV API_KEY=""
ENV INTERVAL=60
ENV DISK_PATH="/"

# Run the agent
ENTRYPOINT ["./agent"]
CMD ["-backend-url", "${BACKEND_URL}", "-agent-id", "${AGENT_ID}", "-api-key", "${API_KEY}", "-interval", "${INTERVAL}", "-disk-path", "${DISK_PATH}"]

