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
- `worker/` — check execution (all monitor types); checkers dial through
  `dialGuard` (`internal/worker/dial_guard.go`, an alias of `shared/netguard`)
- `alerter/` — alert evaluation and notification delivery
- `status-page/` — public status pages (custom template engine)
- `shared/` — cross-service models, config, secrets, queue, AI provider
- `shared/db/migrations/` — golang-migrate pairs `NNNNNN_name.{up,down}.sql`
  (take the next free number), applied by `cmd/migrate` — from the repo root
  it needs `MIGRATIONS_PATH=./shared/db/migrations`; `make migrate` runs the
  Compose job, Helm a post-install/upgrade hook Job
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
  The merge contract per secret field is: absent → keep stored, `***` → keep
  stored, `""` → clear, anything else → replace (map fields keep the stored
  map only when the whole field is absent; a submitted map is edited per key).
  So a form that drops a stored secret on purpose — a Clear button, switching
  a database monitor between connection-string and discrete-field mode — must
  submit `""`, never omit the field.
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
  **Dependency suppression** (S-M4) is the per-alert rule in the same package:
  `SuppressedByDependencyPredicate(al, m, te)` is what the alerter's
  `dispatchOpenAlerts` excludes on **and** what the API renders as
  `suppression_reason` / the dashboard's `suppressed_alerts` — always use the
  fragment, never re-derive it. The grace clock is `alerts.root_cause_cleared_at`,
  stamped by `annotateOpenAlertRootCauses` when the annotation clears and reset
  when one is set again; `DispatchEligibleSinceExpr` is what escalation delays
  count from. `ImpactedMonitorsSubquery` lists only *suppressed* downstreams, so
  with the policy off (the default) existing notifications do not change.
- **Status page settings live in THREE mirrors**, all of which must change
  together for a new setting to survive a round trip:
  `api/internal/models/statuspages.go` (`StatusPageSettings`, the wire type),
  `api/internal/services/statuspages/service.go`
  (`statusPageSettingsStored`/`applyPatch`/`toAPI` — the **write** path), and
  `status-page/internal/statuspage/service.go` (the **read** path the public
  renderer uses). Plus `web/lib/types.ts` for the UI. The API's stored struct
  re-serializes only the fields it knows, so a setting added to the model but
  not to `applyPatch`/`toAPI` is accepted by the endpoint, silently dropped on
  write, and never reaches the page — no error anywhere.
- **Status page push notifications**: `monitor_state_intervals` (migration
  000082) is the trigger source, not `statuspage.updates`. The NATS subject
  cannot carry it: push/agent/OTLP monitors discard the `Transition` from
  `monitorstate.Record` and publish `check_result` unconditionally
  (`api/internal/services/{push,agent,otlp}/service.go`), so the watchdog
  announces their down and nothing announces their up; and the subject is
  fire-and-forget core NATS with no redelivery. Anything that changes how
  intervals are written now has a real-time consumer. The notify rule lives
  twice — `classifyPushTransition` (Go) and the CASE in
  `reconcileNotifications` (SQL); change them together. Page membership for
  fan-out uses the **rendered** set (`status_page_section_monitors`, legacy
  `status_page_monitors`), never `statusPageSlugQuery`, which resolves upward
  through `monitor_groups` and would push a member's name to a page that only
  lists its parent group.
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

1. `MonitorType` constant in `api/internal/models/monitors.go`; config struct in
   `shared/models/` (one file per newer type, e.g. `prometheus.go`; older types
   sit in `check_job_payload.go`). The API validator and the worker both consume
   the shared struct, so put `Validate()` on it rather than duplicating rules
2. Validator in `api/internal/validation/` + register in `registry.go`
3. Checker in `worker/internal/worker/` + register in worker `registry.go` —
   any checker that dials must take `(blockPrivateIPs, allowedCIDRs)` and dial
   through `dialGuard` (SSRF policy)
4. Secret fields → `MonitorSecretFields`
5. UI: form component in `web/components/monitors/`, wire into
   `MonitorForm.tsx` (`MONITOR_TYPE_META` + dispatch), types in `web/lib/types.ts`
6. Docs: `website/lib/docs/pages/monitors.ts` (type table + protocol passage +
   secrets coverage) — import/export support is automatic via the registry
7. Display-only mirrors that the registry cannot drive: `typeLabel`/`typeBadge`
   in `status-page/internal/statuspage/render.go` (badge ≤ 5 chars),
   `SUPPORTED_TYPES` in `web/app/monitors/import/page.tsx`, `TYPE_LABELS` in
   `web/app/monitors/page.tsx`, and the type count + `monitorGroups` on the
   landing page (`website/app/page.tsx`, `website/app/docs/page.tsx`, README)

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
  `APP_BASE_URL` is the operator-UI origin and only adds "open the monitor"
  deep links to notifications (every channel, via `plugin.Configure`) —
  unset omits them, never a guessed host. The same three workloads read
  `NOTIFICATION_BLOCK_PRIVATE_IPS` (default **true**) /
  `NOTIFICATION_ALLOWED_CIDRS` (`loadNotificationEgressConfig`); each main
  passes both to `plugin.Configure` next to `email.SetMailer`. A binary that
  skips `plugin.Configure` fails closed (private egress blocked, no links).
  This policy is independent of the worker's `HTTP_BLOCK_PRIVATE_IPS`
  (default false); both use `shared/netguard`, the single SSRF range table
  and resolve-then-dial guard (the worker's `dialGuard` is an alias of it).
- **`STATUS_PAGE_VAPID_*`**: status-page only, and both key halves are
  required or visitor notifications are off (no control rendered, push routes
  404). The **public** key is deliberately a literal `value:` in the PodSpec —
  the one documented exception to the never-a-literal rule, because it ships
  inside every rendered page as the `applicationServerKey`. The private key
  goes through `monitoring-platform.secretEnv` under canonical key
  `status_page_vapid_private_key`. Never generate the pair at startup: the
  deployment runs 2 replicas, so each would mint a different pair and a
  subscription created against one would be unusable by another. Rotating the
  pair invalidates **every** stored subscription. Mint with
  `go run ./cmd/admin/gen_vapid_keys`.
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

- Go is a single module at the repo root (`go.mod`) — run everything from
  there: `go build ./...`, then `go test` scoped to what you touched
  (`go test ./api/...`, `go test ./shared/notifications/...`). `make test`
  runs the whole module with `-race` (skipping `scripts/`). The full api
  suite takes >2 min — run it in the background.
- Integration tests (`testutil.SetupPostgresDB`) use testcontainers by
  default; without a Docker daemon set
  `PROBARA_TEST_POSTGRES_DSN=postgres://user@host:port/postgres?sslmode=disable`
  (role needs CREATEDB) and each test gets a scratch database on that server.
- Web/website: `npx tsc --noEmit` in `web/` or `website/`.
- Helm: `helm lint helm/monitoring-platform` and
  `helm template helm/monitoring-platform` (long output — write it to a file).
- Compose: `docker compose config` validates env wiring.

## Local dev environment

- `make start-all-local` is the usual stack: Compose runs only postgres
  (`probara`/`probara`, :5432) and nats (:4222, monitoring :8222); the
  target applies migrations, builds the collector binaries, then runs the Go
  services as local processes (binaries in `/tmp/probara-bin`, logs in
  `/tmp/probara-<service>.log`) and the UI as `next dev`. HTTP/metrics
  ports: api 8080/9090, scheduler 8081/9091, status-page 8082/9093, worker
  8083/9092, alerter 8084/9094; web 3000. Stop and restart with the matching
  `stop-all-local` / `restart-all-local`. `make start-all` / `stop-all`
  instead run the backend in Compose (`docker-compose.yml` defines every
  service). Dex (test OIDC IdP, :5556) is opt-in in either mode:
  `docker compose --profile sso up -d dex`.
- The start script refuses any Go except the minor on `go.mod`'s `go` line
  (1.26.x). With a newer local Go, prefix the command:
  `GOTOOLCHAIN=go1.26.7 make start-all-local`.
- Credentials: `admin` / `change-me`; default tenant ID
  `00000000-0000-0000-0000-000000000001`.
- API auth is cookie-based: POST `/api/v1/auth/login`, keep a cookie jar, and
  pass `tenant_id` as a query param on tenant-scoped routes.
- Locally running binaries are stale after code changes — verify new worker/
  api behavior through unit tests, or restart the processes
  (`scripts/start-local-services.sh` rebuilds and restarts just the Go
  services).

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
  status pages get a data URI because pages render under arbitrary domains
  and path prefixes (the service now has exactly one static route — the push
  service worker, which must be a real URL for its scope to mean anything —
  but every other asset stays inlined). Alert email is
  the deliberate exception: `shared/notifications/plugin/builtin/email/alert.gohtml`
  draws a bar trace out of table cells because Gmail drops `data:` image URIs
  and every client can block remote ones — a masthead that vanishes is worse
  than an approximation. Recolour it with the other four; do not give it an
  `<img>`.

- **Notification sends go through `deliverNotification`** in
  `alerter/internal/alerter/notification_claim.go`, never `sendFunc` directly.
  It claims the `(alert, channel, event)` slot in `alert_notification_states`
  inside a transaction, sends, then commits, which is what makes
  `alerter.replicas: 2` safe: replicas have no leader election and their
  tickers stay in phase, so a read-then-send-then-upsert path sends every
  DOWN and reminder twice. The claim predicates in `claimNotificationTx` and
  the in-memory pre-filter `shouldSendNotification` encode the same due rules;
  change them together. A lost `resolveAlert` race returns
  `errAlertAlreadyResolved` and the loser skips notifications, so it is not
  an error to log.

- **Alert wording lives in `shared/notifications/present`**, for every
  channel. `present.Build` turns an event into one `Message` (label, summary,
  metric, facts, deep link, tone); plugins reach it via
  `DispatchRequest.View()` and own layout only. Add a new alert kind's or
  event type's wording in `labelFor`/`summaryFor`/`toneFor`/`metricFor`
  there, never in a plugin or template. Email wraps it (`email/view.go`:
  `alertView` = `present.Message` + palette) so `alert.gohtml` (HTML part)
  and `renderAlertText` (plain-text part) still render from one model.
  `smtp.go` assembles `multipart/alternative` with base64 parts (long styled
  lines otherwise trip the SMTP 998-octet limit) and RFC 2047 headers.

- **Notification plugins** (`shared/notifications/plugin/builtin/<type>/`,
  registered in `builtin.go`) must: build their client with
  `plugin.NewHTTPClient` (egress-guarded, no redirects) and send through
  `plugin.PostJSON`/`plugin.Post` (classifies responses; strips the URL from
  transport errors, because webhook paths and bot tokens are credentials);
  mark permanent failures with `plugin.Permanent` (the worker then stops
  redelivering and the sync alerter keeps the claim instead of re-failing
  every cycle); validate hosts with `plugin.HostMatches` (label-boundary,
  never `strings.Contains`/`HasSuffix`); and treat `req.Test` as a real send
  that must not leave provider state behind. Paging plugins advertise
  `plugin.CapabilityAcknowledge`: the alerter's `dispatchAcknowledgements`
  sends them `acknowledged` once per paged channel
  (`alert_notification_states.acknowledged_sent_at`, a separate column so the
  created/reminder/resolved machine in `last_event_type` is untouched), and
  they skip reminders. Channel updates run the plugin's `Validate` on the
  merged config (`alertchannels.Service.validateMerged`) — create-time host
  checks are otherwise bypassable by editing. The async worker
  (`eventSuperseded`) reads the alert `FOR SHARE` in a transaction held
  through `Send`, and drops any non-resolve envelope whose alert has since
  resolved. The lock is the ordering guarantee: resolution is an `UPDATE` of
  that row, so it waits for an in-flight trigger (bounded by
  `DispatchTimeout`), and the resolve is only published after it commits —
  without it a trigger could reach PagerDuty after the resolve and leave the
  incident open forever. Do not move `Send` out of that transaction.
  Acknowledgement has the mirror race: the alerter publishes `acknowledged`
  once the created slot is *claimed*, not delivered, so an ack can overtake a
  trigger sitting on a retry backoff and be dropped by the provider. The
  worker therefore follows a `created` it delivers to an ack-capable channel
  of an `acknowledged` alert with an `acknowledged` send (`ackOvertaken`).
  The alerter's own claim lock in `deliverNotification` is `FOR SHARE` for
  the same reason as the worker's: it still blocks resolution, while an
  exclusive lock would queue every claim behind in-flight async sends.
  Channel secrets merge differently from monitor secrets
  (`secrets.MergePreserveSecrets`): absent, `""` and `***` all **keep** the
  stored value (the form submits every secret input blank on edit), and
  `null` **clears** it — SchemaForm's "Remove stored value", offered only for
  optional secret fields.
  Multi-recipient plugins must flatten a mixed error before returning it —
  `plugin.IsPermanent` walks `errors.Join` children, so one permanent child
  would stop the retry for the others — and carry the longest
  `plugin.RetryAfterDelay` onto the flattened error.

- **status-page is read-only except for push.** The `Service` takes a
  `db.Querier` and `service.go` stubs `ExecContext` into a refusal so stray
  writes cannot compile. `pushStore` (`push_store.go`) is the single
  deliberate exception and the only holder of a `*sql.DB` in the package; it
  is constructed only when VAPID keys are set, so an unconfigured deployment
  stays strictly read-only. Do not widen `Querier` to add a write.

- **`Subscriber.handleEvent` early-returns** when no SSE client is connected
  and the render cache is empty. That is exactly the situation browser
  notifications exist for, so the push wake-up sits **above** that guard.
  Anything else hooked in below it will never fire for the case it was built
  to serve.

- `expected_status` etc. reuse `HTTPStatus` fields for non-HTTP protocols
  (SIP status codes ride in `http_status`).
- SIP dev against the host: `SIP_LOCALHOST_AS_HOST_GATEWAY=true` rewrites
  loopback targets to `host.docker.internal` (compose worker only).
- `alert_policies` are retired (API returns 410); workspace alert config
  lives on tenants via `/notification-settings`.
- **Folded web routes stay as redirects**: Mesh is a tab of Locations and
  the alert channel list is a tab of Settings (`?tab=`, via
  `components/ui/Tabs.tsx` `useUrlTab`). `/mesh` and `/alert-channels` are
  kept as redirects — mesh-edge notifications deep-link to `/mesh`
  (`shared/notifications/present`) — and `/alert-channels/{new,catalog,[id]}`
  remain real routes. Link to the tabs, not the old list pages.
- **Row actions** in tables and lists use `components/ui/IconButton.tsx`
  (icon only, named by its tooltip, `danger` last) for up to three
  actions, and `components/ui/RowMenu.tsx` (⋮, position:fixed, flips up)
  beyond that. Don't put text buttons in rows.
- **Web overlays must portal**: pages wrap content in `space-y-*`, which puts a
  `margin-top` on a `fixed inset-0` sibling, so the backdrop stops covering the
  viewport (and `main`'s `overflow-x-clip` can clip it). Wrap modal roots in
  `components/ui/ModalPortal.tsx`. Modals still rendered inline elsewhere carry
  this bug.
- Commit style: conventional commits (`fix(scope):`, `feat(scope):`) with a
  body explaining root cause and verification.
