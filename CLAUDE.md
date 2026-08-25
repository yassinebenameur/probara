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
- `collector/` — OCB manifest for `probara-collector`, the minimal OTel
  Collector distribution that agent monitors install on hosts (built by
  `scripts/build-collector.sh` into `static/collector/`; no in-repo agent code)
- `scripts/kuma-export/` — standalone Go CLI that pulls monitors out of a live
  Uptime Kuma over its socket.io API and writes Kuma's own backup shape (Kuma
  2.0 removed the export button). Transport only — all translation lives in
  `api/internal/services/import/kuma.go`
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
- **Metric identity and display**: `shared/metricstore` owns canonical series
  keys (`name{k=v,…}`, sorted keys), attribute hashing, and the curated
  label/unit table for OTel metrics. Ingest dedup, `host_metric` alert
  identity (`alerts.metric_name`), notification wording, and status pages all
  go through it; `web/lib/metrics.ts` is its TS twin. Never invent a parallel
  series encoding.
- **Alert routing**: `shared/alertrouting` owns which channels a monitor's
  alerts reach — mode `custom`/`default`, active-channel filtering, and group
  roll-up suppression — as SQL fragments plus `Classify`. The alerter builds
  its dispatch queries from those fragments; the API builds the read-only
  `alert_routing` field and the dashboard's `unrouted_monitors` count from
  them, so "who gets notified" and "who we warn is unrouted" cannot disagree.
  `UnreachablePredicate` is the SQL twin of `Classify().Reachable`; change
  them together (`TestUnreachablePredicateMatchesClassify` pins the pair).
  Note the roll-up asymmetry it exposes: member suppression ignores the
  group's `enabled` flag while the group's own alert requires it, so a paused
  `group`-rollup group silences its whole membership. Reachability *reports*
  that (`group_rollup_paused`); dispatch still behaves that way.
- **Monitor state semantics**: `docs/state-semantics.md` (rules `S-*`).
  Changes to state transitions, quorum aggregation, freshness/absence,
  pause/maintenance handling, or uptime accounting must update the rule
  table in the same commit; tests cite rule IDs
  (`shared/monitorstate/spec_test.go`). New aggregation surfaces delegate to
  `shared/monitorstate`, never invent parallel state rules.

## Monitor import

`api/internal/services/import/service.go` parses a source into `parsedSource`
(rows + schema + warnings + skipped rows), then `ExecuteImport` creates
monitors. Two paths, chosen by whether `FieldMapping.Config` is set:

- **Raw path** (`useRawConfig`): rows carry an already-typed nested `config`
  object. Supports every registry type, resolves group members by name in a
  second pass, and validates through `validation.DefaultRegistry` before
  create. Both recognized schemas take this path.
- **Simple path**: generic CSV/JSON/YAML field mapping. Only http, ping, dns,
  grpc and group; everything else is skipped.

Adding a foreign source format means one adapter returning a `parsedSource`
(see `parsePortableExport` and `parseKumaExport`), hooked into `parseJSON` or
`parseYAML`. Build `ImportRow.Fields` **directly** — going through
`flattenMap` dot-flattens the nested `config` and silently drops you onto the
simple path. Records you refuse to translate go in `SkippedRows`, never as
rows: `detectAndSuggestTypes` defaults unknown types to `http` and the UI
auto-applies that suggestion, so an emitted row for an untranslatable monitor
becomes a silent bad import.

**Group membership is the junction table**, not `config.monitor_ids`.
`Monitor.MemberIDs`, group alert roll-up, and the dashboards all read
`monitor_groups`. The HTTP handler syncs it after create; the importer must
call `groupService.AddMonitorsToGroup` itself (it does — `syncGroupMembers`),
or imported groups come back empty. Any other path that creates groups through
`monitorService.CreateMonitor` directly inherits the same obligation.

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

- **Agent monitors are OTLP push**: hosts run `probara-collector` (or stock
  otelcol-contrib) exporting to `POST /api/v1/otlp/v1/metrics` with
  `Authorization: Bearer <tenant API key>` + `X-Probara-Agent-Id` (fallback:
  `probara.agent.id` resource attribute). Samples land in the metric store
  (`metric_series`/`metric_samples`, daily partitions, hourly rollups via the
  `metric_rollup_dirty` ledger); each accepted export records ONE success
  heartbeat via `monitorstate.Record` with **server-clock** `StartedAt`
  (S-O3). The legacy `POST /api/v1/agent/metrics` stays accepting (with
  Deprecation/Sunset headers) until the sunset date in
  `api/internal/handlers/agent/handler.go`, then becomes a 410 tombstone.
  Host alerting is `metric_rules` on the agent config (native units — ratio
  0-1 for `*.utilization`; validated in `AgentConfigValidator`), evaluated by
  the alerter against the store with a freshness bound.

- **Check-job queue**: `CHECK_JOB_STREAM`/`CHECK_JOB_SUBJECT` must be identical
  on API (run-now publisher), scheduler, and workers. Code defaults
  `CHECK_JOBS`/`check.jobs`; Compose and Helm ship `check-jobs`/`check.job` on
  all three. Changing one service alone silently breaks run-now.
- **`PROBARA_SECRETS_KEY`**: needed by api, scheduler (dispatch decrypt),
  worker, alerter. Compose passes it through from the shell env to all four;
  Helm renders it through `monitoring-platform.secretEnv`, guarded by
  `monitoring-platform.hasSecret` — the guard tests "literal **or** any
  existing-Secret ref", never the literal alone, or an external-secret install
  silently runs with encryption off. Rotation keys (`_V2`+) go through the
  chart's top-level `extraEnv`.
- **`SMTP_*` / `APP_BASE_URL`**: needed by alerter (alert delivery), worker
  (async dispatch), and **api** — `POST /alert-channels/{id}/test` runs the
  email plugin in the API process, so alerter-only SMTP yields channels that
  deliver alerts but fail every test with `mailer not configured`. Parsed once
  in `shared/config` (`loadSMTPConfig`, embedded `SMTPConfig`); the chart's
  top-level `smtp:` and `appBaseURL:` values render the env into all three
  workloads via the `smtpEnv` helper. Compose wires none of it.
  `SMTP_USE_TLS=true` is implicit TLS (port 465), never STARTTLS.
  `APP_BASE_URL` is the operator-UI origin and only adds the "open the
  monitor" button to alert email — unset omits the button, never a guessed
  host.
- **Helm `extraEnv`**: top-level (all workloads incl. migrations job) and
  `<service>.extraEnv`; `worker.extraEnv` also reaches location workers, and
  `worker.locations[]` entries can carry their own.
- **Helm secret sources**: every credential resolves per-field
  `<field>ExistingSecret` → chart-wide `secrets.existingSecret` → the
  chart-managed `<fullname>-secret`, all through
  `monitoring-platform.secretEnv` in `_helpers.tpl`. Never render a credential
  as a literal `value:` in a PodSpec and never hardcode the Secret name — the
  canonical keys (`postgres_url`, `nats_url`, `nats_platform_password`,
  `nats_location_auth_issuer_seed`, `admin_jwt_secret`, `probara_secrets_key`,
  `oidc_client_secret`) are the contract external secret managers fill.
  The NATS platform password reaches the embedded broker as a `--pass` arg
  substituted by Kubernetes (`$(NATS_PLATFORM_PASSWORD)`), keeping the
  ConfigMap secret-free; never move it into `nats.conf` via nats-server's own
  `$VAR` expansion, which re-parses the value as config (numeric, boolean-ish,
  or space/comma/brace passwords abort startup) and silently ignores the
  expansion if you quote the reference. Helm cannot read Secrets,
  so anything the chart *composes* from a credential (the Postgres DSN, the
  embedded NATS URL) needs the composed value externalized too — hence the
  paired `fail`s in `secret.yaml`.
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

- **Collector version pinning**: the OCB/component version is pinned in BOTH
  `collector/manifest.yaml` and `scripts/build-collector.sh` (`OCB_VERSION`),
  and surfaced as `CollectorVersion` in
  `api/internal/services/agent/collector_install.go` — bump all three
  together, and re-run
  `go test ./api/internal/services/agent/ -run TestGeneratedConfigValidates`
  after `make build-collector-static` (it runs the real binary's `validate`
  against the generated config).

- **Brand mark lives in four runtimes** and they must change together:
  `web/components/ui/BrandMark.tsx` (operator UI logo — sidebar, login,
  connect), `web/app/icon.svg` (dashboard favicon), `website/app/icon.svg`
  (landing/docs favicon), and an inline data-URI `<link rel="icon">` in
  `shared/statustemplate/default.gohtml` (status pages, monochrome variant
  that inverts with browser chrome). Same trace geometry, four copies —
  status pages get a data URI because the service has no static-asset route
  and pages render under arbitrary domains and path prefixes. Alert email is
  the deliberate exception: `shared/notifications/plugin/builtin/email/alert.gohtml`
  draws a bar trace out of table cells because Gmail drops `data:` image URIs
  and every client can block remote ones — a masthead that vanishes is worse
  than an approximation. Recolour it with the other four; do not give it an
  `<img>`.

- **Alert email rendering** lives entirely in
  `shared/notifications/plugin/builtin/email`: `view.go` builds one
  presentation model (`alertView`) that both `alert.gohtml` (HTML part) and
  `renderAlertText` (plain-text part) consume, so the two MIME parts of a
  message cannot describe different alerts. Add a new alert kind's wording in
  `summaryFor`/`toneFor`/`metricFor`, never in the template. `smtp.go`
  assembles `multipart/alternative` with base64 parts (long styled lines
  otherwise trip the SMTP 998-octet limit) and RFC 2047 headers.

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
