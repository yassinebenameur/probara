.PHONY: help up down restart logs build build-collector-static kuma-export clean ps healthcheck migrate test test-cover lint fmt vet start-all stop-all restart-all start-all-local stop-all-local restart-all-local dev-start dev-stop

# Local AI runs beside the platform, never in its way: its image build fails on
# purpose for an artifact that is not release-approved, and Compose aborts a
# whole `up`/`build` when any one build fails. So every target below addresses
# the platform services by name (all Compose services except local-ai, read
# from `docker compose config`) and runs local-ai in a separate, tolerant
# command. LOCAL_AI_DISABLED=true skips it, and api/worker then run with the
# feature off.
PLATFORM_SERVICES = $$(docker compose config --services | grep -vx local-ai)
# $(call local_ai,<compose subcommand and flags>,<what failed>). Docker
# refuses a container whose --cpus exceeds the host's CPU count, so unless set
# explicitly LOCAL_AI_CPUS defaults to min(host CPUs, 4) and LOCAL_AI_THREADS
# to min(LOCAL_AI_CPUS, 4); the model is CPU-validated at 4 threads.
local_ai = if [ "$${LOCAL_AI_DISABLED:-false}" != "true" ]; then \
	cpus="$${LOCAL_AI_CPUS:-$$(docker info --format '{{.NCPU}}' 2>/dev/null)}"; \
	[ "$$cpus" -ge 1 ] 2>/dev/null || cpus=4; \
	[ -n "$${LOCAL_AI_CPUS:-}" ] || [ "$$cpus" -le 4 ] || cpus=4; \
	threads="$${LOCAL_AI_THREADS:-$$cpus}"; \
	[ -n "$${LOCAL_AI_THREADS:-}" ] || [ "$$threads" -le 4 ] || threads=4; \
	LOCAL_AI_CPUS="$$cpus" LOCAL_AI_THREADS="$$threads" docker compose $(1) local-ai \
	|| echo "WARNING: local-ai $(2) failed; the platform runs with AI analysis unavailable (LOCAL_AI_DISABLED=true to skip)"; \
	fi

# Default target
help:
	@echo "Probara - Docker Compose Management"
	@echo ""
	@echo "Available commands:"
	@echo "  make up          - Start all services"
	@echo "  make down        - Stop all services"
	@echo "  make restart     - Restart all services"
	@echo "  make logs        - View logs from all services"
	@echo "  make build       - Build all Docker images"
	@echo "  make build-collector-static - Build downloadable collector binaries"
	@echo "  make kuma-export - Build the Uptime Kuma export helper"
	@echo "  make rebuild     - Rebuild and restart all services"
	@echo "  make clean       - Stop services and remove volumes"
	@echo "  make ps          - Show running services"
	@echo "  make healthcheck - Check health of all services"
	@echo "  make migrate     - Run database migrations"
	@echo "  make scale-workers N=3 - Scale worker service to N instances"
	@echo "  make start-all-local - Start infra + local AI in Docker and app services locally"
	@echo "  make stop-all-local - Stop local app services, local AI, UI, and infra"
	@echo "  make restart-all-local - Restart local app services, UI, and infra"
	@echo "  make start-all   - Start infra, Docker app services, and UI"
	@echo "  make stop-all    - Stop Docker app services and UI"
	@echo "  make restart-all - Restart Docker app services and UI"
	@echo ""
	@echo "Testing and CI:"
	@echo "  make test        - Run all tests with race detection and coverage"
	@echo "  make test-cover  - Run tests and open coverage report in browser"
	@echo "  make lint        - Run go vet and fmt checks"
	@echo "  make fmt         - Format Go code"
	@echo "  make vet         - Run go vet"
	@echo ""
	@echo "Service-specific logs:"
	@echo "  make logs-api    - View API logs"
	@echo "  make logs-scheduler - View scheduler logs"
	@echo "  make logs-worker - View worker logs"
	@echo "  make logs-status - View status page logs"
	@echo ""
	@echo "Database commands:"
	@echo "  make db-shell    - Access PostgreSQL shell"
	@echo "  make db-backup   - Backup database to backup.sql"
	@echo "  make db-restore  - Restore database from backup.sql"

# Start all services
up:
	services="$(PLATFORM_SERVICES)" && docker compose up -d $$services
	@$(call local_ai,up -d,start)

# Stop all services
down:
	docker compose down

# Restart all services
restart:
	docker compose restart

# View logs
logs:
	docker compose logs -f

logs-api:
	docker compose logs -f api

logs-scheduler:
	docker compose logs -f scheduler

logs-worker:
	docker compose logs -f worker

logs-status:
	docker compose logs -f status-page

# Build images
build:
	services="$(PLATFORM_SERVICES)" && docker compose build $$services
	@$(call local_ai,build,build)

build-collector-static:
	@bash scripts/build-collector.sh

# Build the Uptime Kuma export helper (see scripts/kuma-export/README.md)
kuma-export:
	go build -o bin/kuma-export ./scripts/kuma-export

# Rebuild and restart
rebuild:
	services="$(PLATFORM_SERVICES)" && docker compose up -d --build $$services
	@$(call local_ai,up -d --build,rebuild)

# Clean up (remove volumes)
clean:
	docker compose down -v
	@echo "⚠️  All data has been removed!"

# Show running services
ps:
	docker compose ps

# Health checks
healthcheck:
	@echo "Checking service health..."
	@echo ""
	@echo "API:"
	@curl -s -o /dev/null -w "  Status: %{http_code}\n" http://localhost:8080/healthz || echo "  Status: Not responding"
	@echo ""
	@echo "Scheduler:"
	@curl -s -o /dev/null -w "  Status: %{http_code}\n" http://localhost:9091/healthz || echo "  Status: Not responding"
	@echo ""
	@echo "Worker:"
	@curl -s -o /dev/null -w "  Status: %{http_code}\n" http://localhost:9092/healthz || echo "  Status: Not responding"
	@echo ""
	@echo "Status Page:"
	@curl -s -o /dev/null -w "  Status: %{http_code}\n" http://localhost:9093/healthz || echo "  Status: Not responding"
	@echo ""
	@echo "NATS:"
	@curl -s -o /dev/null -w "  Status: %{http_code}\n" http://localhost:8222/healthz || echo "  Status: Not responding"

# Run migrations
migrate:
	docker compose run --rm migrations

# Scale workers
scale-workers:
	@if [ -z "$(N)" ]; then echo "Usage: make scale-workers N=3"; exit 1; fi
	services="$(PLATFORM_SERVICES)" && docker compose up -d --scale worker=$(N) $$services
	@$(call local_ai,up -d,start)

# Database commands
db-shell:
	docker compose exec postgres psql -U probara -d probara

db-backup:
	docker compose exec postgres pg_dump -U probara probara > backup.sql
	@echo "✅ Database backed up to backup.sql"

db-restore:
	@if [ ! -f backup.sql ]; then echo "❌ backup.sql not found"; exit 1; fi
	docker compose exec -T postgres psql -U probara probara < backup.sql
	@echo "✅ Database restored from backup.sql"

# Development helpers
dev-start:
	@echo "Starting infrastructure (postgres + nats)..."
	docker compose up -d postgres nats
	@echo "Waiting for services to be ready..."
	@sleep 5
	@echo "Bootstrapping local database access..."
	@bash scripts/bootstrap-local-db.sh
	@echo "Running migrations..."
	docker compose run --rm migrations
	@echo "✅ Infrastructure ready for local development"

dev-stop:
	docker compose stop local-ai postgres nats

# Start everything (backend + UI with nvm LTS)
start-all:
	@echo "Starting backend services..."
	docker compose up -d postgres nats
	@echo "Waiting for infrastructure to be ready..."
	@sleep 5
	@echo "Bootstrapping local database access..."
	@bash scripts/bootstrap-local-db.sh
	@echo "Running database migrations..."
	docker compose run --rm migrations
	@echo "Starting application services..."
	@$(call local_ai,up -d,start)
	docker compose up -d api scheduler worker status-page alerter
	@echo "Waiting for services to be healthy..."
	@sleep 10
	@echo "Starting UI with nvm LTS..."
	@cd "$(CURDIR)" && { \
		nohup bash scripts/start-ui.sh > /tmp/probara-ui.log 2>&1 </dev/null & \
		echo $$! > /tmp/probara-ui.pid; \
	}
	@echo "UI started. Logs: tail -f /tmp/probara-ui.log"
	@echo ""
	@echo "Service URLs:"
	@echo "  UI:           http://localhost:3000"
	@echo "  API:          http://localhost:8080"
	@echo "  Status Page:  http://localhost:8082"
	@echo "  NATS Monitor: http://localhost:8222"

# Start everything locally (Go services + UI, Docker only for infra)
start-all-local:
	@echo "Starting infrastructure (postgres + nats)..."
	docker compose up -d postgres nats
	@echo "Waiting for infrastructure to be ready..."
	@sleep 5
	@echo "Bootstrapping local database access..."
	@bash scripts/bootstrap-local-db.sh
	@echo "Validating local Go toolchain..."
	@bash scripts/start-local-services.sh check-go
	@echo "Running database migrations locally..."
	@POSTGRES_URL='postgres://probara:probara@localhost:5432/probara?sslmode=disable' MIGRATIONS_PATH='./shared/db/migrations' go run ./cmd/migrate
	@echo "Building downloadable collector binaries..."
	@bash scripts/build-collector.sh
	@echo "Starting local Go services..."
	@bash scripts/start-local-services.sh
	@echo "Starting UI with nvm LTS..."
	@cd "$(CURDIR)" && { \
		nohup bash scripts/start-ui.sh > /tmp/probara-ui.log 2>&1 </dev/null & \
		echo $$! > /tmp/probara-ui.pid; \
	}
	@echo "UI started. Logs: tail -f /tmp/probara-ui.log"
	@echo "Local service logs: tail -f /tmp/probara-*.log"
	@echo ""
	@echo "Service URLs:"
	@echo "  UI:           http://localhost:3000"
	@echo "  API:          http://localhost:8080"
	@echo "  Status Page:  http://localhost:8082"
	@echo "  NATS Monitor: http://localhost:8222"

# Stop everything (backend + UI)
stop-all:
	@echo "Stopping UI..."
	@if [ -f /tmp/probara-ui.pid ]; then \
		ui_pid=$$(cat /tmp/probara-ui.pid); \
		kill "$$ui_pid" 2>/dev/null || true; \
		sleep 1; \
		kill -9 "$$ui_pid" 2>/dev/null || true; \
		rm -f /tmp/probara-ui.pid; \
	else \
		pkill -f "$(CURDIR)/web/node_modules/.bin/[n]ext dev" || true; \
		pkill -f "[n]ext dev --hostname 0.0.0.0 --port 3000" || true; \
	fi
	@echo "Stopping backend services..."
	docker compose down
	@echo "All services stopped."

# Restart everything (backend + UI)
restart-all: stop-all start-all

# Stop local services, UI, and infrastructure
stop-all-local:
	@echo "Stopping local Go services..."
	@bash scripts/stop-local-services.sh
	@echo "Stopping UI..."
	@if [ -f /tmp/probara-ui.pid ]; then \
		ui_pid=$$(cat /tmp/probara-ui.pid); \
		kill "$$ui_pid" 2>/dev/null || true; \
		sleep 1; \
		kill -9 "$$ui_pid" 2>/dev/null || true; \
		rm -f /tmp/probara-ui.pid; \
	else \
		pkill -f "$(CURDIR)/web/node_modules/.bin/[n]ext dev" || true; \
		pkill -f "[n]ext dev --hostname 0.0.0.0 --port 3000" || true; \
	fi
	@echo "Stopping infrastructure..."
	docker compose stop local-ai postgres nats
	@echo "Local stack stopped."

# Restart local services, UI, and infrastructure
restart-all-local: stop-all-local start-all-local

# Testing and CI
test:
	@echo "Running tests with race detection and coverage..."
	go test -v -race -coverprofile=coverage.out $$(go list ./... | grep -v /scripts)
	@echo "✅ Tests completed. Coverage report: coverage.out"

test-cover: test
	@echo "Opening coverage report..."
	go tool cover -html=coverage.out

lint: fmt vet
	@echo "✅ Lint checks passed"

fmt:
	@echo "Checking code formatting..."
	@gofmt_output=$$(git ls-files -z -- '*.go' | xargs -0 gofmt -l); \
	if [ -n "$$gofmt_output" ]; then \
		echo "The following files need formatting:"; \
		echo "$$gofmt_output"; \
		exit 1; \
	fi
	@echo "✅ Code formatting OK"

vet:
	@echo "Running go vet..."
	go vet ./...
	@echo "✅ go vet passed"
