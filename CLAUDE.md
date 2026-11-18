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
   update this file in the same commit. It is the only agent guide —
   `AGENTS.md` just points here, so never add guidance there. Keep an entry
   to the rule and where it lives; the reasoning belongs in a comment next to
   the code, where the next person editing it will see it.

## Repo layout

- `api/` — REST API (chi), validation registry, monitor/import/auth services,
  the MCP server (`internal/mcp/`) and its OAuth authorization server
  (`internal/services/mcpoauth/`, `internal/handlers/oauth/`)
- `scheduler/` — cron-style dispatch, JetStream stream provisioning, results
  ingest (`internal/ingest/`)
- `worker/` — check execution (all monitor types); checkers dial through
  `dialGuard` (`internal/worker/dial_guard.go`, an alias of `shared/netguard`),
  except `synthetic_browser`, whose Chromium connects on its own and is
  documented as outside the policy (`website/lib/docs/pages/security.ts`);
  SIP call-quality math (RTP and RTCP codecs, RFC 3550 statistics, the G.107
  E-model, G.711 frames) is the I/O-free `internal/rtp`
- `alerter/` — alert evaluation and notification delivery
- `status-page/` — public status pages (custom template engine)
- `shared/` — cross-service models, config, secrets, queue, local AI evidence/decisions
- `shared/db/migrations/` — golang-migrate pairs `NNNNNN_name.{up,down}.sql`
  (take the next free number; numbering must stay contiguous — a gap filled
  later is silently skipped on upgraded installs, and `TestMigrationNumbering`
  (`shared/db/migrations_numbering_test.go`) fails on a duplicate number, an
  unpaired file, or a gap not in `knownMigrationGaps`), applied by
  `cmd/migrate` — from the repo root
  it needs `MIGRATIONS_PATH=./shared/db/migrations`; `make migrate` runs the
  Compose job, Helm a `post-install,pre-upgrade` hook Job
  (`job-migrations.yaml`, with its own hook Secret for a chart-composed DSN,
  since the release Secret is not updated yet), so new pods never start on
  an old schema and code may rely on its release's migrations; Compose
  services wait for `migrations` (`service_completed_successfully`). Old
  pods run briefly on the new schema: keep migrations additive. The API's
  startup migration is a safety net only
- `cmd/` — `migrate`, `discovery-collector` (the in-cluster Kubernetes
  inventory collector, own image), plus one-off admin tools in `cmd/admin/`
  (`cmd/admin` itself upserts a superadmin, like the first-user form; VAPID
  keypair minting, secret encryption backfills)
- `collector/` — OCB manifest for `probara-collector`, the minimal OTel
  Collector distribution that agent monitors install on hosts (built by
  `scripts/build-collector.sh` into `static/collector/`; no in-repo agent code)
- `scripts/kuma-export/` — standalone Go CLI that pulls monitors out of a live
  Uptime Kuma over its socket.io API and writes Kuma's own backup shape (Kuma
  2.0 removed the export button). Transport only — all translation lives in
  `api/internal/services/import/kuma.go`
- `scripts/sipp/` — digest-pinned SIPp image that
  `worker/internal/worker/sip_invite_integration_test.go` builds and runs its
  `testdata/sipp/` UAS scenarios in (bump base digest and package together)
- `ai-service/` — Python CPU inference service for local AI (FastAPI,
  `probara_ai/`: `server.py`, `runtime.py` with `verify_artifact`, `gate.py`
  the release predicate, `contract.py` the evidence contract); committed
  head-only artifacts in `artifacts/<version>/`, training/validation scripts
  in `training/`, hash-locked `requirements*.lock`. `docs/local-ai.md` is its
  guide, `docs/local-ai-validation.md` the current model status
- `scripts/seed-demo/` — dev-only Go tool that seeds the "Northwind" demo
  tenant (structure via the API, history via SQL) and keeps it live with mock
  targets (`targets` subcommand); see its README
- `web/` — Next.js operator UI (`components/monitors/MonitorForm.tsx` is the type registry;
  "instrument glass" design, surface contract at the top of `app/globals.css`)
- `website/` — Next.js landing + docs site (flight-recorder design: orange accent, Archivo + Plex Mono)
- `docs/` — `architecture.md` (see below), `state-semantics.md`,
  `auth-and-rbac.md`, `location-worker-auth.md`, `correctness-notes.md`,
  `testing.md` (test suites and CI jobs), `monitoring-discovery.md`
- `helm/monitoring-platform/` — chart; `docker-compose.yml` — full local stack;
  `infra/` — dev infra config (dex for the SSO profile)

## Service boundaries

`docs/architecture.md` is the service map: responsibilities, data access,
NATS streams and subjects with their defaults, the alert lifecycle. Update it
when a boundary or messaging contract moves. The rules that are easy to break:

- The scheduler dispatches and ingests; it never executes checks or
  evaluates alerts. `scheduler/internal/ingest/` owns result persistence and
  state advancement.
- Check execution needs NATS only, never Postgres — private-location workers
  get neither the database nor the platform key. Only the worker's optional
  notification/AI consumers and the AI triage relay touch the database.
- Local AI decision calls come only from the api and DB-connected workers —
  never from location workers, the scheduler or the alerter. In
  `worker/cmd/worker/main.go` the AI_RCA consumer needs `dbClient != nil`;
  the triage relay and consumer also need `LocationID == ""`. The relay
  (`worker/internal/airca/relay.go`) reads/writes Postgres and publishes
  `AI_TRIAGE`; the triage consumer (`triage.go`) writes only `alert_triage`.
- **Alert triage never changes paging.** `shared/alertrouting`, suppression,
  severity and the created/reminder/acknowledged/resolved events never read
  `alert_triage`, and triage never writes an alert. Its one reader outside the
  API's `triageview` is the alerter's opt-in `dispatchSuspectedCauses`
  (`alerter/internal/alerter/suspected_cause.go`): one `suspected_cause`
  follow-up per already-paged, still-routed channel, claimed on
  `alert_notification_states.suspected_cause_sent_at`, muted by the same
  `openAlertDispatchablePredicate` as reminders, on its own time budget
  ahead of the recovery sweep, with a round-robin batch so one hanging
  provider cannot hold the others back. Cause predicates (`ai.IsNamedCause`,
  `ai.IsConfident`, `ai.RCAInsufficientEvidence`) live in `shared/ai`; reuse
  them. SLA reports (`api/internal/services/slas/causes.go`) also read
  triage, only through triageview's `CauseJoin`/`CauseColumns`/
  `ClaimableExpr`/`EffectiveCause` (label over hypothesis). Never let triage
  gate or alter a page.
- Durable work goes through JetStream (`shared/queue`). Core NATS
  (`shared/statusupdates`, `statuspage.updates`) is live fan-out only;
  nothing persistent may depend on a core-NATS subscriber.
- Session advisory locks are acquired and released on one pinned connection
  (see `scheduler/internal/scheduler/maintenance_lock.go`), never through
  separate pool calls.
- Every service keeps `/healthz`, `/readyz` and `/metrics`. The Go mains get
  them, the shutdown-signal wait, the at-rest encryptor and the notification
  runtime from `shared/bootstrap`; add shared startup mechanics there, never
  as another copy in a main (the copies had drifted: one liveness probe
  ignored a dead NATS connection). Worker side consumers (notifications,
  AI-RCA, triage) run under `airca.RunWithRestart` so one that dies restarts
  instead of leaving a healthy-looking pod. Keep the probes intact
  when touching startup or server wiring (ai-service's `/readyz` needs the
  bearer token, so the chart's readiness probe is an exec; its `/healthz`
  turns 503 once a batch outruns `LOCAL_AI_MAX_INFERENCE_SECONDS`, which is
  what restarts a wedged pod).
- Preserve tenant isolation on every tenant-scoped query, and put logic two
  services need in `shared/` rather than copying it.
- **Deleted workspaces are refused at the shared lookup.** Workspace delete
  is a soft delete (`tenants.deleted_at`, superadmins only,
  `api/internal/services/tenants/workspaces.go`); the API never runs
  `DELETE FROM tenants`. Every query that resolves a credential or tenant, or
  iterates tenant data in a background loop, adds
  `shared/tenancy.LiveTenantSQL` (or `DeletedTenantSQL` where an unknown ID
  must stay distinct), filtered before any `LIMIT`; the alerter's
  notification claim and the check-result insert (`monitorstate.insertResult`)
  are the last gates. The fragments name `tenants.deleted_at` so the planner
  has statistics: migration 000115 must run before the binaries serve
  (upgrade notes in `website/lib/docs/pages/deployment.ts`). The one tolerant
  read is the auth `identityQuery` (`to_jsonb`), because its missing-schema
  fallback treats every admin as a superadmin; never add another. It answers
  `404 workspace_not_found` for any workspace the caller cannot open
  (deleted, purged, unknown, or a member's non-membership). The scheduler's
  workspace purge (`scheduler/internal/scheduler/purger_tenants.go`, own
  advisory lock) removes the data, blanking secret columns first
  (`workspaceScrubPlan`): a new table holding tenant data (a `tenant_id`
  column or a foreign key into such a table) goes into `workspacePurgePlan`
  (before the tables it references) or `workspacePurgeExempt`, gets a row in
  the test's `workspaceFixtureSQL`, and any secret column gets a scrub step
  (`TestWorkspacePurgePlanCoversEveryTenantTable`,
  `TestWorkspacePurge_RemovesEveryRowAndSparesOtherWorkspaces`,
  `TestWorkspacePurge_ScrubsSecretsFirst`).
- API routes are registered by the `mount*Routes` functions in
  `api/internal/api/routes_*.go`; `NewServer` (`server.go`) builds the
  services and handlers and calls them in order.
- API handlers take their plumbing from `api/internal/httpx` (`TenantID`,
  `WriteJSON`, `Page`/`PageWithLimits`); services clamp with
  `api/internal/pagination`. No private copies. The plaintext protocol
  handlers (`agent`, `push`, `otlp`, `metricsquery`) keep their own response
  bodies.
- Services return exported sentinel errors (shared ones in
  `api/internal/apperr`) and handlers map them with `errors.Is` — never add
  a comparison on error text, and keep a sentinel's message stable: it
  reaches API clients. Three validation-message switches predate this and
  are the remaining debt (`handlers/incidents`, `handlers/maintenancewindows`,
  plus the plaintext `handlers/push`); convert them to typed validation
  errors rather than adding a fourth. Postgres error codes go through
  `shared/db/pgerr`.

## Single sources of truth — never duplicate these lists

- **Monitor types**: `api/internal/validation/registry.go` (`DefaultRegistry`).
  Import, preview, and anything type-driven must delegate to it
  (`DefaultRegistry.Has/Types`), never keep a parallel hardcoded list.
- **Active-check types** (timeout semantics): `validation.IsActiveCheckType`.
  Every registered type is active xor passive
  (`TestRegistryTypesArePartitionedActiveOrPassive`).
- **Passive types** (never dispatched, refused by run-now, skipped by the
  worker): `shared/models.PassiveMonitorTypes`; the monitor page's
  `PASSIVE_TYPES` is its UI mirror.
- **Server-chosen destinations**: `netguard.Policy.AllowsServerChosen`
  (`shared/netguard`) vets every address a remote server chose rather than
  the operator (SDP media, latch targets, RTCP); never send to one without
  it. Its extra ranges (`0.0.0.0/8`, NAT64/6to4 embedded IPv4, Teredo,
  documentation) stay out of the shared `privateCIDRs` table.
- **Monitor config secrets**: `shared/secrets/monitor_config.go`
  (`MonitorSecretFields` / `MonitorSecretMapFields`). One entry gives
  encryption at rest, `***` masking on reads, write-only merge on updates,
  and correct scheduler/worker/location handling — no per-type code.
  The merge contract per secret field is: absent → keep stored, `***` → keep
  stored, `""` → clear, anything else → replace (map fields keep the stored
  map only when the whole field is absent; a submitted map is edited per key).
  So a form that drops a stored secret on purpose — a Clear button, switching
  a database monitor between connection-string and discrete-field mode,
  turning a database monitor's TLS off (its client key) — must
  submit `""`, never omit the field. On create there is nothing to keep, so
  `***` is dropped (`TestCreateMonitor_NeverPersistsAMaskedSecret`); the UI
  clone strips masks for every type first (`stripMaskedSecrets` in
  `web/lib/monitor-clone.ts`), and a form marks a masked value "stored" only
  in edit mode.
  Request credentials: http `secret_headers` + `basic_auth_password`, grpc
  `secret_metadata`, synthetic_api/synthetic_browser `secret_variables`. A
  secret nested below the top level (a step header, a fill value) is a
  `{{name}}` reference to `secret_variables`, never a nested registry path;
  the worker redacts those values from results (`secretRedactor`,
  `worker/internal/worker/secret_redact.go`) and keeps credentials on the
  origin across redirects (`redirectGuard`). Every write moves
  credential-named headers, metadata and synthetic variables into the secret
  maps (`secrets.NormalizeSensitiveHeaders`, after the merge in
  `prepareConfigForWrite`, which then refuses a merged config that conflicts
  with a kept secret); the `cmd/admin/migrate_sensitive_headers` backfill
  calls the same function, after renaming miscased keys stored before
  validation refused them. Two name rules, each one fixture in
  `shared/secrets/testdata/`: headers/metadata use `IsSensitiveHeaderName`
  (any name containing token/secret/…; TS twin in
  `web/lib/monitor-form/http.ts`, also called by `scripts/kuma-export`),
  synthetic variables use `IsSensitiveVariableName` (last name segment only,
  so `token_url` stays plain; TS twin in `web/lib/monitor-form/synthetic.ts`).
  Both twins are tested equal to their fixture. A variable move never creates
  a secret name validation refuses (`movableSensitiveVariables`). Skip
  secret work for a config with no secret key via `secrets.HasSecretValues`
  (byte-level). Keys match exactly while config structs decode
  case-insensitively, so `ValidatorRegistry.Validate` refuses a miscapitalized
  secret key or plain key on a `secrets.PlainCredentialPaths` path
  (`validateSecretKeySpelling`); add a path there when the write path reads a
  new plain key.
- **Channel config secrets** are the plugin manifest's `secret` fields, and
  they merge differently (`secrets.MergePreserveSecrets`): absent, `""` and
  `***` all **keep** the stored value (the channel form submits every secret
  input blank on edit), and `null` **clears** it — SchemaForm's "Remove
  stored value", offered only for optional secret fields.
- **Certificate location in `metrics_data`**: `shared/tlscert` owns the
  certificate snapshot (`Info`, built by `FromConnectionState`) and where it
  sits (`MetricsPaths`: `http.tls`, `sip.tls`, `tcp.tls`). The alerter's `tls_expiry`
  reads through `SQLExpr`, the status page and AI evidence through
  `Extract`; `TestSQLExprMatchesExtract` pins the pair. A new TLS-capable
  type adds its path there, never a hard-coded `#> '{x,tls}'`. The expiry
  that counts is always the chain's earliest (leaf + presented
  intermediates, never a trust-store root; `chainOf`): `Info.Expiry` in Go,
  `ExpirySQL` in SQL (`TestExpirySQLMatchesExpiry`), never `not_after` alone.
- **Domain registration snapshot**: `shared/domainexpiry` owns the domain
  check's `metrics_data.domain` (`Info`, `MetricsPaths`, `SQLExpr`/
  `ExpiresAtSQL`/`UnknownReasonSQL`, `Extract`; its own
  `TestSQLExprMatchesExtract`), the lookups (RDAP via the embedded IANA
  bootstrap in `Directory`, WHOIS fallback) and `RegistrableDomain`. Lookups
  dial only through the worker's `domainDialGuard`, which blocks private
  destinations whatever `HTTP_BLOCK_PRIVATE_IPS` says (servers come from
  bootstrap data and referrals); keep redirect/referral targets validated
  there. An undetermined expiry (including a throttled or refused lookup)
  is a `success` with `unknown_reason`, never a failure; the alerter's
  `evaluateDomainExpiry` (`domain_expiry.go`) turns a persistent one into a
  valueless `domain_expiry` alert (not for `PublishesNoExpiry` TLDs), and
  resolves-and-reopens when the alert switches between valueless and dated.
  One registry answer never pages: `ValidateDomainMonitorRules`
  (`shared/models/domain.go`) holds domain monitors to a threshold of at
  least 2 and at most one location, and an RDAP 404 yields to a WHOIS that
  holds the name. Lookups are throttled per registry host in the worker
  (`throttle.go`), and `POST /monitors/test` of this type needs write
  access (`handlers/monitors/checks.go`, beside the saved-secret gate).
- **Metric identity and display**: `shared/metricstore` owns canonical series
  keys (`name{k=v,…}`, sorted keys), attribute hashing, and the curated
  label/unit table for OTel metrics. Ingest dedup, `host_metric` alert
  identity (`alerts.metric_name`), notification wording, and status pages all
  go through it; `web/lib/metrics.ts` is its TS twin. Never invent a parallel
  series encoding.
- **Alert routing**: `shared/alertrouting` owns which channels a monitor's
  alerts reach — mode `custom`/`default`, active-channel filtering, group
  roll-up suppression — and dependency suppression (S-M4,
  `SuppressedByDependencyPredicate`), as SQL fragments plus `Classify`. The
  alerter's dispatch queries, the API's `alert_routing`
  and `suppression_reason` fields, and the dashboard's `unrouted_monitors` /
  `suppressed_alerts` counts are all built from those fragments, so "who gets
  notified" and "what the UI reports" cannot disagree — always use the
  fragment, never re-derive a rule. `UnreachablePredicate` is the SQL twin of
  `Classify().Reachable`; change them together
  (`TestUnreachablePredicateMatchesClassify` pins the pair).
  `RoutesToChannelPredicate` is `TargetsSQL`'s own union correlated per row,
  for batch queries that must drop unrouted pairs before a `LIMIT`
  (`TestRoutesToChannelPredicateMatchesTargetsSQL`). Escalation delays
  count from `DispatchEligibleSinceExpr` (the end of a cleared root cause's
  grace period, else `triggered_at`). Known asymmetry: member suppression
  ignores the group's `enabled` flag, so a paused `group`-rollup group
  silences its whole membership — reachability reports it
  (`group_rollup_paused`), dispatch still does it. The package doc, the
  fragments' comments and `annotateOpenAlertRootCauses` (the grace clock)
  cover the rest. Monitor-less alerts (mesh edges, `location_offline`) route
  through `TenantTargetsSQL`, the tenant defaults without a monitor
  (`TestTenantTargetsSQLMatchesDefaults`), and a mesh edge with a location
  that stopped reporting dispatches nothing
  (`SuppressedByOfflineLocationPredicate`, `suppression_reason`
  `location_offline`).
- **Status page settings live in THREE mirrors**, all of which must change
  together for a new setting to survive a round trip:
  `api/internal/models/statuspages.go` (`StatusPageSettings`, the wire type),
  `api/internal/services/statuspages/service.go`
  (`statusPageSettingsStored`/`applyPatch`/`toAPI` — the **write** path), and
  `status-page/internal/statuspage/service_settings.go` (the **read** path the public
  renderer uses). Plus `web/lib/types/status-pages.ts` for the UI. The API's stored struct
  re-serializes only the fields it knows, so a setting added to the model but
  not to `applyPatch`/`toAPI` is accepted by the endpoint, silently dropped on
  write, and never reaches the page — no error anywhere.
- **Monitor state semantics**: `docs/state-semantics.md` (rules `S-*`).
  Changes to state transitions, quorum aggregation, freshness/absence,
  pause/maintenance handling, or uptime accounting must update the rule
  table in the same commit; tests cite rule IDs
  (`shared/monitorstate/spec_test.go`). New aggregation surfaces delegate to
  `shared/monitorstate`, never invent parallel state rules. Group state is
  `GroupSummary` (`group.go`, rules S-G): the alerter's SQL uses
  `GroupAlertStateSQL` (pinned by `TestGroupAlertStateSQLMatchesSummary`),
  every display surface `LoadGroupSummaries` + `DisplayState`; never roll a
  group up from member check rows.
- **Interval availability**: `IntegrateTimeline` (`shared/analytics/timeline.go`)
  is the one integration of `monitor_state_intervals` (+ maintenance union,
  paused/unknown/untracked split, serial composite). The monitor headline
  (`computeIntervalAvailability`) and SLA reports (`api/internal/services/slas`)
  both reduce it; never write another interval query. Calendar periods live
  next to it in `period.go`. Issued SLA reports (`sla_reports.data`) are a
  frozen `models.SLAReport` document: add fields, never repurpose them. A
  field added later (e.g. `suspected_causes`) is nil on older reports, and
  every renderer must export those unchanged
  (`TestLegacyIssuedReportRendersUnchanged`, `slas/testdata` goldens).
  Down spans come from its sibling `Outages`: analytics downtime periods
  are `down` intervals on both paths (raw ranges via `loadTimelineDowntime`,
  rollup ranges via the scheduler's `syncDowntimeFromIntervals` projection;
  `TestDowntimeProjectionMatchesRawAnalyticsPath` pins the pair). Never
  derive downtime from check rows.
- **SLA burn and report emails**: burn rate, burn-down and exhaustion (S-U8,
  `api/internal/services/slas/burn.go`) are extra buckets on the report's one
  `IntegrateTimeline` call, never another query; `SLAReport.burn` is optional
  like `suspected_causes`. Scheduled report emails run in the **API**
  (`api/internal/services/reportschedules`, its `Runner` started from
  `server.go` and stopped before the audit recorder): issue the report with
  no lock held, then claim the period and its per-recipient deliveries in one
  `lock_timeout` transaction before mailing; a delivery is `claimed` (safe to
  hand back) until `sending` is committed right before `SendMessage`, so
  `uncertain` (never resent) only follows a started send. Which periods go
  out (catch-up of the last 3, `sends_after`, the grace-hour carry-over) is
  `nextPeriod`; the SLA service re-plans when a period or timezone changes.
  The burn budget base is `periodBudget` (eligible time, as the headline). Schedules need SMTP,
  `APP_BASE_URL` and `PROBARA_SECRETS_KEY` on the API; unsubscribe links are
  `shared/actionlink` tokens (`ActionUnsubscribeReport`, own TTL).
- **Local AI choices, wording and evidence**: `shared/ai/catalogue.json`
  (version `probara-evidence-v1`) is the closed set of hypotheses the model may
  pick and the only source of analysis/suggestion prose and actions; the
  inference service validates requests against the same file (the image
  copies it in) and every artifact manifest pins its version, so a label or
  description change is a catalogue version bump on both sides and needs a
  newly trained artifact; a catalogue mismatch makes the service refuse the
  model and the Go client refuse its answers. What reaches
  the model is the counters-and-booleans allow-list in `shared/ai/evidence.go`
  (`MonitorEvidence`) plus opaque IDs — never names, URLs, error text, bodies
  or monitor config. Model answers only count when `Supports` (and the
  dependency gates in `dependencies.go`) agree with the measured evidence.
- **AI label export format**: `shared/ai/labelexport.go` (`WriteLabelExport`,
  `StateText`) is the Go twin of ai-service `probara_ai/contract.py`
  (`training_row`/`state_text`/`validate_evidence`), pinned by
  `shared/ai/testdata/label-export-v1.jsonl` (`TestLabelExportFixture`,
  regenerate with `-update`); the ai-service training reader must test the
  same file (`read_extra_train` re-renders `op-` rows through
  `from_label_export`), so change both sides together. Labels copy evidence server-side,
  never from the client: `incident_ai_analyses.decision_snapshot` (worker
  `markReady`) and `alert_triage.decision_snapshot` (`markTriageReady`) are
  read by `api/internal/services/ailabels`
  (change the `DecisionSnapshot` shape on both together); pairs come from
  `depsuggest.PairDecision`, the same input builder as `Suggest`. `ai_labels`
  cascades only on tenant delete. The label routes are writes (never in
  `postReadPaths`); export is `RequireTenantAdmin`.
- **Monitor config history**: every path that creates or changes a monitor
  records a version through `api/internal/services/monitors/versions.go`,
  inside the save's transaction (`api/internal/dbtx` carries it through the
  monitors, groups and dependencies services; `Service.InTx`, which retries a
  deadlock once). `CreateMonitor`/`UpdateMonitor` do it; a write beside them
  calls `RecordVersion` (member and dependency routes) or `RecordVersionTx`
  (discovery approval); a change made outside a save (location, channel or
  monitor delete) uses the set-based `PrepareSystemVersionsTx` (before its
  writes) and `RecordTx` (after), which only log failures. A save of an
  existing monitor calls `CaptureBaseline` before its first write; nothing in
  a save may use a second pool connection. **Lock order** (doc in
  `api/internal/dbtx`): every transaction locks the monitor rows it touches
  before its first write, ascending id, one pass (`LockMonitorsTx`: FOR
  UPDATE for monitors changed or versioned, edge sources included; FOR KEY
  SHARE for edge targets); deletes lock the monitors they rewrite the same
  way. The FOR UPDATE row lock also serializes version numbering, with no
  advisory lock; apply's per-workspace advisory lock is taken before any row
  lock (below). Snapshots are
  masked with `secrets.MaskStoredMonitorConfig`; restore never writes a
  secret from a version (`restoreConfig`) and refuses another type. A new
  top-level setting accepted by `UpdateMonitorRequest` is one
  `snapshotColumns` entry (`versions_fields.go`) plus its
  `models.MonitorConfigSnapshot` field, or it is neither versioned nor
  restored.
- **Maintenance coverage**: `shared/maintenance` (S-M5). A window is a
  schedule (one-off, or the weekly `Rule`, expanded only by `Rule.Expand`);
  what covers monitors is its materialized occurrences
  (`maintenance_window_occurrences`), read only through
  `OccurrenceRangesSQL`, `InMaintenance*Predicate` and
  `MaintenanceUntilExpr` — never `maintenance_windows.starts_at/ends_at`.
  Every occurrence writer is in `store.go`: the API on save, the scheduler's
  `maintenance_occurrences.go` rolling the 35-day horizon. Started
  occurrences, removed targets (`maintenance_window_past_targets`) and
  deleted windows (`deleted_at`) are history, never rewritten. Anything that
  inserts a window directly (tests, `scripts/seed-demo`) must insert its
  occurrence too (`testutil.MaterializeOneOffMaintenance`).

## Monitor import

`api/internal/services/import/` parses a source into `parsedSource` (rows +
schema + warnings + skipped rows; `parse.go`), then `ExecuteImport`
(`execute.go`) creates monitors. Two paths, chosen by whether `FieldMapping.Config` is set:

- **Raw path** (`useRawConfig`): rows carry an already-typed nested `config`
  object. Supports every registry type, resolves group members by name in a
  second pass, and validates through `validation.DefaultRegistry` before
  create. Every recognized schema takes this path.
- **Simple path**: generic CSV/JSON/YAML field mapping. Only http, ping, dns,
  grpc and group; everything else is skipped.

Adding a foreign source format means one adapter returning a `parsedSource`
(see `parsePortableExport` and `parseKumaExport`), hooked into `parseJSON` or
`parseYAML` before the generic wrappers. Better Stack, UptimeRobot and Gatus
(`betterstack.go`, `uptimerobot.go`, `gatus.go`) build on `foreign.go`, whose
`finish` registry-validates every translated row (rejects become
`SkippedRows`) and assembles names and groups; each has a fixture and golden in
`testdata/` and a row in the detection matrix (`foreign_test.go`). A row an
adapter adds rather than translates (a domain monitor from a domain-expiry
check) sets `ImportRow.Derived`, so the review step can leave it out.
Build `ImportRow.Fields` **directly** — going through
`flattenMap` dot-flattens the nested `config` and silently drops you onto the
simple path. Records you refuse to translate go in `SkippedRows`, never as
rows: `detectAndSuggestTypes` defaults unknown types to `http` and the UI
auto-applies that suggestion, so an emitted row for an untranslatable monitor
becomes a silent bad import.

**Config-as-code apply** (`POST /monitors/apply[/plan]`, `apply.go`/
`apply_execute.go` here) is another monitor write path, built on the same
row builders (`portableRow`, `build*RequestFromConfig`) and the monitors,
groups and dependencies services (versions source `apply`). One request is
one `InTx` transaction that first takes `pg_advisory_xact_lock` per
workspace on that transaction (released by commit or rollback; never a
session lock on a pooled connection), re-plans, locks rows in lock order
before the first write, and re-checks the locked monitors against the plan
(`checkLockedUnchanged`). Prune deletes keyed monitors (`monitors.key`)
only, within `prune_scope` when given. Secrets are compared only inside the
monitors service (`PlanConfigWrites`, which also resolves a document's
masked URL credentials and synthetic step credentials against the stored
config so the same file converges), and a plan compares a literal with the
stored value only for callers with write access. A field added to the
portable export is applied only once the plan's `diff` handles it.

**Group membership is the junction table**, not `config.monitor_ids`.
`Monitor.MemberIDs`, group alert roll-up, and the dashboards all read
`monitor_groups`. The HTTP handler syncs it after create; the importer must
call `groupService.AddMonitorsToGroup` itself (it does — `syncGroupMembers`),
or imported groups come back empty. Any other path that creates groups through
`monitorService.CreateMonitor` directly inherits the same obligation.

## Adding a monitor type (checklist)

1. `MonitorType` constant + `AllMonitorTypes` entry in
   `shared/models/monitor_types.go` (the one Go list of types; aliased in
   `api/internal/models/monitors.go`). Parity tests then fail in every Go
   mirror that has not been taught the type; config struct in
   `shared/models/` (one file per newer type, e.g. `prometheus.go`; older types
   sit in `check_job_payload.go`). The API validator and the worker both consume
   the shared struct, so put `Validate()` on it rather than duplicating rules
2. Validator in `api/internal/validation/` + register in `registry.go`, and
   its `ConfigType()` in `config_types.go` (the struct MCP's
   `describe_monitor_type` reflects; `TestEveryValidatorNamesItsConfigType`);
   an active type also goes in `activeCheckTypes` (`validation.go`). No
   migration: `monitors_timeout_check` (000099) names only the passive types,
   so only a new passive type rewrites it, together with
   `PassiveMonitorTypes` (`TestTimeoutCheckPassiveTypesMatchGo`)
3. Checker in `worker/internal/worker/` + register in worker `registry.go` —
   any checker that dials must take `(blockPrivateIPs, allowedCIDRs)` and dial
   through `dialGuard` (SSRF policy)
4. Secret fields → `MonitorSecretFields`
5. UI: form component in `web/components/monitors/`, wire into
   `MonitorForm.tsx` (`MONITOR_TYPE_META` + `TYPE_FORMS` dispatch), types in
   `web/lib/types/monitors.ts` (re-exported by `web/lib/types.ts`);
   its hydration and request builder in `web/lib/monitor-form/`, with exact
   payload tests (create, filled, an edit round trip that sets every field
   the form hydrates, clone, secret clears) in `web/lib/monitor-form-*.test.ts`
6. Docs: `website/lib/docs/pages/monitors.ts` (type table + protocol passage +
   secrets coverage) — import/export support is automatic via the registry
7. Display-only mirrors that the registry cannot drive: `typeLabels`/`typeBadges`
   in `status-page/internal/statuspage/render.go` (badge ≤ 5 chars) and
   `typeIcons` in `shared/statustemplate/statustemplate.go` (both pinned to
   `AllMonitorTypes` by tests), `MONITOR_TYPE_ICONS` in
   `web/components/monitors/monitor-type-icons.ts` (typed against
   `MonitorType`, so a missing icon does not compile), `SUPPORTED_TYPES` in `web/app/monitors/import/page.tsx`, `TYPE_LABELS` in
   `web/lib/monitor-type-labels.ts` (every operator-UI type label and its ≤ 5-char
   `badge`), and the type count + `monitorGroups` on the
   landing page (`website/app/page.tsx`, three counts; `website/app/docs/page.tsx`,
   README). Also per type: the status page's target extractor
   (`newMonitorPresenters`, `status-page/internal/statuspage/presenter.go`),
   `monitorTarget`/`monitorTargetLabel` (`web/lib/monitor-utils.ts`), the
   clone case (`web/lib/monitor-clone.ts`), the import page's `getTarget`,
   and any spelling worth an alias in the importer's `typeAliases`
   (`api/internal/services/import/mapping.go`)

## Cross-service contracts

- **Agent monitors are OTLP push**: hosts run `probara-collector` (or stock
  otelcol-contrib) exporting to `POST /api/v1/otlp/v1/metrics` with
  `Authorization: Bearer <tenant API key>` + `X-Probara-Agent-Id` (fallback:
  `probara.agent.id` resource attribute). Samples land in the metric store
  (`metric_series`/`metric_samples`, daily partitions, hourly rollups via the
  `metric_rollup_dirty` ledger); each accepted export records ONE success
  heartbeat via `monitorstate.Record` with **server-clock** `StartedAt`
  (S-O3) and no `metrics_data` — every reader queries the store. The
  legacy `POST /api/v1/agent/metrics` is a 410 tombstone
  (`HandleLegacyMetricsGone`); keep it routed, never revive an ingest there.
  Host alerting is `metric_rules` on the agent config (native units — ratio
  0-1 for `*.utilization`; validated in `AgentConfigValidator`), evaluated by
  the alerter against the store with a freshness bound.
- **Check-job queue**: `CHECK_JOB_STREAM`/`CHECK_JOB_SUBJECT` must be identical
  on API (run-now publisher), scheduler, and workers. Code defaults
  `CHECK_JOBS`/`check.jobs` (constants in `shared/models`, read for all three
  by `loadCheckQueueConfig` in `shared/config` — never read a `CHECK_*` queue
  variable anywhere else); Compose and Helm ship `check-jobs`/`check.job` on
  all three. Changing one service alone silently breaks run-now. In Helm,
  `scheduler.checkJobStream`/`checkJobSubject` are the only knob: every
  workload renders them through the `checkJobEnv` helper (`_helpers.tpl`),
  and a legacy `worker.checkJobStream`/`checkJobSubject` that differs fails
  the render.
- **Check-job delivery**: the worker heartbeats `InProgress` every 10 s while
  a check runs (`checkProgressInterval`, `worker.go`; keep it well under the
  30 s AckWait). Checks whose checker implements `SideEffectingChecker`
  (`checker.go`) are at-most-once: a redelivery (`NumDelivered > 1`) is acked
  without running, a failed publish retries in place, never NAKs, and a result
  the worker's own shutdown produced (`CheckResult.Interrupted`) is acked
  unpublished. A checker that acts on the world (places calls, sends messages)
  must implement it, deciding from non-secret config fields only. Test
  checks (core-NATS request/reply, one goroutine per request) are not bounded
  by `WORKER_CONCURRENCY`, so `handleTestCheck` refuses a side-effecting one
  beyond `maxSideEffectTestChecks` (16) in flight; scheduled checks never
  share that bound. Scheduled
  jobs carry a per-slot `Nats-Msg-Id` (`checkJobMsgID`, used by
  `publishMonitorJobs`), so a dispatch retried for the same `next_run_at` is a
  stream duplicate; keep the id deterministic per slot.
- **SIP test calls** (`sip` + `method: invite`,
  `worker/internal/worker/sip_invite.go`) place real, billable calls.
  `models.IsSIPCallConfig` is the one "is this a call" test (non-secret
  fields; it errs towards a call, matching any key spelling, and the API
  validator parses with `ParseSIPMonitorConfig`, which refuses case-variant
  keys): the checker's `SideEffecting`, the API's tenant-admin gate
  (`monitorservice.RequireCallPermission` → `ctxpkg.IsTenantAdmin`, in
  `CreateMonitor`/`UpdateMonitor` so import inherits it, plus the run-now and
  test handlers; API keys never pass) and the update-time method-class refusal
  all use it. Schedule bounds go through `ScheduleAwareValidator`
  (`validation/registry.go`), applied to the effective
  type/config/interval/timeout on every update. Workers read
  `SIP_CALL_ENABLED` (default true, false with `WORKER_LOCATION_ID`) and
  `SIP_CALL_ALLOWED_DESTINATIONS`; a `SIPChecker` never configured
  (`ConfigureCalls`) places no call. `call.media` (`shared/models/sip_media.go`,
  orchestrated by `worker/internal/worker/sip_media.go`) measures the hold's
  audio; it rides `SIP_CALL_ENABLED` (no second switch), its keys are strict
  (`UnmarshalJSON` records, never returns, what `Validate` refuses, so other
  decoders of a SIP config never break), and a worker that predates a media
  field fails the check closed before dialing. Its web twin is `isSipCall`
  (`web/lib/sip-config.ts`): the UI hides call run/pause/clone and locks the
  form for non-admins (`useIsTenantAdmin`), and confirms every Test and run.
- **Compose and Helm move together**: a port, queue subject or credential
  changed in `docker-compose.yml` must change in the chart's values too (the
  check-job names above show what drift costs).
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
  workloads via the `smtpEnv` helper. The base Compose file wires none of
  it; `docker-compose.release.yml` passes it to all three.
  `SMTP_USE_TLS=true` is implicit TLS (port 465), never STARTTLS.
  `APP_BASE_URL` is the operator-UI origin and only adds "open the monitor"
  deep links and, with `PROBARA_SECRETS_KEY`, acknowledge links to
  notifications (every channel, via `plugin.Configure`) — unset omits them,
  never a guessed host.
- **Notification egress**: the same three workloads read
  `NOTIFICATION_BLOCK_PRIVATE_IPS` (default **true**) /
  `NOTIFICATION_ALLOWED_CIDRS` (`loadNotificationEgressConfig`); each main
  hands both to `bootstrap.ConfigureNotifications`, which calls
  `plugin.Configure` and installs the SMTP mailer. A binary that skips it
  fails closed (private egress blocked, no links).
  This policy is independent of the worker's `HTTP_BLOCK_PRIVATE_IPS`
  (default false); both use `shared/netguard`, the single SSRF range table
  and resolve-then-dial guard (the worker's `dialGuard` is an alias of it).
- **`STATUS_PAGE_VAPID_*`**: status-page only, and both key halves are
  required or visitor notifications are off (no control rendered, push routes
  404). The **public** key is a literal `value:` in the PodSpec — the one
  exception to the never-a-literal rule, since it ships in every rendered
  page — and the private key goes through `monitoring-platform.secretEnv`
  (canonical key `status_page_vapid_private_key`). Mint the pair once with
  `go run ./cmd/admin/gen_vapid_keys`; never generate it at startup, and
  rotating it invalidates **every** stored subscription (`statusPage.vapid`
  in `values.yaml` explains both).
- **Helm `extraEnv`**: top-level (all workloads incl. migrations job, but
  never the local AI pod, which holds no platform credentials — it has
  `localAI.extraEnv`) and `<service>.extraEnv`; `worker.extraEnv` also
  reaches location workers, and `worker.locations[]` entries can carry their
  own.
- **Local AI wiring**: `LOCAL_AI_*` reach the api and the DB-connected
  (platform) worker only — never scheduler, alerter, status page or location
  workers — via `monitoring-platform.localAIEnv` in Helm and explicit
  entries in Compose. The exception is the two kill switches: the alerter
  also gets `LOCAL_AI_DISABLED` and `AI_TRIAGE_ENABLED` (Helm
  `alerterAIEnv`/`aiTriageEnv`, value `localAI.triageEnabled`; Compose shell
  passthrough), which stop suspected-cause follow-ups
  (`AlerterConfig.SuspectedCausesDisabled`); `AI_TRIAGE_ENABLED` also reaches
  worker and api (`aiTriageEnv`; the api's SLA reports read it). The service gets only `LOCAL_AI_TOKEN`. Helm's token is
  canonical key `local_ai_token`; chart-managed and empty, it is derived from
  `secrets.adminJwtSecret` (stable across renders; never `randAlphaNum` or
  `lookup`), and `secret.yaml` fails where it cannot be derived (`externalUrl`,
  or an empty/externally-sourced JWT secret). The clients' reference is
  `optional` so a pre-local-AI existing Secret never blocks api/worker.
- **Helm secret sources**: every credential resolves per-field
  `<field>ExistingSecret` → chart-wide `secrets.existingSecret` → the
  chart-managed `<fullname>-secret`, all through
  `monitoring-platform.secretEnv` in `_helpers.tpl`. Never render a credential
  as a literal `value:` in a PodSpec and never hardcode the Secret name — the
  canonical keys (`postgres_url`, `nats_url`, `nats_platform_password`,
  `nats_location_auth_issuer_seed`, `admin_jwt_secret`, `probara_secrets_key`,
  `oidc_client_secret`, `local_ai_token`, `status_page_vapid_private_key`,
  `status_page_preview_secret`, `bootstrap_setup_code`, `status_page_session_secret`, `status_page_internal_token`) are the contract external secret managers
  fill. `STATUS_PAGE_PREVIEW_SECRET` is one key rendered into two workloads
  (the api signs preview tokens, status-page verifies them) and is `optional`
  only without a per-field Secret (as `BOOTSTRAP_SETUP_CODE`). `ADMIN_COOKIE_SECURE` comes from the
  `monitoring-platform.adminCookieSecure` helper: `api.cookieSecure` if set,
  else true exactly when `api.publicBaseURL` is https.
  Helm cannot read Secrets, so anything the chart *composes* from a
  credential (the Postgres DSN, the embedded NATS URL) needs the composed
  value externalized too — hence the paired `fail`s in `secret.yaml`.
- **NATS platform password** reaches the embedded broker as a `--pass`
  arg that Kubernetes substitutes (`$(NATS_PLATFORM_PASSWORD)`,
  `nats-statefulset.yaml`). Never move it into `nats.conf` `$VAR` expansion:
  that re-parses the value as config (numeric, boolean-ish or
  space/comma/brace passwords abort startup), and quoting the reference
  silently disables the expansion. The release Compose file does the same
  with a Compose-interpolated `--pass` in the nats `command`.
- **Release version (`shared/buildinfo.Version`)** is stamped by
  `-ldflags -X` from each Go Dockerfile's `VERSION` build-arg
  (`docker-build.yml`: the metadata action's version; base Compose:
  `PROBARA_VERSION`, default `dev`); a new Go Dockerfile must stamp it too.
  Services log it at startup and MCP reports it. **`LOCATION_WORKER_IMAGE`**
  (api) is the image private-location deploy snippets reference: unset is
  `locations.DefaultWorkerImage`, the worker at `buildinfo.ReleaseVersion`
  (the moving `:alpha` channel tag for unreleased builds), never `:latest`. Helm sets
  `{image.repository}-worker:{imageTag}` (override `api.locationWorkerImage`).
- **Release Compose** (`docker-compose.release.yml`, an override of the base
  file) runs the published images plus the frontend and an `edge` Caddy. It
  overrides images, ports, credentials (platform ones `${VAR:?}`; optional
  features' never a dev default) and the client-IP chain; other env stays in
  the base file. `scripts/install.sh` must generate every `:?` secret
  (`bash scripts/test-install.sh` checks its `.env` handling: every form
  Compose accepts, `KEY: v` included, counts as set; a second run changes
  nothing). Client IPs: `edge` overwrites `X-Forwarded-For` with
  `{client_ip}` and drops every other client-address header; the frontend
  (never published) forwards it with `API_PROXY_TRUST_FORWARDED_FOR=true`;
  the api trusts only the frontend's fixed address `PROBARA_FRONTEND_IP/32`
  (`TRUSTED_PROXY_CIDRS`). `edge`, frontend and api share `probara-front`,
  which the worker (tenant-defined requests, arbitrary headers) must never
  join; never trust a whole network the worker is on. Public origins
  (`APP_BASE_URL`, `STATUS_PAGE_BASE_URL`) default to empty, never
  localhost; the installer writes them empty and names them in its closing
  text.
- **Status-page origin in the operator UI**: the published UI image is built
  once, so `NEXT_PUBLIC_STATUS_PAGE_URL` is a dev fallback only. The api
  reads `STATUS_PAGE_BASE_URL` and returns it as `status_page_base_url` on
  `GET /auth-context`; `useStatusPageBaseUrl()` (CurrentUserProvider) feeds
  `lib/statusPageUrl.ts`, and every call site passes it; anything that loads
  such a URL by itself (the template preview iframe, which may carry a
  preview token) waits for `useCurrentUser().loading` to clear. The UI server reads
  the same variable at runtime for its CSP `frame-src`
  (`configuredStatusPageUrl`, `lib/csp.ts`). The api reads it once, as
  `StatusPagePublicBaseURL` (`config_api_domains.go`), which also gates
  custom domains. Helm renders `statusPage.baseUrl` into status-page, api
  and frontend (`frontend.statusPageUrl` is deprecated and ignored);
  release Compose passes `.env`'s value to the same three.
- **Result ingest never acks a platform failure**
  (`scheduler/internal/ingest`): a location result is acked unprocessed
  only when verification fails with `errRejected` (subject/payload
  mismatch, unknown or disabled location, bad signature); a database error
  or a schema behind the code NAKs with `retryDelay` (explicit NAKs bypass
  the consumer's BackOff), as do persist failures. Every retry is bounded
  (`retryOrGiveUp`: ~15 min for database/schema errors, 3 deliveries for a
  credential the configured key cannot decrypt), then TERMed
  (`queue.Terminate`) and counted in `result_ingest_abandoned_total`: a
  delayed NAK stays in the ack-pending set, and at the consumer's
  `MaxAckPending` (set deliberately, `resultIngestMaxAckPending`) no new
  result is delivered for any tenant. Core-NATS heartbeats
  cannot be redelivered and only log the difference.
- **NATS websocket listener** (port 8443, plaintext; TLS terminates at the
  ingress or proxy) lets private locations dial `wss://` on 443. Helm
  `nats.websocket.*`: its own Ingress (`ingress-nats-websocket.yaml`, never a
  path in `ingress.yaml`), which fails the render without `nats.auth.enabled`,
  without `tls` (unless `tlsTerminatedUpstream`), and on `/` of an app host; with it on and `api.publicNatsURL` empty the
  API advertises `wss://<host><path>` (`monitoring-platform.publicNatsUrl`).
  Release Compose serves it on loopback 8443. The listener has no auth block,
  so the callout covers it (`TestAuthorizationCalloutCoversWebsocketClients`);
  `shared/queue.NewClient` moves a `wss://` URL's path into `nats.ProxyPath`,
  since nats.go otherwise upgrades on `/`, and refuses credentials over plain
  `ws://` to a non-loopback host.
- **`BOOTSTRAP_SETUP_CODE`** (api, optional): when set, `POST
  /users/bootstrap/first` needs it (`setup_code`, constant-time compare,
  case/dash-insensitive, same 403 for missing and wrong, never logged), and
  `GET /users/bootstrap/status` reports `setup_code_required`, never the code
  (`api/internal/handlers/users`). `LoadAPIConfig` normalizes it once
  (`config.NormalizeSetupCode`) and refuses to start below 16 characters;
  every consumer reads that value. Unset keeps the open first-run form for
  dev and existing installs. `scripts/install.sh` generates and prints it,
  the release Compose requires it, Helm renders `secrets.bootstrapSetupCode`
  through `secretEnv` (`optional` only without a per-field Secret). The login page's field logic lives in
  `web/lib/bootstrap.ts`. With a code set, an SSO sign-in never bootstraps:
  JIT users (the first included) get the ordinary JIT role, and bootstrap
  stays open until an active **superadmin** exists rather than any active
  user (`ConfigureSetupCode` on `oidcauth` and `adminusers`, wired in
  `server.go`; both must follow the same setting). Unset keeps SSO
  bootstrap parity. Either way bootstrap (form and SSO) closes for good once
  a superadmin has ever existed: `admin_bootstrap_closed`, set by a trigger
  on `admin_users` (000106), read via `adminusers.SuperadminEverExisted`.
  Every path that demotes, disables or deletes a user takes
  `adminusers.LockSuperadmins` first, then counts, in one transaction.
- **Client IPs come only from `shared/clientip`** (`FromRequest` behind the
  router-wide resolver middleware, or a `Resolver`): forwarding headers count
  only from `TRUSTED_PROXY_CIDRS` peers (parsed once in
  `shared/config/config_edge.go`, api and status-page). Never read
  `X-Forwarded-For`/`X-Real-IP` in a handler. The web `/api` proxy drops
  them (`web/lib/api-proxy.ts`) unless `API_PROXY_TRUST_FORWARDED_FOR=true`.
- **Edge controls**: `shared/securityheaders` owns the headers (API policy,
  status-page per-page CSP, HSTS only via `IsHTTPS`); `web/next.config.js`
  mirrors its static values. The API mounts headers, the CORS allowlist
  (`middleware/cors.go`) and the anonymous rate-limit stage in
  `useEdgeSecurity` (`api/internal/api/routes_edge.go`); `authMiddleware` is
  wrapped by `rateLimiter.Authenticate`. A presented credential is never
  judged on an address bucket: valid ones spend their own bucket, a signed
  session cookie keys on its user, an expired one (within the refresh
  lifetime, with no bearer header) keeps its 401, and only verified
  failures (401s incl. sign-in, unknown push/collector tokens) are charged
  to the address afterwards. While that failure bucket is empty, anything
  needing a lookup (key, token, refresh cookie, sign-in) is refused before
  the handler, except a live session cookie and a credential that verified
  on this replica in the last 10 min (`verifiedCredentials`,
  `ratelimit_verified.go`: keyed HMAC only, bounded LRU, TTL restarted per
  success, forgotten at first failure; a refresh's rotated cookie is
  recorded from its Set-Cookie and kept for the access TTL + 10 min). A
  refresh carrying a signed access cookie skips the pre-check, charged per
  user, its 401 kept (the access cookie's Max-Age is the refresh lifetime,
  not the token's, so browsers send it expired; keep it so). A request that authenticates and is then refused (bad
  tenant id, `workspace_not_found`) spends its credential's bucket
  (`ratelimit_refusal.go`). A never-seen valid credential and every
  sign-in wait for budget -- accepted. The cookie check is
  `classifySessionToken`, unexported in `middleware` -- never authentication. Address buckets exist only for
  `clientip.Attributable` addresses (trusted-proxy-forwarded or public
  peer), IPv6 per /64. Authenticated routes are read from the router (on
  `RawPath`, as chi routes), so new routes need no list. Never echo `Origin` in a handler. The env rides
  Compose and the chart's `trustedProxyCIDRs` + `security:` block
  (`monitoring-platform.edgeEnv`) together.
- **CSRF**: `middleware.OriginPolicy` (`origin.go`) is the one origin check.
  `AuthMiddleware` applies it to every unsafe request the session cookie
  authenticates (403 `cross_origin_refused`; non-JSON body 415, multipart
  only on `MultipartUploadPaths`, pinned to the MCP catalog by
  `TestMultipartUploadPathsMatchCatalog`); sign-in (both steps), refresh,
  sign-out and the mailed-link POSTs wear `RequireTrustedOrigin` (same
  origin + JSON rule), OAuth consent re-checks it. The UI's `/api` proxy
  vouches for its own origin's writes by dropping Origin/Referer/
  Sec-Fetch-Site (`vouchForSameOriginWrite`, `web/lib/api-proxy.ts`), which
  is why no origin configuration is needed. It judges by its `Host` header
  only (`browserHost`) and never reads or forwards a client
  `X-Forwarded-Host` unless `API_PROXY_TRUST_FORWARDED_HOST=true`; the API
  never believes an untrusted peer's `X-Forwarded-Host`. A new cookie-setting route takes
  `RequireTrustedOrigin`. Reasoning: the `OriginPolicy` comment.
- **Session identity**: one `admin_session_families` row per sign-in
  (password or SSO); refresh tokens rotate inside it and the access JWT's
  `sid` claim names it. The family's `revoked_at` is the only authority:
  `AuthMiddleware` checks it per cookie request (`middleware/session.go`,
  one PK lookup, no cache) and `/auth/me` too, so never add a path that
  trusts a session JWT without it. Revocation lives in
  `api/internal/services/adminauth/sessions.go` (`RevokeSession`,
  `RevokeUserSessions`); a rotated refresh token reused past 60 s revokes
  its family. Password sign-in lockout (10 failures → 15 min, on
  `admin_users`) is `adminauth/lockout.go`: every refusal answers alike,
  and a second sign-in factor counts through `RecordFailedAttempt`. What
  revokes what: `docs/auth-and-rbac.md`. Long-lived responses (the alert
  SSE stream) re-check their credential with `middleware.StillAuthorized`
  each heartbeat and end at `CredentialExpiry`; a new stream must too. The
  `admin_sessions_default_family` trigger (000117) is rolling-deploy
  compatibility only: drop it in a later release's migration.
- **Account links and two-factor** (local password accounts only; an SSO
  account never gets a reset, email change, password or second factor
  here): invite / reset / email-change links and the sign-in challenge are
  single-use rows in `admin_account_tokens` (random, SHA-256 stored,
  purpose-bound; `adminauth/tokens.go`), never `shared/actionlink`. Every
  path that sets a password or resets two-factor retires the user's open
  tokens (`RetireAccountTokens`; `cmd/admin` does it in SQL), or a pending
  email change survives the recovery. Link tokens ride in the URL fragment
  and `X-Action-Token`; the pages are in `web/lib/public-routes.ts`. Without
  `SMTP_*` + `APP_BASE_URL` on the API (`accountmail.Enabled`) only a
  superadmin's response carries a link. TOTP secrets are sealed with
  `PROBARA_SECRETS_KEY` (no key, no enrolment; never the no-op encryptor).
  Failed second factors and account-page password checks reserve an attempt
  through the lockout before comparing; a correct password releases only its
  own count, an internal error gives the attempt back, and a reset keeps a
  two-factor account's ordinary lock (repeated second-factor lockouts hold
  it until unlock or reset). Every password set also revokes the user's MCP
  OAuth grants (`RevokeMCPAccess`), and an address change retires mailed
  links. Passwords are capped at 72 bytes (bcrypt) in
  `auth.ValidatePassword` and `web/lib/password.ts`. The rules and expiries:
  `docs/auth-and-rbac.md`.

## MCP server

- **Tools are a protocol adapter over the router.** Each catalog entry
  (`api/internal/mcp/catalog*.go`) is one REST route; a call rebuilds that
  request and dispatches it through the root router in process with the
  caller's bearer, so auth, audit, the write gate, role gates, validation and
  masking apply unchanged. Never call a service from `api/internal/mcp` —
  extras (`extras.go`) read only compile-time registries or go through the
  router too. Inner requests get a value-free context (`detached`): chi
  reuses a RouteContext it finds, and identity must be re-resolved.
- **Every `/api/v1` route is a tool or in `notExposed` with a reason.**
  `Bind` (startup) and `TestMCPCatalogMatchesRouter` fail on a missing route,
  a tool whose route is gone, or a stale exclusion. Body schemas reflect the
  handler's request type, so name the type (no anonymous decode structs on a
  routed handler); readOnly comes from `middleware.IsReadOnlyRequest`, gates
  from `chi.Walk`.
- **Bearer only.** `Protect` strips cookies before auth; never let the MCP
  route accept the session cookie. MCP OAuth access tokens (`pbm_at_`) are
  accepted only on `/api/v1/mcp` and on requests it dispatched
  (`ctxpkg.ViaMCP`), carry a scope and never a role, and `CanWrite` checks
  scope first so it only narrows. Consent and `/oauth/grants` take a user
  session only, outside the write gate, with their own audit events.
- `tools/list` filtering by capability is UX; enforcement is the inner
  dispatch. Install scripts embed only API keys (`embeddableAPIKey`).
- A handler that refuses API keys without a router role gate marks its entry
  `SessionOnly` (the tenant list); the gate walk cannot see it, and both the
  tool list and the OpenAPI security read the flag.

## Kubernetes discovery

- `cmd/discovery-collector` reads only Services/Ingresses in configured namespaces
  and sends bounded complete snapshots over HTTPS using an integration-scoped
  token. It does not execute checks or receive database/NATS/platform credentials.
- `shared/discovery` owns the snapshot contract and Kubernetes translation;
  `api/internal/services/discovery` owns reconciliation, explicit links and
  atomic approval; `/discovery` is the operator UI. Setup and limitations:
  `docs/monitoring-discovery.md`, `website/lib/docs/pages/discovery.ts`.
- Snapshot revisions and namespace completeness are checked under the integration
  row lock. Failed/partial scans never mark resources missing.
- Integration namespaces are `namespaces` (active: every snapshot covers them,
  one failure aborts the scan), `pending_namespaces` (added via PATCH, scanned
  all-or-nothing, a failure only skips, promoted at the first full scan) and the
  fixed `home_namespace` (always active). Edits are deltas under the same row
  lock and never bump the revision. Any query that reads a service for
  coverage, approval or linking filters `s.namespace = ANY(i.namespaces)`
  (`Overview`, `lockService`), so a removed namespace leaves at once and only
  retention deletes it. Removing inventory
  never removes monitors. Routing assessment uses `shared/alertrouting`;
  check freshness uses `monitorstate.EvidenceWindowSeconds` over
  `COALESCE(last_result_at, created_at)` (S-F1), as the absence watchdog does.
- Discovery timing and bounds are the constants at the top of
  `services/discovery/service.go` (grace, staleness, 7-day retention,
  per-integration resource cap, snapshot rate limit); every age compares on the
  DB clock (`sqlInterval`), never `time.Since`. `pruneInventory` deletes
  inventory and links, never monitors.
- Snapshot data is untrusted: `Snapshot.Validate` recomputes each suggestion's
  `Internal` flag (which gates approval from the shared fleet) and its key, and
  rejects Service targets other than the Service's own `.svc` name. Never read
  a reachability decision from collector input.
- Candidate matching and the approval duplicate refusal share `targetForms` /
  `targetMatchSQL` (`services/discovery/service.go`); change them together.
- Approval reuses `monitors.PostgresRepository.CreateInTx` to commit the monitor,
  its initial timeline interval, location/channels and inventory link together.
  Collector tokens authorize only collector config/snapshot routes, never monitor
  creation. The Docker build matrix publishes its separate collector image.
- Operator writes record their own named audit events
  (`handlers/discovery`, `Handlers.Audit`); `/api/v1/discovery/` is in
  `auditItemizedPrefixes`, so a new operator write route must record one too.
  An approval replay (`created: false`) writes nothing and is not audited.
- The operator routes are MCP tools (`discoveryTools`,
  `api/internal/mcp/catalog_ops.go`); the collector routes sit in
  `notExposed` beside the other ingest routes.

## Notifications

- **Alerter sends go through `deliverNotification`**
  (`alerter/internal/alerter/notification_claim.go`), never `sendFunc`
  directly. Its transactional claim on the `(alert, channel, event)` slot in
  `alert_notification_states` is what makes `alerter.replicas: 2` safe
  without leader election. The claim
  predicates in `claimNotificationTx` and the in-memory pre-filter
  `openAlertEventDue` (same file; used by the lifecycle and mesh dispatch)
  encode the same due rules; change them together.
  The one-shot event columns (`acknowledged_sent_at`, `suspected_cause_sent_at`)
  never touch `last_event_type`/`last_sent_at`, so they cannot move reminders.
- **Acknowledge and snooze mute an alert** (no setting): no reminder and no
  not-yet-fired escalation page; the resolve, the `acknowledged` event to
  paging channels and the `suspected_cause` follow-up still go out. The
  rule is `shared/alertrouting/mute.go` (`MutedPredicate`, `MuteHolds`,
  `DispatchResumedAtExpr`), applied by `openAlertEventDue` (pre-filter, also
  mesh, location offline), `deliverNotification` (claim) and the worker's `eventSuperseded`;
  change all three together (`TestMutePreFilterMatchesClaim` pins the
  alerter pair). Unack/unsnooze restart the delay and reminder clocks
  (`DispatchEligibleSinceExpr`, the reminder claim). A held page the worker
  drops hands its claim back (`releaseMutedClaim`) only when it provably
  reached nobody: no page attempt marker and no recipient in the ledger
  (`recipientledger.MarkAttempt`, written before the send, cleared only on
  `plugin.IsNotDelivered`; `worker/.../page_marker.go`), never by delivery
  count; otherwise the route keeps counting as paged so it hears the ack
  and the resolve. Resolves and relapse notices are marked the same way,
  under their delivery key, which carries the episode
  (`releaseSupersededResolveClaim` and the relapse branch of
  `releaseMutedClaim` read it); a held relapse notice is handed back to the
  exact row its claim replaced (`DispatchEnvelope.Release`), except a
  `resolved` whose resolve never reached the channel, which goes back as
  `created` (`recoveryReached`, the resolve's own marker). Dispatch message ids are the idempotency key plus the
  claim's `dispatchRound` (`notification_claim.go`): what a rolled-back claim
  finds again on retry (the resume clock for a page, `relapsed:` plus it
  for a relapse notice, the round for a reminder, `acknowledged_at` for an
  ack, nothing for resolve and follow-up; the episode is in the
  idempotency key itself), so JetStream's duplicate window collapses a retried publish
  ("duplicate" is success) yet queues a re-page after an unack or snooze end.
  SLA time to acknowledge reads `first_acknowledged_at` (set once by the API,
  never cleared), not `acknowledged_at`, which an unacknowledge clears,
  measured from `first_ack_episode_started_at` (set with it:
  `firstAckEpisodeStartSet` in `api/internal/services/alerts`).
  A lost `resolveAlert` race returns `errAlertAlreadyResolved` and the loser
  skips notifications, so it is not an error to log.
- **Incident recovery entries have one writer**: `shared/incidentrecovery`.
  The alerter (monitor recovered) and the API (operator resolved an alert)
  both call it in the transaction that resolves the alert; its advisory lock
  on the incident is what serializes the two services, so never write the
  "All linked alerts recovered" entry, or take that lock, anywhere else.
  Auto-incident creation is alerter-only (`ensureAutoIncidentForAlertTx`,
  gated by the tenant's `auto_create_incident`).
- **Delivery ordering rests on row locks — keep them.** The async worker
  holds the alert row `FOR SHARE` from its status check through `Send`
  (`eventSuperseded` in `worker/internal/worker/notifications/consumer.go`),
  so a trigger never reaches a provider after its resolve, nor after an
  acknowledgement or snooze: the acknowledge, snooze and resolve UPDATEs
  wait for an in-flight send, and a page or reminder still queued when they
  commit is dropped under the same lock (`alertrouting.MuteHolds`), as is an
  acknowledgement queued before an unacknowledge. Resolves take the lock
  too, so a relapse reopen waits (at most `reopenLockTimeout`) for a resolve
  being sent; a resolve still queued is dropped only while the alert is open
  again and the channel is not a paging one (`releaseSupersededResolveClaim`
  hands its claim back to `created` only when it provably reached nobody),
  and delivered once the alert has recovered again; any other event of an
  earlier episode (`relapse_count`) is dropped.
  The alerter's claim lock is `FOR SHARE` too. Never move `Send` out of the
  worker's transaction or make either lock `FOR UPDATE`; the comments at
  those functions explain why.
- **Relapse reopen** (off unless `tenants.alert_reopen_cooldown_minutes` is
  set): the rule is `shared/alertrouting/relapse.go`; every alert kind's
  opener calls `reopenRecentAlertTx` (`alerter/internal/alerter/relapse.go`)
  before its INSERT, and a new kind's opener must too, as must a new
  resolve path pass `resolvedRecovered` (condition cleared, reopenable) or
  `resolvedOutOfScope` (paused, deleted, rule removed, stale) to
  `resolveAlert`; a manual resolve never reopens. `reopened_at` restarts the
  delay and reminder clocks (`DispatchResumedAtExpr`). A route whose
  `paged_relapse` is below the alert's `relapse_count` is due one `relapsed`
  claim (sent as `created` with `RelapseNotice`, the only send `present`
  words as a relapse), in `openAlertEventDue` and
  `claimNotificationTx` together (`TestRelapsePreFilterMatchesClaim`); the
  episode rides the ledger and idempotency keys
  (`notifications.RecipientEventKeyFor`, `notifications.IdempotencyKey`), and
  paging plugins key incidents on the route's `PagedRelapse`
  (`pagerduty.DedupKey`, `opsgenie.Alias`). What is open at a provider is
  `delivered_relapse`, written only after a delivered page (alerter sync
  path, worker `recordDeliveredPage`); resolves, acks and a relapse notice's
  close of the earlier incident target it (`claimNotificationTx`, worker
  `useOpenIncident`), and a relapse notice dropped or failed for good still
  sends that close (`closePreviousIncident`). Alert times come from the
  database clock: `resolveAlert` and the API resolves write `NOW()`, as the
  reopen window and `reopened_at` do; never resolve with a process clock.
- **Alert wording lives in `shared/notifications/present`**, for every
  channel. `present.Build` turns an event into one `Message` (label, summary,
  metric, facts, deep link, tone); plugins reach it via
  `DispatchRequest.View()` and own layout only. Add a new alert kind's or
  event type's wording in `labelFor`/`summaryFor`/`toneFor`/`metricFor`
  there, never in a plugin or template. Email wraps it (`email/view.go`:
  `alertView` = `present.Message` + palette) so `alert.gohtml` (HTML part)
  and `renderAlertText` (plain-text part) render from one model.
- **Alert kinds**: `alerts_kind_check` (latest rewrite 000100) and the
  `Kind*` constants in `shared/notifications/event.go` list the same kinds;
  a new kind rewrites the CHECK in one migration and adds its constant, its
  wording in `present`, and `AlertKind` in `web/lib/types/monitors.ts`
  (`TestAlertKindCheckMatchesGo` in `shared/db` pins all but the last). A
  kind that notifies must also be listed in `openAlertDispatchablePredicate`
  (`alerter/internal/alerter/lifecycle.go`), or it opens and never pages.
- **Location liveness** (S-F6) is `shared/monitorstate/freshness.go`:
  offline is `LocationOfflinePredicate`, disconnected
  `LocationDisconnectedPredicate`, each with a Go twin
  (`TestLocationPredicatesMatchGo` pins both at the boundaries), connected
  `LocationConnected`. The alerter's `location_offline` evaluator
  (`alerter/internal/alerter/location_offline.go`), the mesh hold, the API's
  `connected`/`offline` and the UI's wording (`locationConnection` in
  `web/lib/status-tones.ts`) all read them; never re-derive one from
  `last_seen_at`. The evaluator's guard (`locationOfflineHold`) holds the
  locations that went quiet together with the newest heartbeat, at most
  15 minutes, and everything just after startup (database clock).
- **Transactional mail** (invites, resets, subscriber mail, reports) goes
  through `shared/mail.Sender` (`SMTPMailer.SendMessage`), never a second
  MIME builder; `Message.Validate` is the header-injection guard and accepts
  only plain dot-atom addresses (`ParseRecipient`). It sends one copy per
  recipient over one reused connection, never falls back to the alert
  default recipients, and reports partial failures as `*mail.DeliveryError`.
  Alert email stays on `SendAlert`. Both run SMTP I/O under a deadline
  (`smtp_session.go`: the ctx deadline, else 30 s per message; a cancelled
  ctx means now), and neither fails a send on QUIT once DATA was accepted.
- **Channel plugins** (`shared/notifications/plugin/builtin/<type>/`,
  registered in `builtin.go`) follow the checklist in the `plugin` package
  doc (`shared/notifications/plugin/doc.go`): the egress-guarded client and
  `PostJSON`/`Post`, `Permanent` errors, `HostMatches` host checks, test
  sends that leave no provider state, the acknowledge capability, and
  multi-recipient errors. The alerter targets advisory follow-ups
  (`suspected_cause`) by `CapabilityFollowUp`, never by plugin type: paging
  plugins (PagerDuty, Opsgenie) leave it off, everything else advertises it.
  Email follow-ups ignore channel template overrides (`effectiveTemplates`).
- **Fan-out progress is durable, per recipient.** Every real send sets
  `DispatchRequest.Recipients` (`shared/notifications/recipientledger`, table
  `alert_notification_recipient_deliveries`) from the pool, never the claim or
  lock transaction (both roll back), keyed by `notifications.RecipientEventKeyFor`:
  the event type, plus for reminders the `last_sent_at` the reminder claim
  replaced (`claimNotificationTx`), carried to the worker as
  `DispatchEnvelope.DeliveryKey`. A deliberate re-send of an event needs a key
  of its own, or it inherits the first send's recipients and reaches nobody. A multi-recipient
  plugin skips what it reports delivered and records each acceptance (the
  `plugin` doc; `twilio` does).
- **Acknowledge links come from `present`, never a plugin.** `ackFor`
  (`present/ack.go`) puts the signed link on `Message.AckURL` for
  created/reminder pages, minted by
  `plugin.Runtime.Links` (`shared/actionlink`, HKDF of `PROBARA_SECRETS_KEY`,
  installed by `bootstrap.ConfigureNotifications`) and bound to the channel;
  no key or no `APP_BASE_URL` means no link, and `View()` never passes the
  signer on test sends. The token rides in the URL fragment
  (`/alert-action#…`) and the `X-Action-Token` header, never a URL path, so
  request logs and metric labels cannot record it; the public API routes are
  `api/internal/handlers/alertactions` (GET reads, only POST acknowledges).
  Paging plugins (PagerDuty, Opsgenie) never render it; SMS drops the deep
  link when it has one. A link acts only on its alert episode: the handler
  refuses one issued (`actionlink.Claims.IssuedAt`, derived from the fixed
  TTL) before the latest relapse's recovery (`issuedInEarlierEpisode`), so
  changing an action's TTL shifts every outstanding link's issue time.
- **Delivery history** (`alert_notification_deliveries`, 90 days) is written
  only through `shared/notifications/deliverylog`, from exactly two send
  paths — `deliverNotification` (`alerter/.../delivery_log.go`: in the claim
  tx when it commits, through the pool after a rollback) and the worker
  consumer (`worker/.../notifications/delivery_log.go`, one call per outcome,
  drops included) — plus the API test send. Never write it elsewhere, never
  let a failed write fail a delivery (`RecordTx`: a savepoint plus server-side
  `SET LOCAL` timeouts — never a client context deadline inside a caller's
  transaction, lib/pq closes the connection when one fires), and never
  store raw error text: `deliverylog` sanitizes it and masks the channel's
  secrets (`SecretValues`). Fan-out rows follow the recipient ledger
  (`deliverylog.Track`), one per recipient an attempt tried. A held page's
  row says `muted_requeued` when `releaseMutedClaim` handed its claim back and
  `muted` when it kept it (`mutedReason`, read in the same tx), so change them
  together; a relapse notice is recorded as event `relapsed`, and a resolve
  dropped for a relapse as `reopened` (committed with its hand-back). A new drop or failure cause is a `Reason*` constant plus its
  wording in `web/lib/deliveries.ts` and the alerting docs.

## Status pages

- **status-page is read-only except for push.** The `Service` takes a
  `db.Querier` and `service_agent_metrics.go` stubs `ExecContext` into a refusal so stray
  writes cannot compile. `pushStore` (`push_store.go`) is the single
  deliberate exception and the only holder of a `*sql.DB` in the package; it
  is constructed only when VAPID keys are set, so an unconfigured deployment
  stays strictly read-only. Do not widen `Querier` to add a write.
- **Visitor push notifications trigger from `monitor_state_intervals`**
  (migration 000082), not `statuspage.updates` — `reconcileNotifications`
  explains why the NATS subject cannot carry them. Anything that changes how
  intervals are written therefore changes what visitors are told. The notify
  rule lives twice, `classifyPushTransition` (Go) and the SQL in
  `reconcileNotifications`; change them together
  (`TestReconcileNotifications_NotifyRuleMatchesClassify` pins the pair).
  Fan-out uses the pages' **rendered** monitors
  (`status_page_section_monitors`, legacy `status_page_monitors`), never
  `statusPageSlugQuery`, which resolves upward through `monitor_groups` and
  would push a member's name to a page that only lists its parent group.
- **`Subscriber.handleEvent` early-returns** when no SSE client is connected
  and the render cache is empty — exactly when browser notifications matter —
  so the push wake-up sits **above** that guard. Anything hooked in below it
  never fires for the case it was built to serve.
- **CSP nonce contract**: every rendered page gets an enforced CSP
  (`status-page/internal/statuspage/csp.go`, modes in
  `shared/securityheaders`, `csp_mode` setting in the three mirrors).
  `{{cspNonce}}` renders `statustemplate.NoncePlaceholder`, swapped per
  response after the cached render; custom head/footer HTML may carry the
  literal `{{cspNonce}}`. Every inline `<script>` in `default.gohtml` needs
  `nonce="{{cspNonce}}"` (`TestCSPNonceContract`; every built-in:
  `TestBuiltinsParseValidateAndCarryNonce`), and a 304 never carries a
  CSP.
- **Built-in templates**: `shared/statustemplate/builtin.go` (`Builtins`) is
  the one list (`default.gohtml`, `classic.gohtml`); the page's `template`
  setting (three mirrors; UI twin `web/lib/status-page-templates.ts`) picks
  one while no custom template is published. API validation,
  `GET …/template/default?name=` and the renderer (`builtinTemplateFor`) all
  read it. View fields the classic built-in needs are built in
  `status-page/internal/statuspage/render_classic.go` from data the render
  already loads, reach every template, and are documented in
  `website/lib/docs/pages/status-pages.ts`; never change `default.gohtml`'s
  output to serve another built-in.
- **Visibility gate before everything** (`statuspage/access.go`, contract
  `shared/pageaccess`): `authorize` runs first in `HandleStatusPage`, before
  the method guard, every sub-route and the slug-keyed render cache; deny by
  default, exempt only `/unlock` and a valid preview token on
  `/preview/draft`. A new route or suffix (feeds, summaries, badges) needs a
  row in `routeTable` (`TestRouteAccess_EveryRouteIsClassified` scans the
  package). Private-without-credential answers via `writeNotFound`, identical
  to a missing slug, from the same single `PageAccess` query. The API alone
  writes visibility, password hashes and token hashes; sessions are stateless
  HMAC cookies (`session.go`, `STATUS_PAGE_SESSION_SECRET`, path-scoped;
  Helm derives it from a chart-managed `adminJwtSecret`). No
  push on password/private pages; a surface that enumerates pages lists only
  `pageaccess.Listed` ones. A route behind the gate writes through the
  returned `accessWriter` (`gated()`; `markGatedResponse` forces no-store, no
  ETag, no CORS, same-origin CORP) and gets `crossPageRequest` (another page
  on the origin may not fetch or frame a gated page) for free. Wrong passwords
  and kiosk tokens share `failureThrottle` (`unlock_throttle.go`: failures
  only, `clientip` `BucketKey`, LRU-capped). Every SSE stream re-checks access
  on its heartbeat. Migration 000114's down refuses while a page is not public.
- **Page add-ons** (`feed.atom`, `summary.json`, badges; `addons.go`) are
  sub-routes dispatched from `HandleStatusPage` (`parsePageAddon`, every
  shape in `pageAddonRoutes`), never mux routes, so a check there covers
  them. `summary.json` and the badges come from the page's own render
  (`cachedPage`, keyed by slug alone, so access checks run before it) and
  show only rendered components; hidden uptime stays `null` and uptime
  badges 404 (`summary.go`); `/data` applies the same rule
  (`publicPageData`). The feed is cached under `feedCacheKey` and dropped
  with the page. A gated page's add-ons go through `gatedPageRequest` (one
  call: private, no CORS). Affected lists use the page's display names
  (`pagePublicNameSQL`). The summary is a versioned contract pinned by
  `testdata/summary_v1.golden.json`: add fields, never repurpose them. Head
  metadata and the indexing decision live in `seo.go` (`pageIndexable`:
  only public pages are indexable, whatever `allow_search_indexing` says).
  The gate passes its visibility on the request (`withPageVisibility`) and
  `cachedPage` rebuilds an entry built under another one; an access change
  invalidates the page, summary and feed together.
- **Email subscribers are written by the API alone**
  (`api/internal/services/pagesubscribers`); the status-page service only
  forwards (`subscribe.go`, `STATUS_PAGE_INTERNAL_API_URL`), presenting
  `STATUS_PAGE_INTERNAL_TOKEN` (`pageaccess.ForwardTokenHeader`; the API's
  subscribe/confirm answer nothing else, one-click unsubscribe stays public),
  per-client `subscribeThrottle`, nothing identifying the visitor forwarded.
  The subscribe form is gated; on the platform host the `/subscription…`
  link routes run before the gate and never read the page (row class
  `classSubscriptionLink`), so unsubscribing survives a visibility change;
  on a custom domain they are gated like the page. Public and unlisted pages only;
  addresses are readable with write access only (`…/subscribers/summary` for
  everyone). Page limits count rows created in the hour whatever their
  state; confirmations are capped per inbox (`InboxHash`, +tag stripped) and
  per hour (`status_page_confirmation_sends`). The
  notification outbox (`status_page_notification_outbox`, written by
  `api/internal/services/pagenotify` in the trigger's own transaction) is the
  single trigger for subscriber mail and incident push: a new trigger is a
  `pagenotify` call inside that transaction, never a send. The runner follows
  the report schedules' claimed/sending/uncertain rules; every subscriber link
  is built in `pagesubscribers/links.go` through `pagedomains.PublicBaseURL`
  (the page's verified custom domain, else `STATUS_PAGE_BASE_URL`), except
  the unsubscribe landing, always on the platform host; tokens
  are `shared/actionlink` actions in the URL fragment. Subscribe answers alike
  for every address state and never mails from the request. Automatic outage
  publishing (`autopublish.go`) finds or creates incidents only through
  `shared/autoincident`, the alerter's lock and lookup; page component names
  come from `shared/statuspagesql`. **No public surface shows an
  automatically created incident's own title or summary** (they name the
  monitor): `incidents.PublishTx` gives every publication of one a per-page
  `public_title`/`public_summary`, and the page, `/data`, feed, summary,
  subscriber mail and push read `COALESCE(isp.public_title, i.title)`
  (`publicIncidentTitleSQL`). A new public reader uses the same columns.
  Announced maintenance withdrawn (delete, one-off skipped at start) is a
  `pagenotify.MaintenanceCancelledTx`, mailed only to who got the
  announcement; an announced window is soft-deleted, never removed.
  Every subscriber query (public endpoints, runner, auto-publish) excludes
  deleted workspaces with `tenancy.LiveTenantSQL`.
- **Custom domains** (`shared/pagedomains` is the contract; the API alone
  writes and verifies them in `api/internal/services/statuspagedomains`, TXT +
  CNAME, DNS only, runner claims with `FOR UPDATE SKIP LOCKED` in batches
  sized to finish within the lease). A hostname is unique only among
  **verified** rows (partial unique index): unverified claims coexist, the
  first to verify supersedes the rest (`record`, audited per workspace), and
  pending/superseded claims expire after `ClaimTTL`. Verification messages
  are fixed wording (`verify.go`), never resolver text or foreign names. On
  the status page, `CustomHosts` (`custom_host.go`) runs **in front of the
  mux** and **fails closed**: the platform routes only for platform hosts
  (`STATUS_PAGE_BASE_URL`, `STATUS_PAGE_PLATFORM_HOSTS`) and names that can
  never be a domain (`pagedomains.CanBeCustomDomain`: probes, in-cluster);
  a verified host is rewritten onto `/public/status/{slug}/…` so the gate,
  route table and caches apply unchanged (`/sw.js` included, not found on
  gated pages); every other host, and every public host before the table
  first loads, gets `writeNotFound`. The API keeps the feature off unless
  the same settings make the status page fail closed
  (`config.StatusPageFailsClosed`). Every domain query carries
  `tenancy.LiveTenantSQL`; the workspace purge removes domains first. Hosts resolve only through the
  in-memory `hostTable` (`domains.go`: every claimed hostname, one join
  through `status_pages`, invalidated by page events above the subscriber's
  guard, 30 s TTL), never a per-request query on the Host, and the gate
  refuses a slug whose page ID differs from the table's
  (`customHostPageMismatch`). Visitor-facing paths go through `pagePath`,
  `pageSubPath`, `visitorPath` and `sessionCookieScope` (host-only `/`,
  always `Secure`), never a literal `/public/status/`; absolute page URLs
  (API and status page) come from `pagedomains.PublicBaseURL`/`PublicURL`
  with the primary domain from `PrimaryHostSQL`, the same on every host, so
  the slug-keyed render stays host-independent. Push subscriptions keep
  `origin_host` for their deep link (`payloadsByOrigin`). New custom-host
  path forms go in `customHostRouteTable` (`custom_host_test.go`).

## Web UI conventions

- **Selected tenant**: `lib/tenant.ts` owns storage +
  `TENANT_CHANGED_EVENT`; `components/providers/TenantProvider.tsx` is the
  React-side source of truth (`useSelectedTenantId`) and its `TenantScope`
  keys the page subtree on the tenant, so every page's mount-time fetch
  re-runs on a switch. Pages must not add their own `tenant-changed`
  listeners — only components *outside* that subtree (Sidebar,
  `useFleetHealth`, CurrentUserProvider, AlertStreamProvider) need one. `…/[id]` detail routes
  redirect to their list page on a switch (`/users/[id]` excepted: users are
  platform-scoped). A change to the workspace list (create, rename, delete,
  or a `workspace_not_found` answer, handled in `lib/api/core.ts`) emits
  `TENANTS_CHANGED_EVENT`, which only `TenantSwitcher` listens to: it reloads
  the list and settles the selection again. Platform-scoped API paths are
  `PLATFORM_PATHS` in `lib/api/core.ts` (never `X-Tenant-ID`, never the
  no-workspace error); add a new platform route there.
- **Folded routes stay as redirects**: Mesh is a tab of Locations and
  the alert channel list is a tab of Settings (`?tab=`, via
  `components/ui/Tabs.tsx` `useUrlTab`). `/mesh` and `/alert-channels` are
  kept as redirects — mesh-edge notifications deep-link to `/mesh`
  (`shared/notifications/present`) — and `/alert-channels/{new,catalog,[id]}`
  remain real routes. Link to the tabs, not the old list pages.
- **Row actions** in tables and lists use `components/ui/IconButton.tsx`
  (icon only, named by its tooltip, `danger` last) for up to three
  actions, and `components/ui/RowMenu.tsx` (⋮, position:fixed, flips up)
  beyond that. Don't put text buttons in rows — except a lifecycle list's one
  labelled next step (alerts: Acknowledge → Resolve, the rest in ⋮; see
  `components/alerts/AlertFeed.tsx`).
  Bulk selection (monitors, alerts) is a hover-revealed box in a reserved
  lead slot (pinned once anything is selected, always on touch), Shift-click
  ranges, Escape clears, and a portaled bar at `md:left-[var(--sidebar-offset)]`
  for writers only (alerts: `useAlertSelection`, `AlertBulkBar`). A
  single-key row shortcut (alerts' A / R) fires only from a focused row and
  never in text fields — WCAG 2.1.4; the guard, range and prune rules are
  pure functions in `lib/alert-list.ts` with node tests.
- **Shell**: the sidebar (`components/layout/Sidebar.tsx`) is the only chrome
  — no desktop top bar; below `md` it is a drawer opened from `TopBar`. It is
  ONE instance on every breakpoint, so `TenantSwitcher`'s tenant bootstrap
  (load, settle, `emitTenantChange`) runs once; never render a second copy.
  From `md` it collapses to a 64px icon rail (header toggle, or Ctrl+B / ⌘B —
  a modifier chord, never a bare character key, WCAG 2.1.4; its rules are
  `lib/sidebar-shortcut.ts`, unit-tested; every page-level modifier chord
  takes its ⌘/Ctrl label and platform check from `lib/shortcuts.ts`
  (`shortcutLabel`/`shortcutAria`, `isApplePlatform`) and stands down behind
  a dialog via `modalOpen`): the same
  instance with `rail` props, rows folding via `components/layout/rail.ts`.
  Nothing may change height while the width animates: rows keep one
  structure, the header is one fixed row (the rail toggle overlays the mark),
  and the footer swaps layout only once the width settles.
  State is `html[data-sidebar="collapsed"]`, set pre-paint by the boot script
  in `app/layout.tsx` from localStorage `probara-sidebar` and owned by
  `useSidebarCollapsed`; widths are CSS keyed on it, and fixed overlays that
  clear the sidebar use `md:left-[var(--sidebar-offset)]`, never a literal.
  Rail labels are `components/ui/Tooltip.tsx` (`useTooltip`, portaled).
  `useFleetHealth` (called once, in `Layout`'s `Shell`) feeds the sidebar
  FleetHealth row and TopBar; fleet health never tints the page. The shell
  has no content cap below 2560px (`max-w-[160rem]`, gutters only); each page
  picks its width with `components/ui/Page.tsx` (`<Page width="data|detail|
  wide|reading|form">`, the page root). Settings is `data` too: its tabs
  lay panels into container-width columns from
  `components/settings/layout.ts`, never viewport breakpoints (the sidebar
  changes the main width). Tailwind screens add `3xl` 1920 / `4xl` 2400.
- **Route titles**: every route folder has a server `layout.tsx` that only
  exports `metadata.title` (the root template appends ` · Probara`); nested
  segments under a titled parent use `title: { absolute: '… · Probara' }`,
  since a parent's string title stops the root template. Client detail pages
  name themselves with `useDocumentTitle` (`lib/useDocumentTitle.ts`).
- **Glass surfaces**: the canvas is flat (no gradients, grid or bloom).
  Panels use `.glass` (or `Panel`/`FormCard`, near-opaque, no
  blur), overlays `.glass-overlay`, groups inside a panel `.glass-well`,
  banners `components/ui/Callout.tsx`; only overlays and the shell blur, and
  never nest a blurred surface in another. Orange (`cyan-*`) is for actions
  only; text on it is `text-onaccent`. "Selected" tabs and segments are
  neutral raised glass (`SELECTED_SEGMENT` in `components/ui/roving.ts`,
  fill `bg-selected`/`--segment-selected`); a selected `FilterChip` is
  inverted ink (`bg-white` + panel-coloured text) so applied filters read
  at a glance; the active nav item is
  `bg-nav-selected` (a filled step above hover on the shell); never orange
  and never a rail; checked list rows use `bg-well-strong`. Icon-only
  buttons are named by `useTooltip` (`components/ui/Tooltip.tsx`, fast,
  shows on focus), never a native `title`.
  Chart series take `--chart-1..6` (`SERIES_COLORS`/`LATENCY_COLOR`/
  `CHART_GRID` in `components/monitors/metric-chart.tsx`), never a hex or a
  status colour; monitor-detail charts are that file's `ChartTile`/
  `ChartLegend`/`MetricChart` (`xDomain` spans the page range), never a
  per-view copy, and detail side panels are `DetailRow`/`DetailList` from
  `components/monitors/MonitorFactsCard.tsx`; an empty trace slot or no-data bucket is `NO_DATA_BAR`
  (`lib/status-tones.ts`, hollow). Text meets 4.5:1 and meaningful marks
  3:1 on every surface they sit on (contrast notes per token in the
  `globals.css` header). Focus is
  `FOCUS_RING`/`FOCUS_RING_INSET` (`components/ui/focus.ts`, the `--focus`
  token); input, select, checkbox and radio borders use `--input-edge`, the
  Switch off track `--switch-off`. Type scale is `text-2xs`/`xs`/`ui`/`sm`/
  `title`, plus `text-metric` for KPI numbers; no arbitrary px sizes. The
  rules and token list sit in the comment at the top of `app/globals.css`
  and in `tailwind.config.ts`.
- **Theme** follows `prefers-color-scheme` unless `probara-theme` is stored
  (boot script in `app/layout.tsx`); `ThemeToggle` cycles light/dark/system.
- **Motion**: `--dur-*`/`--ease-*` in `globals.css`; Tailwind
  `duration-fast|base|slow`, `ease-standard|emphasized|exit` and
  `animate-fade-in|pop-in|dialog-in|toast-in|row-flash|bar-in|value-in|
  live-ping|status-pulse` (row-flash/live-ping take their colour from the
  `--flash` CSS var, see `flashStyle` in `lib/useRowFlash.ts`). Two things
  loop, both meaning "this needs someone now": `status-pulse` on the newest
  CheckTrace bar of a monitor degraded right now, and `live-ping` on an
  unacknowledged alert's dot on /alerts (and on LiveStatus's dot) — never
  pulse whole traces, lines or rows. Reduced motion is
  handled globally in `globals.css`; never hard-code a duration.
- **Formatting and tones have one home each**: dates, recency, durations,
  uptime %, latency, call quality and the `RANGES` table live in
  `lib/format.ts` (`formatDateTime`, `formatTimestamp`, `formatRelativeTime`,
  `formatUptime`, `formatLatency`, `formatMOS`/`formatLossPct`/
  `formatJitter`, …); what colour a state or number gets lives in
  `lib/status-tones.ts` (`uptimeTone(pct, target)` for availability only;
  `failedChecksTone` for check-bucket trace bars, `utilizationTone` for
  agent gauges from the monitor's `metric_rules`, `mosTone`/`lossTone` for
  SIP call quality from the monitor's `call.media` thresholds (with default
  bands), `thresholdTone` for its jitter and round trip (own thresholds only);
  alert/incident/check/monitor-status tones). Never `toLocaleString()`, `toFixed()` on a percent,
  or an inline uptime threshold in a component. Severity order, monitor
  target and short failure reasons are `STATUS_RANK`/`sortBySeverity`,
  `monitorTarget`, `shortFailureReason` in `lib/monitor-utils.ts`.
- **Shared controls**: time ranges use `components/ui/RangePicker.tsx`
  (page-scoped: header or Tabs `trailing`, never inside one card); a setting
  with a few discrete stops (sensitivity, a window length) is
  `components/ui/SegmentedControl.tsx` (radiogroup, roving arrows,
  `SELECTED_SEGMENT`), never a range slider (there is no slider style); a
  Switch's on track is neutral ink like a checked Checkbox, never orange; a save bar shown only
  while a form is dirty floats over the page (portaled, `data-floating-bar`,
  as in Settings → Notifications) so its arrival never shifts content; monitor
  types render as `components/ui/TypeBadge.tsx` (pass `config` where it is at
  hand so a SIP test call reads `CALL`, via `monitorTypeDisplay`), labels from
  `lib/monitor-type-labels.ts` only; write affordances gate on
  `useCanWrite()` / `useIsTenantAdmin()` from `CurrentUserProvider` (both
  read false while identity loads, so "read-only" copy also waits for
  `useCurrentUser().loading` to clear). Recent
  checks draw as `components/monitors/CheckTrace.tsx`, the one check-history
  trace (monitors list, dashboard; fixed 79px, equal-height bars)
  — never a second bar strip. Multi-select list filters and tag pickers
  (/monitors Type and Tags, the dashboard's tag scope, `TagFilter` in
  `MonitorMultiSelect` and the SLA form) are
  `components/ui/FilterPopover.tsx`: a FilterChip trigger with a portaled,
  anchored popup — never an inline panel that pushes the content down. In a
  dialog it stacks above the scrim (z-60) and its `data-popover-layer` keeps
  ModalPortal's focus trap off its Tab order; Escape and an outside click
  (whose mousedown and click it swallows) close only the popup. Tag pickers
  that set a value (SLA tags, Settings dashboard groups) use `TagFilter`
  `asValue`.
  Checkboxes and radios are
  `components/ui/Checkbox.tsx` / `Radio.tsx` (`onChange` receives the boolean
  first; never a native `accent-*` box). Every modal is
  `components/ui/Dialog.tsx` (form modals put the submit button in `footer`
  with `form={id}`); scrolling lists inside a card use
  `components/ui/ScrollList.tsx`.
- **Sorting**: list columns use `components/ui/SortableHeader.tsx` over
  `lib/sort.ts` (nulls last in both directions), with state in
  `?sort=&dir=` through `useUrlSort` (defaults left out), and sort the whole
  filtered set before paging. Server-paged views (audit) pass the sort to
  the API instead (`sort=`/`dir=`, a whitelist in the service — `ParseSort`
  in `services/audit/sort.go`, and `ParseListSort` in
  `services/alerts/sort.go` for API clients — ending in an id tiebreaker so
  pages never overlap); keep client sort wherever the whole set is loaded.
  /alerts has no column sort: it is a time-ordered feed (below).
- **/alerts is a timeline over a feed**, both derived from one loaded set
  (the whole open queue plus the range's newest resolved alerts, caps in
  `app/alerts/page.tsx`). `lib/alert-timeline.ts` is the timeline's model
  (segments, lanes, groups, ticks, and `foldSuppressed`: which open alerts
  fold under a root cause) and `lib/alert-list.ts` the feed's (`feedRows`:
  which alerts a view lists, newest start first; `feedItems`: their layout
  on the range's calendar, where every hour or date appears and an empty
  stretch is a "No alerts" gap, never skipped; `mergeLiveAlert`); both are pure with node
  tests, and `AlertTimeline`/`AlertFeed` only lay them out. An alert folds
  only when the API reports it suppressed and its root's open availability
  alert is loaded — anything that pages stays a top-level row. Stream events
  carry only identity, status, times and root cause, so apply them with
  `mergeLiveAlert` (never `mergeAlertUpdate`, which is for full API
  responses) or rows lose their kind and unfold. Past a cap, say so and
  hatch the timeline (`coveredFrom`, taken from the load, never from the
  rows); never count over a truncated set, and never lay out a gap below rows still
  to load (`feedItems`' `complete`). The feed grows as its end scrolls into
  view (`AutoMore` in `AlertFeed`, with the button kept for keyboard users),
  then raises the resolved cap.
  Timeline tones are `TIMELINE_LINE`/`TIMELINE_DOT` in `lib/status-tones.ts`
  (a resolved alert's line is neutral; suppressed is dashed/hollow). Only an
  unacknowledged alert moves: its rail dot and its line's "now" end ping
  (`animate-live-ping`); acknowledged, suppressed and resolved stay still. Groups follow the tenant's
  `dashboard_group_tags`, else monitor type (`timelineGroupOf`).
- **Live pages and feedback**: relative times render as `RelativeTime` /
  `ElapsedTime` (`components/ui/RelativeTime.tsx`, on the shared `useNow`
  clock), never a frozen `formatRelativeTime` string. Live pages load through
  `useLiveRefresh` with `LiveStatus` in the header and flash changed rows with
  `useRowFlash`. Toasts go through `showToast` (`group` coalesces bursts); no
  success toast for a change already visible in place, and an own
  acknowledge/resolve calls `markLocalAction` first so the stream echo stays
  quiet.
- **Dashboard**: `app/page.tsx` `GRID`/`CELL_*` constants own the 12-column
  xl/3xl/4xl layout; a service group's tone is `groupHealth()` in
  `lib/dashboard-view-model.ts` (current worst member state; history only
  through `uptimeTone`), never `attention_count`.
  Per-check `metrics_data` charts on the detail page (`MongoClusterPanels`,
  `SipCallPanels`) fetch through `lib/useMetricsSnapshots.ts` and pick and
  grid their points with `lib/metric-snapshots.ts`; a new panel does the same.
- **API failures**: `lib/errors.ts` is the one error contract. The client
  throws `ApiRequestError` (a real `Error` carrying the server's `error`
  code) for JSON error bodies; show failures with `errorMessage(err,
  fallback)` and branch on `errorCode(err)`. Never throw a plain object from
  the client, and don't write another message-extracting helper.
- **Client layout and shared plumbing**: API calls live per domain in
  `lib/api/` (request plumbing in `core.ts`, query strings via `withQuery`)
  and wire types in `lib/types/`; `lib/api.ts` and `lib/types.ts` are
  barrels, so add to the domain file and never `fetch('/api/…')` from a
  component. A mount-time fetch is `lib/useAsyncData.ts` (`useLiveRefresh`
  when it must stay fresh), paging is `components/ui/Pagination.tsx`,
  copy-to-clipboard is `lib/useClipboard.ts`, a form's config "Test" is
  `components/monitors/useConfigTest.ts`, and the route-handler proxies
  share `lib/api-proxy.ts`. `any` is a lint error
  (`@typescript-eslint/no-explicit-any`); a `catch` binding stays untyped.
- **`/monitors` deep links** (`?status=`, repeatable `?tag=`,
  `?routing=unrouted`, `?sort=status|name|success|latency|last_check` with
  `?dir=asc|desc`, defaults left out per `MONITOR_SORT_DEFAULT_DIR`) are
  parsed and written only by `parseMonitorListParams`/
  `formatMonitorListParams` in `lib/monitor-list.ts`; link to the filtered
  list with `monitorsListHref` rather than a hand-written query string. The list order is
  `sortMonitorList`: groups and monitors sort together, a group ranking by
  its worst member under status. Tags match ANY selected tag
  (`matchesAnyTag`), and every dashboard endpoint's `tag` scope is the same
  rule (`tags && $n`, never `@>`) so a dashboard count and the list it links
  to agree. The list page (`app/monitors/page.tsx`) keeps the row actions
  (delete, pause, snooze, export) and the filter wiring; rows, panel,
  toolbar, bulk bar and its hooks live in `components/monitors/list/`,
  and the filter and group-tree rules (`monitorMatchesFilters`,
  `groupHasMatchingMember`, `groupContainsGroup`) in `lib/monitor-list.ts`.
  Likewise the one-alert `/alerts?alert=<id>&tenant=<id>` link notifications
  carry (built in `present.suspectedCauseAction`) is parsed only by
  `lib/alert-links.ts`.
- **Alert counts**: "active" = `status=active` only (sidebar fleet health,
  dashboard tile); "open" = active + acknowledged (the alerts page's Open
  chip and the timeline's "Open now" group). Keep the words matched to what is counted.
- **Overlays must portal**: pages wrap content in `space-y-*`, which puts a
  `margin-top` on a `fixed inset-0` sibling, so the backdrop stops covering the
  viewport (and `main`'s `overflow-x-clip` can clip it), and every `.glass`
  ancestor's `backdrop-filter` becomes the containing block of a `fixed`
  child, offsetting and clipping it. Wrap modal roots in
  `components/ui/ModalPortal.tsx` (menus/popovers: `createPortal`, as
  `RowMenu` and `InfoTip` do). ModalPortal also traps Tab focus, marks
  `#app-root` inert while open and restores focus on close; pass `onEscape`
  unless the modal handles Escape itself (`Dialog` does).
- **Large pages keep composition; their pieces sit with the domain**: the monitor page
  (`app/monitors/[id]/page.tsx`) reads `useMonitorDetail` (data, run-now,
  pause, delete, history reset) and renders header, aside and tab
  components from `components/monitors/detail/`; its Server panel facts are
  `lib/monitor-server-facts.ts` (database, TCP) and `lib/sip-metrics.ts`.
  The dashboard (`app/page.tsx`) reads `useDashboardData` and
  `useProblemTraces` and lays out the cards in `components/dashboard/`
  (grid cells and list limits in its `layout.ts`).
  Settings tabs are panels in `components/settings/`; state that must
  survive a tab switch (a just-created API key, the retention draft) is a
  hook the page calls (`useApiKeys`, `useRetentionSettings`), never state
  inside a panel that unmounts with its tab.
- **Monitor forms are `noValidate`**: each calls `reportConstraintViolation`
  (brings back native min/max checks), `useClearErrorsOnEdit` and
  `useFollowEnabled` from `components/monitors/form-kit.tsx`; a new type
  form does the same. MonitorForm itself is the shell: it owns the state and
  submit of the types it edits inline (http, ping, dns, synthetic) and draws
  their fields through field-only editors (`HttpMonitorForm`, `DnsEditor`,
  `SyntheticApiEditor`, `SyntheticBrowserEditor`); synthetic "Test now" is
  `useSyntheticLiveTest`. A large type form keeps state, validation and
  submit in its own file and draws its panels from a folder beside it
  (`components/monitors/sip/`, `components/monitors/database/`); the
  panels hold no form state. Labelled fields in a type form are
  `components/ui/FormField`; form-kit's `FormInput`/`FormSelect`/
  `FormTextarea` are only for those inline editors. Form layout is form-kit
  too: sections are `FormPanel`, settings are
  `FormRow`/`ToggleRow`, the sticky bar is `FormActionBar`, and the form
  root carries `data-form-stack`; MonitorForm's shell switches the
  two-column (section rail + card) layout on from its measured width
  (`useWideForm`). Child forms update state functionally
  (`setFormData((prev) => …)`): AlertingSection fires two changes in one
  event when switching to Custom.
- **Monitor form payloads are pure and pinned**: every form's initial state
  and submitted request come from `lib/monitor-form/` (`initial*FormState`,
  `build*Request`; the shared envelope is `common.ts`), never from inline
  `handleSubmit` code. `lib/monitor-form-*.test.ts` assert the exact wire
  JSON of every type's create and edit round trip (and of a clone for the
  delegated types and http), so a payload change shows up as a test diff;
  a field a form hydrates goes into its type's edit fixture.
- **CSP**: `web/proxy.ts` sends a report-only nonce CSP (`lib/csp.ts`);
  no inline `<script>` without the `x-nonce` nonce (`app/layout.tsx`).

## Build, test, verify

- Go is a single module at the repo root (`go.mod`) — run everything from
  there: `go build ./...`, then `go test` scoped to what you touched
  (`go test ./api/...`, `go test ./shared/notifications/...`). `make test`
  runs the whole module with `-race` (skipping `scripts/`); `make lint` fails
  on gofmt drift, `go vet` findings in either build mode, or an
  integration file missing its build tag. The full api suite takes >2 min — run
  it in the background.
- Tests sit next to the code; external-service, browser and binary-backed
  cases are `*_integration_test.go` with `//go:build !unit`. Plain `go test`
  and `make test` still run everything; `make test-unit` / `go test -tags=unit`
  exclude those files. Anything a plain `*_test.go` calls must live in an
  untagged file, or only `-tags=unit` builds break. CI checks that every
  `*_integration_test.go` carries the tag and runs the fast job without
  Docker, so a Docker-backed test in a plain file fails there; it cannot
  detect other misclassified dependencies (Chrome, binaries).
  `testutil.SetupPostgresDB` uses testcontainers and fails (not skips)
  without a Docker daemon — then set
  `PROBARA_TEST_POSTGRES_DSN=postgres://user@host:port/postgres?sslmode=disable`
  (role needs CREATEDB) and each test gets a scratch database on that server.
  The SIPp INVITE test (`worker/.../sip_invite_integration_test.go`) is no
  database test and skips instead when no Docker provider is reachable (CI
  still fails that skip). Its `media/*` subtests run SIPp's `-rtp_echo` on a
  fixed UDP port published on the host's `127.0.0.1` (the templated
  `uas_echo.xml.tmpl` names it), so they need a local daemon (Docker Desktop
  or the runner's dockerd); a remote `DOCKER_HOST` fails them, never skips.
- CI runs a fast Go job and three complete package groups with `-race`;
  each full group gets one Postgres service via `PROBARA_TEST_POSTGRES_DSN`.
  `scripts/ci.py` under `.github/workflows/` owns package assignment, JSON
  failure/timing summaries, unexpected-skip checks and coverage merging.
  The aggregate keeps the required `Go test (main module)` check name.
  `docs/testing.md` documents the suites, intentional optional skips and
  conservative AI checkpoint selection; update it when changing the workflow.
- Web/website: CI runs `npm run lint`, `npx tsc --noEmit` and `npm run build`
  in both (`website/` lint allows no warnings), plus `npm test` in `web/`.
  Run the same in whichever one you touched.
- Python (`ai-service/`, `scripts/*.py`, CI helper scripts): CI runs
  `ruff check` with the pyflakes rules in `ruff.toml`; run it before pushing
  Python changes (`pip install ruff==0.8.4`).
- OpenAPI: `api/openapi.json` is generated from the MCP tool catalog bound
  to the router (`api/internal/mcp/openapi*.go`) and served at
  `/api/v1/openapi.json`. Regenerate it with `make openapi` whenever a route
  or catalog entry changes; `TestOpenAPIGolden` fails when it is stale, and
  `make openapi` lists the operations still without a typed response. Typed
  success responses come from `successResponses` (`openapi_responses.go`):
  add an entry only after reading the handler, never guess one, and name the
  type the handler writes (no map literals, no copies); an error answer
  richer than the envelope goes in `operationErrors`, same rule. A
  generation failure at runtime only makes the endpoint answer 503. A route
  an agent must not call but the REST API documents goes in `openAPIRoutes`
  (and `notExposed`).
- Helm: `helm lint helm/monitoring-platform` and
  `helm template helm/monitoring-platform` (long output — write it to a file).
  After touching local AI values, templates, Compose, the Makefile or the
  start script, run `python .github/workflows/scripts/local_ai_helm_cases.py`
  and `python3 -I scripts/check-local-ai-packaging.py` (render cases, fail
  cases and Compose wiring; CI runs both in `local-ai-pr.yml`).
- Compose: `docker compose config` validates env wiring; for the release
  override, `docker compose -f docker-compose.yml -f docker-compose.release.yml
  config -q` under dummy values for its required variables, and
  `bash scripts/test-install.sh` (needs only the docker CLI). CI runs both
  (`Release Compose and installer` in `build-test.yml`), and `helm-lint.yml`
  renders four value sets and fails unless the migrations Job keeps its
  `post-install,pre-upgrade` hook.
- ai-service: from the repo root with the hash-locked deps installed
  (`docs/local-ai.md`), `PYTHONDONTWRITEBYTECODE=1
  PYTHONPATH=ai-service:ai-service/training python -m pytest ai-service/tests
  .github/workflows/scripts -q`. Real-base and real-checkpoint tests skip
  unless `PROBARA_LAYA_BASE` / `LOCAL_AI_ARTIFACT` are set, dataset-backed
  ones unless `python ai-service/training/generate.py --output
  ai-service/artifacts/dataset` has run (CI generates it first). Never run training,
  `validate_cpu.py` or the image build casually: they take hours or download
  the ~650 MB base.

## Local dev environment

- `make start-all-local` is the usual stack: Compose runs postgres
  (`probara`/`probara`, :5432), nats (:4222, monitoring :8222) and local-ai
  (:8090, below); the
  target applies migrations, builds the collector binaries, then runs the Go
  services as local processes (binaries in `/tmp/probara-bin`, logs in
  `/tmp/probara-<service>.log`) and the UI as `next dev`. HTTP/metrics
  ports: api 8080/9090, scheduler 8081/9091, status-page 8082/9093, worker
  8083/9092, alerter 8084/9094; web 3000. Stop and restart with the matching
  `stop-all-local` / `restart-all-local`. `make start-all` / `stop-all`
  instead run the backend in Compose (`docker-compose.yml` defines every
  service). Dex (test OIDC IdP, :5556) is opt-in in either mode:
  `docker compose --profile sso up -d dex`.
- AI analysis is local only and default-on: api and the DB-connected worker
  call `ai-service` at `LOCAL_AI_URL` with `LOCAL_AI_TOKEN` (`shared/config`
  `loadLocalAIConfig`); absent workspace `ai_settings` rows default enabled.
  Both `make start-all-local` and the Compose targets run it as the Compose
  service `local-ai` on `127.0.0.1:8090`, started after the platform (in the
  background for `start-all-local`) so a slow or failed image build never
  blocks it; `LOCAL_AI_DISABLED=true` skips it. Building the image needs the
  Git LFS head weights (`git lfs pull --include
  'ai-service/artifacts/*/head.safetensors'`); a local build is reused until
  `docker compose build local-ai`.
  The local-process stack keeps a per-checkout token in the gitignored
  `.dev-local-ai-token`; plain Compose falls back to a fixed, public
  development token (loopback-only). If the image cannot be pulled or built
  (the build fetches the pinned base from Hugging Face), AI reports
  unavailable and the platform runs on. `LLM_*` and tenant
  provider settings are retired.
- The start script refuses any Go except the minor on `go.mod`'s `go` line
  (1.26.x). With a newer local Go, prefix the command:
  `GOTOOLCHAIN=go1.26.5 make start-all-local` (the `toolchain` line in
  `go.mod`).
- Start env config from `.env.example`. If Postgres rejects the `probara`
  credentials after old experiments, `docker compose down -v` deletes the
  repo's volumes (all local data) so the database reinitializes.
- There are no default credentials: the first visit to the login page
  creates the first administrator. The helper tools (`scripts/verify-auth.sh`,
  `seed-demo`) assume `admin` / `change-me` unless `ADMIN_USER`/`ADMIN_PASS`
  say otherwise, so create that user on a dev stack. Default tenant ID
  `00000000-0000-0000-0000-000000000001`.
- API auth is cookie-based: POST `/api/v1/auth/login`, keep a cookie jar, and
  pass `tenant_id` as a query param on tenant-scoped routes.
- `make seed-demo` (re)builds the Northwind demo tenant
  (`00000000-0000-0000-0000-00000000d3e0`, ~150 monitors, 110 days of
  history) and leaves `seed-demo targets` running in the background (under
  `caffeinate -i` on macOS; mock ports 19100–19199 behind
  `*.northwind.localtest.me` hostnames, which need public DNS; three location
  workers; log `/tmp/probara-seed-targets.log`; stop with
  `make seed-demo-stop`). It only
  ever deletes its own tenant and `@northwind.example` users. Its history
  writer encodes the rollup/interval invariants in
  `scripts/seed-demo/README.md` — keep it in step when those rules change.
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
  `go test ./api/internal/services/agent/ -run TestGeneratedConfig`
  after `make build-collector-static` (it runs the real binary's `validate`
  on every OS variant of the generated config, then starts it and fails on
  any deprecation warning; it skips when the binary is not built).
- **Brand mark lives in four runtimes** and they must change together:
  `web/components/ui/BrandMark.tsx` (operator UI logo — sidebar, login,
  connect), `web/app/icon.svg` (dashboard favicon), `website/app/icon.svg`
  (landing/docs favicon), and an inline data-URI `<link rel="icon">` in
  `shared/statustemplate/default.gohtml` (status pages, monochrome variant
  that inverts with browser chrome; `classic.gohtml` carries the same URI,
  `TestClassicFaviconMatchesDefault`). Same trace geometry, four copies —
  status pages get a data URI because pages render under arbitrary domains
  and path prefixes (the push service worker is their one static route: its
  scope needs a real URL). Alert email is the deliberate exception:
  `shared/notifications/plugin/builtin/email/alert.gohtml`
  draws a bar trace out of table cells because Gmail drops `data:` image URIs
  and every client can block remote ones — a masthead that vanishes is worse
  than an approximation. Recolour it with the other four; do not give it an
  `<img>`.
- **Raw `check_results` live 30 days for every tenant**
  (`monitorstate.CheckResultsRawRetentionDays`, `shared/monitorstate/record.go`),
  whatever `data_retention_days` says; rollups are kept 400 days. Any read
  over a window longer than ~24h must use the rollups, never raw rows — the
  one exception is `metrics_data` (no rollup), read only through the API's
  downsampled `GET /monitors/{id}/metrics-snapshots`, clamped to the cap.
  Rollup rebuilds REPLACE a bucket from its raw rows, so a bucket must never
  be marked once any of its rows can be pruned: `Record` marks only from
  `RollupMarkHorizonSQL`, cleanup prunes whole hours below
  `RawPruneCutoffSQL` (one hour lower, same DB clock), and skips buckets with
  a pending mark (`checkResultNotPendingRollup`). Change the pair together.
- `expected_status` etc. reuse `HTTPStatus` fields for non-HTTP protocols
  (SIP status codes ride in `http_status`; for an INVITE it is the final
  INVITE code, the 18x in ring mode, never the BYE's).
- SIP dev against the host: `SIP_LOCALHOST_AS_HOST_GATEWAY=true` rewrites
  each loopback target host (configured host or SRV target) to
  `host.docker.internal` before resolving it — Compose worker only;
  `start-local-services.sh` (host processes) defaults it off.
- SIP resolves its own targets (`sip_resolve.go`: A/AAAA, or opt-in RFC
  3263 NAPTR (`sip_naptr.go`, best effort) and SRV with failover) into
  IP-literal candidates; each attempt dials one through `dialGuard`. SIP
  TLS identity is `tls_server_name` or the configured host, never an SRV
  target.
- SIP signalling rides one flow per attempt: every request, response and
  answer to a server-sent request goes over the connection `dialSIP` opened
  through `dialGuard` (`worker/internal/worker/sip_flow.go`); never dial a
  Contact, Via, Record-Route or SDP address for signalling (in a call's
  dialogs they only fill the Request-URI and Route headers). Call media
  writes RTP/RTCP only from the call's bound, unconnected pair, only to
  addresses `netguard.Policy.AllowsServerChosen` admits (`prepareMedia`, the
  latch; RTCP only to the media destination's IP),
  starts only at HOLDING entry and stops first thing in `teardown`, so no
  RTP follows a BYE; its error and warning text never names an address
  (`present.Build` copies it into every notification). Requests are client
  transactions in `sip_txn.go` (Timer E on UDP, matched by Via branch + CSeq
  method).
- INVITE responses reach `sipCall.onResponse` on the dispatcher goroutine,
  which sends every ACK itself (non-2xx in-transaction, each 2xx per To tag,
  re-sent for retransmissions) so ACKs go out during CANCEL/BYE waits and the
  post-result linger; keep them there. The 2xx ACK repeats the INVITE's
  credentials; CANCEL and the non-2xx ACK carry none.
- `alert_policies` are retired (API returns 410); workspace alert config
  lives on tenants via `/notification-settings`. No Go code names the
  retired tables (`alert_policies`, `alert_policy_channels`,
  `monitor_alert_policies`) or columns (`monitors.alert_policy_id`,
  `alerts.alert_policy_id`, `incidents.auto_alert_policy_id`) any more; the
  migration that drops them is pending for the next release (old pods still
  select them during a rolling deploy), so do not reintroduce a reference.
- **AI analysis timing**: the worker's stale-pending sweep
  (`worker/internal/airca/sweep.go`, 20 min, 30 min lock ceiling) must outlast
  one delivery (`LOCAL_AI_TIMEOUT_SECONDS` max 300 s + 30 s) and the
  unlocked unavailable-retry window (`unavailableRetryWindow`); compile-time
  guards there pin both, so raise them together. Analysis failure messages
  carry no trailing period or retry instruction: `AIRootCausePanel` renders
  "Analysis failed: <message>. Try again.".
  A local AI that cannot take a call right now returns `ai.ErrUnavailable`
  (`shared/ai/provider.go`: 429/502/503/504, refused/reset/DNS — not a
  model timeout); both job consumers NAK it with `deliveryRetryDelay`
  (`jobrun.go`, capped by `unavailableRetryMax`, which the relay's
  `triageFailAfter` guard counts) instead of recording a verdict.
- **AI triage timing**: the relay/consumer windows live as constants with
  compile-time guards in `worker/internal/airca/relay.go` and `triage.go`
  (15 s tick, 6 h lookback — the value is `ai.TriageLookback` in
  `shared/ai/settings.go`, aliased by relay.go's `triageLookback` — claim wait
  `GREATEST(delay, LEAST(2×interval, 1 h))`, 3 min republish, 30 min run timeout from `started_at`, not-started
  timeout = stream max age 1 h + 10 min). Republishes must land inside the
  `AI_TRIAGE` Duplicates window (30 min), and the relay and consumer must
  provision the stream through the same `triageSpec`/`ensureStream`, or one
  side's restart resets that window. Incident and alert evidence share
  `buildEvidence` in `context.go`; never fork the window/evidence SQL. The
  claim and cap SQL is `relayClaimSQL`; the settings ranges are pinned in
  migration 000094, `aisettings` and `web/lib/ai-triage.ts` together.
  Triage eligibility (open, lookback, live monitor, workspace AI +
  auto-triage on) is `ai.TriageClaimableAlertSQL`/`ai.TriageSettingsEnabledSQL`
  in `shared/ai/settings.go`; `relayClaimSQL` and `triageview.ClaimableExpr`
  (the SLA report's pending vs. untriaged split) both build from them — never
  re-type the clause. The SLA service takes the kill switches
  (`aiTriageOff`) and uses `FALSE` for claimable under them.
- **Local AI release gate**: `release_approved` comes only from
  `ai-service/training/validate_cpu.py`; `probara_ai/gate.py` is the
  predicate and `verify_artifact` recomputes it from `cpu-evaluation.json`,
  so editing the flag approves nothing. Never set it by hand. The image build
  fails for an unapproved shipped artifact (`ARG MODEL_VERSION` in
  `ai-service/Dockerfile`, which CI also reads), `docker-build` skips the
  `-local-ai` image, and `release.yml` refuses to tag while it is unapproved.
  `docker-build` builds `-local-ai` on release tags only, and after each
  tagged push checks the image is anonymously pullable (a new GHCR package
  starts private; `docs/local-ai.md` has the one-time fix). `release.yml`
  first runs `local_ai_artifact.py sources` (laya commit + HF revision still
  served); mirrors go in the `LAYA_GIT_URL` / `HF_ENDPOINT` build args.
- **Helm image tags default to the chart's `appVersion`**
  (`monitoring-platform.imageTag`, used by every Probara image): releases
  push each image under its version, while `latest` exists only for stable
  releases. Never default a tag to `latest`.
- **Local AI artifacts are head-only**: `artifacts/<version>/` commits
  `head.safetensors` plus metadata and `manifest.json`; tokenizer and encoder
  come from the base pinned in `ai-service/training/base-lock.json` (HF
  revision + per-file sha256, plus a `normalized` hash for the tokenizer
  config laya rewrites), fetched at build time by `training/upstream.py` and
  composed by `training/compose_artifact.py`, which refuses output differing
  from the manifest's `composed` hashes. Manifest `version`, `catalogue`,
  `state_format`, `base`, `upstream_commit` and `head_sha256` are immutable:
  change one and it is a new version.
- **Git LFS covers only `ai-service/artifacts/*/head.safetensors`**
  (`.gitattributes`). Every CI checkout uses `lfs: false`; only the
  `ai-checkpoint` job in `build-test.yml` (every head, cached) and an approved
  local-ai image build in `docker-build.yml` (tags) / `local-ai-pr.yml` (the
  shipped checkpoint's head only) run `git lfs pull --include` (bandwidth is
  metered). Keep new LFS patterns and pulls equally narrow.
- **Evidence `state_format` is pinned per artifact**: `contract.py`
  `STATE_FORMAT` versions the evidence-to-text rendering; the manifest's
  `state_format` (absent = 1) is what the runtime renders and what the
  validation corpus must use. Add a new format for any wording change, never
  edit an existing renderer (`training/history/` regenerates old corpora
  byte for byte).
- **Signed NATS structs** (`LocationHeartbeat`, `CheckResultMessage` and
  its `MeshResultInfo`) are verified by re-marshalling the receiver's struct
  (`locationauth.VerifyJSON`), so a new non-empty field breaks older
  verifiers (an omitempty field left empty does not). Extend inside
  `metrics_data` or reuse `LocationHeartbeat.Version`;
  `TestSignedNATSStructsFrozen` (`scheduler/internal/ingest`) pins them.
- **`monitor_state_intervals` is SLA history.** Nothing prunes it by time,
  and live SLA reports read it back as far as a period reaches (issued
  reports are frozen copies). Any future retention for it must keep what
  reports need, and state it in `website/lib/docs/pages/slas.ts`.
- **`maintenance_window_occurrences` is SLA history too.** Nothing prunes it;
  the coverage fragments stay cheap as it grows only because they start
  from the monitor's links and probe each window through
  `idx_maintenance_window_occurrences_window_end` (see `occurrencesSQL`;
  `TestCoverageQueriesStayIndexDrivenWithLongHistory` pins the plan). Never
  fold the link branches into one `OR` filter.
- **Ping** (`worker/internal/worker/ping_checker.go`) resolves through the
  SSRF policy (`netguard.Policy.ResolveAllowed`, via `dialGuard`) and sends
  only over unprivileged ICMP sockets. Those need the
  `net.ipv4.ping_group_range` sysctl: Helm sets it (`worker.pingGroupRange`,
  per-location override, `workerPodSecurityContext` helper); Docker 20.10+
  has it by default, and Compose and the location Docker snippets must
  never set it (a user-namespaced container refuses to start with it;
  `dockerDeploySnippets` test). Echo spacing and the prober's deadline are
  `models.PingMonitorConfig.PingSchedule` over the time left after
  resolving; the API's `ValidatePingSchedule` uses the same function.
- **Failed HTTP response capture** (`http_failure_capture.go`) is always on
  for non-success results with a response: redact the whole read body
  (`secretRedactor` + `redactCredentialFields`), then cut by encoded size;
  `captureMaxBytes` keeps it under 16 KiB, far below the AI evidence's 64 KiB
  `metrics_data` cutoff. Reads of many rows never ship it: the API's list,
  batch and snapshot reads go through `listMetricsDataSQL`
  (`services/results/failure_capture.go`, leaves
  `http.failure_response_omitted`), the status page and AI evidence drop
  `{http,failure_response}`; only `GetMonitorResult` returns it. A new
  bulk `metrics_data` read must do the same. Check results reach Postgres
  through `pgSafeJSON`/`pgSafeText` (`shared/monitorstate/pgsafe.go`), so
  NUL or invalid UTF-8 in any checker's text cannot fail an insert.
- **Which certificate a `tls_expiry` alert is about** rides its
  `last_error` (`notifications.FormatTLSExpiryError` /
  `ParseTLSExpiryError`, the alerts row carries it to every event);
  `present` names an intermediate from it, and `web/lib/tls-cert.ts` is the
  twin (`shared/notifications/testdata/tls_expiry_errors.json` pins both).
- **Path-borne credentials** (`/push/{token}`): the API's request log, panic
  log and HTTP metrics never use `r.URL.Path` raw — logs redact by the
  router's patterns, metrics label by route pattern
  (`api/internal/middleware/route_redaction.go`). Redaction keys on the
  parameter NAME, so a credential in a path must be a `{…token}`/`{…secret}`
  parameter, and a handler must not log or echo it.
- **Recovery confirmation** (S-T7): every writer of monitor or location
  state carries `consecutive_successes` and `recovery_pending` with the
  rest (reset with the failures; a down stream going `unknown` or moving
  to other locations keeps owing its confirmation — `AfterSilence`,
  `SetLocations`). Which monitors never get a fast recheck (suspect or
  recovering) is one table, `monitorstate.FastRecheckExempt`; a new type
  whose interval floor is a rate limit goes there.

## Commits and PRs

- Conventional commits (`fix(scope):`, `feat(scope):`) with an imperative
  one-line subject and a body explaining root cause and verification.
- PR descriptions state the behavior change, call out schema and env-var
  changes, link related issues, and include screenshots for `web/` or
  status-page UI changes. Run the checks that match the touched areas.
