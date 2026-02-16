.PHONY: help up down restart logs build clean ps healthcheck migrate test test-cover lint fmt vet

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
	@echo "  make rebuild     - Rebuild and restart all services"
	@echo "  make clean       - Stop services and remove volumes"
	@echo "  make ps          - Show running services"
	@echo "  make healthcheck - Check health of all services"
	@echo "  make migrate     - Run database migrations"
	@echo "  make scale-workers N=3 - Scale worker service to N instances"
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
	docker compose up -d

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
	docker compose build

# Rebuild and restart
rebuild:
	docker compose up -d --build

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
	docker compose up -d --scale worker=$(N)

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
	@echo "Running migrations..."
	docker compose run --rm migrations
	@echo "✅ Infrastructure ready for local development"

dev-stop:
	docker compose stop postgres nats

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
	@gofmt_output=$$(gofmt -l .); \
	if [ -n "$$gofmt_output" ]; then \
		echo "The following files need formatting:"; \
		echo "$$gofmt_output"; \
		exit 1; \
	fi
	@echo "✅ Code formatting OK"

vet:
	@echo "Running go vet..."
	go vet $$(go list ./... | grep -v /scripts)
	@echo "✅ go vet passed"
