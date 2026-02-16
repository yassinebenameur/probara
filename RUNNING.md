# Running the Probara Platform

## Quick Start

### Start Everything with One Command 🚀

```bash
make start-all
```

This single command will:
- Start all backend services (API, Scheduler, Worker, Status Page, PostgreSQL, NATS)
- Run database migrations automatically
- Wait for services to be healthy
- Start the Next.js UI using nvm LTS (Node v24.11.1)
- Show all service URLs

**UI logs** are saved to `/tmp/probara-ui.log`. View them with:
```bash
tail -f /tmp/probara-ui.log
```

### Stop Everything

```bash
make stop-all
```

This will stop both backend services and the UI.

## Service URLs

Once running, you can access:

- **UI (Next.js)**: http://localhost:3000
- **API**: http://localhost:8080
- **Status Page**: http://localhost:8082
- **NATS Monitoring**: http://localhost:8222

## Health Metrics

- **API Metrics**: http://localhost:9090
- **Scheduler Metrics**: http://localhost:9091
- **Worker Metrics**: http://localhost:9092
- **Status Page Metrics**: http://localhost:9093

## Other Useful Commands

### Backend Only

```bash
make up      # Start backend services only
make down    # Stop backend services only
make ps      # Show running Docker containers
```

### Health & Monitoring

```bash
make healthcheck    # Check health of all services
make logs          # View all logs
make logs-api      # View API logs only
make logs-scheduler # View scheduler logs only
make logs-worker   # View worker logs only
make logs-status   # View status page logs only
```

### Development

```bash
make restart       # Restart all services
make rebuild       # Rebuild and restart services
make clean         # Stop services and remove data
```

### Database

```bash
make db-shell      # Access PostgreSQL shell
make db-backup     # Backup database
make db-restore    # Restore database
```

## Requirements

- Docker & Docker Compose v2
- Node.js (via nvm) - LTS version (v24.11.1 recommended)
- Make
- Bash

## How It Works

The `make start-all` command uses:
- `docker compose` to start backend services
- `scripts/start-ui.sh` to launch the UI with nvm LTS

All backend services run in Docker containers with health checks and automatic restarts. The UI runs locally for faster development.

## Notes

- The `start-all` command automatically uses nvm LTS for the UI
- All backend services run in Docker containers
- The UI runs locally using npm/next for hot-reload support
- UI logs are saved to `/tmp/probara-ui.log`
- Use `make help` to see all available commands
- The startup script (`scripts/start-ui.sh`) handles nvm initialization automatically

