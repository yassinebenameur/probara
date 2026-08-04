# Probara — CLAUDE.md

Uptime/voice observability platform. Go microservices connected by NATS
JetStream and Postgres, with a Next.js app and a marketing/docs site.

## Standing policies (apply to every change)

1. **Docs follow code, same commit.** `website/lib/docs/pages/*.ts` documents
   real behavior, including known gaps and limitations. After any change,
   grep those pages for statements the change invalidates (fixed gaps, new
   config fields, changed defaults, new capabilities) and update them in the
   same commit. Fixing a documented gap without updating the doc is an
   incomplete change.
2. **CLAUDE.md follows the project, same commit.** If a change invalidates or
   extends anything in this file (layout, invariants, commands, gotchas),
   update this file in the same commit.

## Repo layout

- `api/` — REST API (chi), validation registry, monitor/import/auth services
- `scheduler/` — cron-style dispatch, JetStream stream provisioning, results ingest
- `worker/` — check execution (all monitor types), `dial_guard.go` SSRF policy
- `alerter/` — alert evaluation and notification delivery
- `status-page/` — public status pages (custom template engine)
- `shared/` — cross-service models, config, secrets, queue, AI provider
- `web/` — Next.js operator UI (`components/monitors/MonitorForm.tsx` is the type registry)
- `website/` — Next.js landing + docs site (flight-recorder design: orange accent, Archivo + Plex Mono)
- `helm/monitoring-platform/` — chart; `docker-compose.yml` — full local stack

## Single sources of truth — never duplicate these lists

- **Monitor types**: `api/internal/validation/registry.go` (`DefaultRegistry`).
  Import, preview, and anything type-driven must delegate to it
  (`DefaultRegistry.Has/Types`), never keep a parallel hardcoded list.
- **Active-check types** (timeout semantics): `validation.IsActiveCheckType`.
- **Monitor config secrets**: `shared/secrets/monitor_config.go`
  (`MonitorSecretFields` / `MonitorSecretMapFields`). One entry gives
  encryption at rest, `***` masking on reads, write-only merge on updates,
  and correct scheduler/worker/location handling — no per-type code.

## Adding a monitor type (checklist)

1. `MonitorType` constant in `api/internal/models/monitors.go` + config struct
   there and in `shared/models/check_job_payload.go` (two mirrors, keep in sync)
2. Validator in `api/internal/validation/` + register in `registry.go`
3. Checker in `worker/internal/worker/` + register in worker `registry.go` —
   any checker that dials must take `(blockPrivateIPs, allowedCIDRs)` and dial
   through `dialGuard` (SSRF policy)
4. Secret fields → `MonitorSecretFields`
5. UI: form component in `web/components/monitors/`, wire into
   `MonitorForm.tsx` (`MONITOR_TYPE_META` + dispatch), types in `web/lib/types.ts`
6. Docs: `website/lib/docs/pages/monitors.ts` (type table + protocol passage +
   secrets coverage) — import/export support is automatic via the registry

## Cross-service contracts

- **Check-job queue**: `CHECK_JOB_STREAM`/`CHECK_JOB_SUBJECT` must be identical
  on API (run-now publisher), scheduler, and workers. Code defaults
  `CHECK_JOBS`/`check.jobs`; Compose and Helm ship `check-jobs`/`check.job` on
  all three. Changing one service alone silently breaks run-now.
- **`PROBARA_SECRETS_KEY`**: needed by api, scheduler (dispatch decrypt),
  worker, alerter. Compose passes it through from the shell env to all four;
  Helm wires it via guarded `secretKeyRef` blocks. Rotation keys (`_V2`+) go
  through the chart's top-level `extraEnv`.
- **`SMTP_*`**: needed by alerter (alert delivery), worker (async dispatch),
  and **api** — `POST /alert-channels/{id}/test` runs the email plugin in the
  API process, so alerter-only SMTP yields channels that deliver alerts but
  fail every test with `mailer not configured`. Parsed once in
  `shared/config` (`loadSMTPConfig`, embedded `SMTPConfig`); the chart's
  top-level `smtp:` block renders the env into all three workloads. Compose
  wires none of it. `SMTP_USE_TLS=true` is implicit TLS (port 465), never
  STARTTLS.
- **Helm `extraEnv`**: top-level (all workloads incl. migrations job) and
  `<service>.extraEnv`; `worker.extraEnv` also reaches location workers, and
  `worker.locations[]` entries can carry their own.
- **Selected tenant (web)**: `lib/tenant.ts` owns storage +
  `TENANT_CHANGED_EVENT`; `components/providers/TenantProvider.tsx` is the
  React-side source of truth (`useSelectedTenantId`) and its `TenantScope`
  keys the page subtree on the tenant, so every page's mount-time fetch
  re-runs on a switch. Pages must not add their own `tenant-changed`
  listeners — only components *outside* that subtree (Sidebar,
  CurrentUserProvider, AlertStreamProvider) need one. `…/[id]` detail routes
  redirect to their list page on a switch (`/users/[id]` excepted: users are
  platform-scoped).

## Build, test, verify

- Go (per module: `api/`, `worker/`, `scheduler/`, `shared/`, `alerter/`):
  `go build ./...` and `go test ./...` from the module dir. Full api suite
  takes >2 min — run in background.
- Web/website: `npx tsc --noEmit` in `web/` or `website/`.
- Helm: `helm lint helm/monitoring-platform` and `helm template` (see gotcha).
- Compose: `docker compose config` validates env wiring.

## Local dev environment

- App services run as local processes (api :8080, scheduler :9091 metrics,
  worker :9092 metrics, web :3000); Compose provides infra only (postgres,
  nats, dex). Credentials: `admin` / `change-me`; default tenant ID
  `00000000-0000-0000-0000-000000000001`.
- API auth is cookie-based: POST `/api/v1/auth/login`, keep a cookie jar, and
  pass `tenant_id` as a query param on tenant-scoped routes.
- Locally running binaries are stale after code changes — verify new worker/
  api behavior through unit tests or restart the processes.

## Gotchas

- **Brand mark lives in four runtimes** and they must change together:
  `web/components/ui/BrandMark.tsx` (operator UI logo — sidebar, login,
  connect), `web/app/icon.svg` (dashboard favicon), `website/app/icon.svg`
  (landing/docs favicon), and an inline data-URI `<link rel="icon">` in
  `shared/statustemplate/default.gohtml` (status pages, monochrome variant
  that inverts with browser chrome). Same trace geometry, four copies —
  status pages get a data URI because the service has no static-asset route
  and pages render under arbitrary domains and path prefixes.

- **rtk output filter** (user-global CLAUDE.md tool) truncates long command
  output in pipes — `helm template`, large `curl` responses. Use
  `rtk proxy <cmd>` for raw output or write to a file and read that.
- `expected_status` etc. reuse `HTTPStatus` fields for non-HTTP protocols
  (SIP status codes ride in `http_status`).
- SIP dev against the host: `SIP_LOCALHOST_AS_HOST_GATEWAY=true` rewrites
  loopback targets to `host.docker.internal` (compose worker only).
- `alert_policies` are retired (API returns 410); workspace alert config
  lives on tenants via `/notification-settings`.
- **Web overlays must portal**: pages wrap content in `space-y-*`, which puts a
  `margin-top` on a `fixed inset-0` sibling, so the backdrop stops covering the
  viewport (and `main`'s `overflow-x-clip` can clip it). Wrap modal roots in
  `components/ui/ModalPortal.tsx`. Modals still rendered inline elsewhere carry
  this bug.
- Commit style: conventional commits (`fix(scope):`, `feat(scope):`) with a
  body explaining root cause and verification.
