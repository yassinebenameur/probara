# Running the Probara Platform

## Quick Start

### Start Everything with One Command 🚀

```bash
make start-all
```

This single command will:
- Start all backend services (API, Scheduler, Worker, Alerter, Status Page, PostgreSQL, NATS)
- Run database migrations automatically
- Start the default-on local AI container (`local-ai`, loopback-bound on
  127.0.0.1:8090) after the platform; if its image cannot build or start, a
  warning is printed and the platform runs with AI analysis unavailable
- Wait for services to be healthy
- Start the Next.js UI using the current nvm LTS release
- Show all service URLs

**UI logs** are saved to `/tmp/probara-ui.log`. View them with:
```bash
tail -f /tmp/probara-ui.log
```

### Start App Services Locally

```bash
make start-all-local
```

This command:
- starts `postgres` and `nats` with Docker Compose
- runs database bootstrap and migrations locally
- starts `api`, `scheduler`, `worker`, `status-page`, and `alerter` with `go run`
- then starts the `local-ai` container in the background (Docker Compose; the
  first image build takes several minutes and needs Git LFS, see Requirements),
  with a per-checkout bearer token generated into the gitignored
  `.dev-local-ai-token`; a failed build leaves AI unavailable, not the platform
- starts the Next.js UI locally with `nvm use --lts`

Local service logs are saved under `/tmp/probara-*.log`, and startup validation logs are written to `/tmp/probara-local-start.log`.

Set `LOCAL_AI_DISABLED=true` to skip local AI with either target. Its image
builds only from a release-approved model artifact; see
[`docs/local-ai.md`](docs/local-ai.md).

If Go is missing or the installed version does not match the repository requirement in [`go.mod`](go.mod), startup fails immediately and logs the reason.

### Stop Everything

```bash
make stop-all
```

This will stop both backend services and the UI.

To stop the local-Go variant (this also stops the `local-ai` container):

```bash
make stop-all-local
```

## Service URLs

Once running, you can access:

- **UI (Next.js)**: http://localhost:3000
- **API**: http://localhost:8080
- **Status Page**: http://localhost:8082
- **NATS Monitoring**: http://localhost:8222
- **Local AI**: http://127.0.0.1:8090 (`/healthz`; `/readyz` needs the bearer token)

## Health Metrics

- **API Metrics**: http://localhost:9090
- **Scheduler Metrics**: http://localhost:9091
- **Worker Metrics**: http://localhost:9092
- **Status Page Metrics**: http://localhost:9093
- **Alerter Metrics**: http://localhost:9094

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

## Private Location Workers

Checks can run from remote networks ("private locations"). Register a location
on the **Locations** page, then deploy a worker where the checks should run —
the page generates a ready-to-paste `docker run` / compose snippet.

Location workers are **NATS-only**:

- Set `NATS_URL` (must be reachable from the remote network — enable NATS
  auth/TLS before exposing it) and `WORKER_LOCATION_ID` (the location's UUID).
- Do **not** set `POSTGRES_URL`. Results are published over NATS and persisted
  by the scheduler-side ingest consumer; the worker never touches the database.
- To try it locally, uncomment the `worker-location-example` service in
  `docker-compose.yml` and paste a location UUID into `WORKER_LOCATION_ID`.

Monitors select their locations in the monitor form; with 2+ locations a
quorum ("locations required down") controls when the monitor counts as down —
fewer failing locations show as **Degraded** (amber, no alert).

## Requirements

- Docker & Docker Compose v2
- Go `1.26.5`
- Git LFS, to build the local AI image from source: `git lfs install && git lfs pull --include 'ai-service/artifacts/*/head.safetensors'` once after cloning (`LOCAL_AI_DISABLED=true` skips local AI)
- Node.js via nvm — use the current LTS release selected by `.nvmrc`
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
- `start-all-local` keeps Postgres, NATS and the `local-ai` container in Docker and runs app services with local Go
- A locally built `local-ai` image is reused as is: after an `ai-service/` or `MODEL_VERSION` change, run `docker compose build local-ai` (or `docker compose up -d --build local-ai`)
- UI logs are saved to `/tmp/probara-ui.log`
- Local Go service logs are saved to `/tmp/probara-api.log`, `/tmp/probara-scheduler.log`, `/tmp/probara-worker.log`, `/tmp/probara-status-page.log`, and `/tmp/probara-alerter.log`
- Use `make help` to see all available commands
- The startup script (`scripts/start-ui.sh`) handles nvm initialization automatically
