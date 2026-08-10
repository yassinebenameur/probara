# Probara Monitoring Platform

Self-hosted blackbox monitoring platform: Go microservices connected by NATS
JetStream and PostgreSQL, a Next.js operator UI, and a public status page
service. Runs on Docker Compose or Kubernetes (Helm). Licensed AGPL-3.0.

![Monitors list showing http, dns, tcp, postgres, and group monitors with uptime bars](docs/screenshots/monitors.png)

## What it brings to the table

- **17 monitor types, checked at the real protocol layer** — HTTP (status,
  body, JSON, and TLS-expiry assertions), ICMP ping, DNS records, TCP, gRPC
  health, WebSocket, SIP (OPTIONS and digest-auth REGISTER over udp/tcp/tls),
  Redis, PostgreSQL, MySQL, MongoDB, RabbitMQ, multi-step API sequences with
  variable extraction, scripted browser journeys (headless Chromium via
  chromedp), push heartbeats, a host agent for CPU/memory/disk telemetry, and
  monitor groups.
- **Multi-location checks** — remote workers connect outbound to NATS only,
  never to Postgres, so a private location needs one egress rule. Results are
  tracked per location with a configurable failure quorum; fewer failing
  locations than quorum yields a distinct `degraded` state instead of a false
  `down`. An N×N inter-location mesh probes worker-to-worker paths and alerts
  on broken edges.
- **Small footprint** — every service is a static, CGO-free Go binary. At
  default Helm resource requests, a complete HA install (two replicas of each
  service plus bundled Postgres and NATS) requests about 1 CPU and 1.4 Gi of
  memory; one replica of each app service requests 450m / 576Mi. The host
  agent is a single ~8 MB static binary. The worker image is the one heavy
  image — it bundles Chromium for browser checks.
- **Alerting with context** — consecutive-failure thresholds and latency
  anomaly detection, alert grouping, reminders, and maintenance windows.
  Delivery over email, Slack, Discord, Teams, and webhooks. Incidents, a
  service dependency graph, and optional LLM root-cause analysis through any
  OpenAI-compatible endpoint (configuration only — no hardcoded vendor; local
  vLLM/Ollama work).
- **Access control** — multi-tenancy, OIDC SSO with group-to-role mapping,
  admin/editor/viewer roles, an audit log, read/write-scoped API keys, monitor
  and channel secrets encrypted at rest, and an SSRF guard applied to every
  checker that dials out.
- **Status pages** — theme the built-in page, or replace the entire Go
  template with draft preview and versioned publish/revert. Pages update live
  over NATS and server-sent events.

| Inter-location mesh | Per-location monitor detail |
| --- | --- |
| ![Mesh connectivity matrix with per-edge latencies between three locations](docs/screenshots/mesh.png) | ![Monitor detail with per-location status chips and the failure quorum rule](docs/screenshots/monitor-detail.png) |
| **Public status page** | **Operations dashboard** |
| ![Public status page with 24-hour uptime bars and per-service health](docs/screenshots/status-page.png) | ![Dashboard with uptime and response-time trend and recent changes feed](docs/screenshots/dashboard.png) |

## Architecture

- **API** (chi) — monitors, alert channels, notification settings, incidents,
  status pages, tenants, users, auth (sessions, OIDC, API keys), audit log
- **Scheduler** — enqueues check jobs on NATS JetStream and ingests results:
  per-location state, quorum aggregation, and the monitor state machine
- **Workers** — execute all check types; the default fleet plus optional
  per-location fleets consuming only their location's subject
- **Alerter** — alert lifecycle, latency anomaly detection, grouping,
  mesh-edge alerts, and notification delivery
- **Status page** — public uptime pages with live updates
- **Web** — Next.js operator UI

```
clients ──> API ──> PostgreSQL
              └──> NATS JetStream <── Scheduler (dispatch)
                        │
                        ▼
        Workers (default fleet + per-location fleets)
                        │  results via NATS
                        ▼
        Scheduler ingest ──> PostgreSQL (per-location state + quorum)
                                  │
                 Alerter (lifecycle, anomaly, grouping) ──> channels
                 Status pages (live via NATS + SSE)

host agents / push checks ──> API
workers ◀──▶ workers (mesh echo probes between locations)
```

Details: [`docs/architecture.md`](docs/architecture.md).

## Monitor types

Web & API: `http`, `synthetic_api`, `synthetic_browser`, `websocket`, `grpc`
Network: `ping`, `dns`, `tcp`, `sip`
Databases & brokers: `redis`, `postgres`, `mysql`, `mongodb`, `rabbitmq`
Infrastructure: `agent`, `push`, `group`

`grpc` monitors call `grpc.health.v1.Health/Check` and mark success only when
status is `SERVING`.

## Quick Start (Local)

Prerequisites: Docker + Docker Compose v2, Go 1.26.5, Node.js LTS via `nvm`,
Make.

One command — infra in Docker, app services in containers, UI via nvm:

```bash
make start-all
```

Or run the Go services as local processes (Docker only for Postgres/NATS):

```bash
make start-all-local
```

Both bootstrap the database, run migrations, and start the UI. Create or
update an admin user (one-time):

```bash
POSTGRES_URL='postgres://probara:probara@localhost:5432/probara?sslmode=disable' \
ADMIN_USERNAME='admin' \
ADMIN_PASSWORD='change-me' \
go run ./cmd/admin
```

Useful local URLs:

- UI: `http://localhost:3000`
- API: `http://localhost:8080`
- Status page service: `http://localhost:8082`
- NATS monitor: `http://localhost:8222`
- Metrics: `:9090` (api), `:9091` (scheduler), `:9092` (worker), `:9093` (status-page), `:9094` (alerter)

To try OIDC SSO locally, Compose ships a Dex identity provider behind a
profile: `docker compose --profile sso up -d dex`.

Manual, step-by-step alternative:

```bash
make up            # postgres, nats, migrations, api, scheduler, worker, alerter, status-page
cd web
npm ci
npm run dev
```

## Kubernetes Deployment (Helm)

Chart: `helm/monitoring-platform` (bundled PostgreSQL and NATS, optional
external ones, worker HPA, per-location worker fleets, migrations run as a
`post-install`/`post-upgrade` hook job).

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

api:
  # Public URLs used in agent install scripts, push webhook URLs, and
  # private-location deploy snippets. Set both when exposing Probara.
  publicBaseURL: "https://probara.example.com"
  publicNatsURL: "nats://nats.example.com:4222"

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
  # http://<release>-probara-api:8080

worker:
  replicas: 2
  checkJobStream: "check-jobs"
  checkJobSubject: "check.job"
  # Off by default (chart defaults: min 2, max 10, 80% CPU). Example override:
  autoscaling:
    enabled: true
    minReplicas: 2
    maxReplicas: 10
    targetCPUUtilizationPercentage: 80
```

### Per-location worker fleets

Each `worker.locations[]` entry becomes its own Deployment consuming only that
location's job subject. Locations outside the cluster use the deploy snippet
from the Locations page instead — only NATS must be reachable from there,
never Postgres.

```yaml
worker:
  locations:
    - name: eu-west            # DNS-safe suffix for the Deployment name
      locationId: "<uuid>"     # from the Locations page
      credential: "<secret>"   # LOCATION_CREDENTIAL from deploy info
      natsUrl: "nats://..."    # authenticated NATS URL from deploy info
      replicas: 1
      meshService:             # optional: expose the mesh echo port so
        enabled: true          # other locations can probe this one
        type: ClusterIP        # LoadBalancer for cross-VPC probing
```

### Using external PostgreSQL/NATS

```yaml
postgresql:
  enabled: false
  externalUrl: "postgres://user:pass@postgres.example:5432/probara?sslmode=require"

nats:
  enabled: false
  externalUrl: "nats://nats.example:4222"
```

Set scheduler and worker job stream/subject to the same values. If you use API
"run now" checks, keep `CHECK_JOB_SUBJECT` aligned there as well.

### Access without ingress

```bash
kubectl port-forward -n monitoring svc/probara-frontend 3000:3000
kubectl port-forward -n monitoring svc/probara-api 8080:8080
kubectl port-forward -n monitoring svc/probara-status-page 8082:8080
```

`/api/*` requests go through the frontend server and are proxied to
`API_PROXY_TARGET` (set automatically by the chart to the API service
in-cluster).

## Environment Variables

When running services directly (outside Helm), these are the main environment
variables.

> Note: the code defaults for the check-job queue are `CHECK_JOBS` /
> `check.jobs`, while Compose and the Helm chart configure `check-jobs` /
> `check.job` on all services. Either pair works — but API, scheduler, and
> workers must all use the same values, or "run now" silently breaks.

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

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) for local setup, the build and test
commands per module, and the commit conventions.

Found a security issue? Please report it privately — see
[SECURITY.md](SECURITY.md). Do not open a public issue.

Licensed under AGPL-3.0; see [LICENSE](LICENSE).
