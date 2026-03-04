# Probara Monitoring Platform

Kubernetes-first uptime monitoring for APIs, services, and endpoints.

## What This Project Is

Probara is a blackbox monitoring platform designed to run as distributed microservices on Kubernetes:

- API service for monitor, alert policy, status page, tenant, and auth workflows
- Scheduler that continuously enqueues check jobs
- Worker pool that executes checks in parallel
- Alerter that evaluates failures and dispatches notifications
- Status page service for public uptime pages
- Web UI (Next.js) for operations

Supported monitor types in the codebase:

- `http`
- `ping`
- `dns`
- `sip`
- `synthetic_api`
- `synthetic_browser`
- `push`
- `agent`
- `group`

## Why This Is Kubernetes-Focused And Built For Scale

- Microservice split by responsibility (API, scheduler, workers, alerter, status page, frontend)
- Stateless application services; state is externalized to PostgreSQL + NATS JetStream
- Queue-based fan-out (`scheduler -> NATS -> workers`) for high-throughput check execution
- Horizontal scaling by replicas (especially workers)
- Built-in worker autoscaling support in Helm (`HorizontalPodAutoscaler`)
- Health/readiness/metrics endpoints on each service for Kubernetes probes and observability

High-level flow:

```
clients -> API -> PostgreSQL
               -> NATS JetStream <- Scheduler
                                  <- Workers (N replicas)
workers -> check results -> PostgreSQL -> Alerter + Status Page
```

## Quick Start (Local Docker)

Prerequisites:

- Docker + Docker Compose v2
- Go 1.23+ (for local commands like admin bootstrap)
- Node.js LTS via `nvm` (for UI)
- Make

Use nvm LTS before running UI commands:

```bash
nvm install --lts
nvm use --lts
```

Start backend services:

```bash
make up
```

This starts: `postgres`, `nats`, `migrations`, `api`, `scheduler`, `worker`, `alerter`, `status-page`.

Create/update an admin user (one-time):

```bash
POSTGRES_URL='postgres://probara:probara@localhost:5432/probara?sslmode=disable' \
ADMIN_USERNAME='admin' \
ADMIN_PASSWORD='change-me' \
go run ./cmd/admin
```

Run the UI:

```bash
cd web
npm install
npm run dev
```

Useful local URLs:

- UI: `http://localhost:3000`
- API: `http://localhost:8080`
- Status page service: `http://localhost:8082`
- NATS monitor: `http://localhost:8222`
- Metrics: `:9090` (api), `:9091` (scheduler), `:9092` (worker), `:9093` (status-page), `:9094` (alerter)

## Kubernetes Deployment (Helm)

Helm chart path:

- `helm/monitoring-platform`

Prerequisites:

- Kubernetes cluster
- `kubectl`
- `helm`

Install or upgrade:

```bash
helm upgrade --install probara ./helm/monitoring-platform \
  --namespace monitoring \
  --create-namespace \
  -f values.prod.yaml
```

### Minimal `values.prod.yaml`

```yaml
image:
  repository: ghcr.io/yassinebenameur/probara
  tag: "latest" # use a pinned release tag in production

secrets:
  adminJwtSecret: "replace-with-32-plus-char-secret"
  initialApiKey: "replace-with-secure-initial-key"

ingress:
  enabled: true
  className: nginx
  hosts:
    - host: probara.example.com
      paths:
        - path: /
          pathType: Prefix
    - host: status.example.com
      paths:
        - path: /
          pathType: Prefix
  tls: []

statusPage:
  baseUrl: "https://status.example.com"

frontend:
  apiUrl: "/api"
  # Optional override. Defaults to in-cluster API service:
  # http://<release>-probara-api:8080 (or <release>-api if nameOverride/fullnameOverride changes naming)
  apiProxyTarget: ""

worker:
  replicas: 2
  checkJobStream: "check-jobs"
  checkJobSubject: "check.job"
  autoscaling:
    enabled: true
    minReplicas: 2
    maxReplicas: 20
    targetCPUUtilizationPercentage: 70
```

### Using External PostgreSQL/NATS

```yaml
postgresql:
  enabled: false
  externalUrl: "postgres://user:pass@postgres.example:5432/probara?sslmode=require"

nats:
  enabled: false
  externalUrl: "nats://nats.example:4222"
```

Set scheduler and worker job stream/subject to the same values. If you use API "run now" checks, keep `CHECK_JOB_SUBJECT` aligned there as well.

### Access Without Ingress

```bash
kubectl port-forward -n monitoring svc/probara-frontend 3000:3000
kubectl port-forward -n monitoring svc/probara-api 8080:8080
kubectl port-forward -n monitoring svc/probara-status-page 8082:8080
```

`/api/*` requests go through the frontend server and are proxied to `API_PROXY_TARGET`
(set automatically by the chart to the API service in-cluster).

## Environment Variables

When running services directly (outside Helm), these are the main environment variables.

### Core (all services)

| Variable | Required | Default |
| --- | --- | --- |
| `HTTP_PORT` | yes | none |
| `METRICS_PORT` | yes | none |
| `LOG_LEVEL` | no | `info` |
| `POSTGRES_URL` | service-dependent | empty |
| `NATS_URL` | no | `nats://localhost:4222` |

### API

| Variable | Required | Default |
| --- | --- | --- |
| `ADMIN_JWT_SECRET` | yes | none |
| `ADMIN_ACCESS_TTL_MINUTES` | no | `15` |
| `ADMIN_REFRESH_TTL_DAYS` | no | `30` |
| `ADMIN_COOKIE_SECURE` | no | `false` |
| `ADMIN_BCRYPT_COST` | no | `12` |
| `CHECK_JOB_SUBJECT` | no | `check.jobs` |
| `SYNTHETIC_BROWSER_ARTIFACTS_DIR` | no | temp dir |

### Scheduler

| Variable | Required | Default |
| --- | --- | --- |
| `SCHEDULE_INTERVAL_SECONDS` | no | `2` |
| `SCHEDULER_BATCH_SIZE` | no | `500` |
| `CHECK_JOB_STREAM` | no | `CHECK_JOBS` |
| `CHECK_JOB_SUBJECT` | no | `check.jobs` |
| `RETENTION_CLEANUP_ENABLED` | no | `true` |
| `RETENTION_CLEANUP_HOUR_UTC` | no | `2` |
| `RETENTION_CLEANUP_BATCH_SIZE` | no | `5000` |
| `RETENTION_CLEANUP_MAX_ROWS_PER_RUN` | no | `200000` |

Retention setting semantics:
- `data_retention_days = 0` means unlimited history retention.
- `data_retention_days` between `30` and `3650` enables automatic deletion of older check results.

### Worker

| Variable | Required | Default |
| --- | --- | --- |
| `WORKER_CONCURRENCY` | no | `10` |
| `NATS_CONSUMER_NAME` | no | `check-workers` |
| `CHECK_JOB_STREAM` | no | `CHECK_JOBS` |
| `CHECK_JOB_SUBJECT` | no | `check.jobs` |
| `MAX_HTTP_TIMEOUT_SECONDS` | no | `30` |
| `MAX_BODY_SIZE_BYTES` | no | `1048576` |
| `HTTP_BLOCK_PRIVATE_IPS` | no | `false` |
| `HTTP_ALLOWED_CIDRS` | no | empty |
| `SYNTHETIC_BROWSER_ARTIFACTS_DIR` | no | temp dir |
| `CHROME_BIN` | no | auto-detected |
| `SIP_LOCALHOST_AS_HOST_GATEWAY` | no | `false` |

### Alerter

| Variable | Required | Default |
| --- | --- | --- |
| `ALERT_STREAM` | no | `ALERTS` |
| `ALERT_SUBJECT` | no | `alerts` |
| `ALERT_EVAL_INTERVAL_SECONDS` | no | `30` |
| `ALERT_REMINDER_INTERVAL_SECONDS` | no | `3600` |
| `ALERT_GROUP_WINDOW_SECONDS` | no | `60` |
| `ALERT_GROUP_MAX_CHILDREN` | no | `5` |
| `SMTP_HOST` / `SMTP_PORT` / `SMTP_USERNAME` / `SMTP_PASSWORD` / `SMTP_FROM` / `SMTP_USE_TLS` | optional | varies |
| `ALERT_EMAIL_TO` | optional | empty |

### Status Page

| Variable | Required | Default |
| --- | --- | --- |
| `STATUS_PAGE_BASE_URL` | no | empty |
| `STATUS_PAGE_API_BASE_URL` | no | empty |

## Operational Commands

```bash
make help
make up
make down
make logs
make healthcheck
make scale-workers N=3
make migrate
make test
```

## Notes

- `README.md` reflects the current repo state: backend services run via `docker-compose.yml`; UI runs from `web/`.
- Helm chart includes embedded PostgreSQL and NATS, optional external dependencies, and worker HPA support.
- Helm chart runs DB migrations as a `post-install` / `post-upgrade` hook job.
- Architecture details: `docs/architecture.md`.
