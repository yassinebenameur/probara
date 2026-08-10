import type { DocPage } from '../types';

export const OPERATIONS_PAGE: DocPage = {
  slug: 'operations',
  group: 'Build & operate',
  title: 'Operations',
  description:
    'Operate Probara day to day: service lifecycle, health and readiness, Prometheus metrics, logs, migrations, retention, backup and restore, queue inspection, testing, and incident troubleshooting.',
  eyebrow: 'Operator handbook',
  readingTime: '20 min read',
  keywords: [
    'operations',
    'health',
    'readiness',
    'metrics',
    'backup',
    'retention',
    'troubleshooting',
    'testing',
  ],
  sections: [
    {
      id: 'service-dependencies',
      title: 'Service dependency map',
      blocks: [
        {
          type: 'table',
          columns: ['Service', 'Hard dependencies', 'Degraded behavior'],
          rows: [
            [
              'API',
              'PostgreSQL; valid port/JWT configuration',
              'NATS publication/subscription is optional unless private-location authorization is enabled. Run-now checks and live status/alert updates degrade without NATS.',
            ],
            [
              'Scheduler',
              'PostgreSQL and NATS',
              'No useful degraded mode. It schedules checks, ingests results, builds rollups, purges deleted monitors, enforces retention, and schedules mesh probes.',
            ],
            [
              'Worker',
              'NATS',
              'Can execute checks without PostgreSQL. Database-free location workers intentionally omit DB; AI-RCA and notification consumers require DB.',
            ],
            [
              'Alerter',
              'PostgreSQL',
              'NATS is optional for synchronous evaluation/dispatch, but required for async notification publication and alert-event fan-out.',
            ],
            [
              'Status page',
              'PostgreSQL',
              'NATS subscriber is optional; without it, cache invalidation waits for `STATUS_PAGE_CACHE_TTL`.',
            ],
            [
              'Frontend',
              'Reachable API proxy target',
              'Static shell may load, but authenticated product workflows fail when API is unreachable.',
            ],
            [
              'Host agent',
              'Reachable HTTPS API and valid API key',
              'Collects locally but cannot report while disconnected; remote disable is explicit opt-in.',
            ],
          ],
        },
      ],
    },
    {
      id: 'lifecycle',
      title: 'Start, stop, inspect, and restart',
      blocks: [
        {
          type: 'code',
          language: 'bash',
          title: 'Local-process workflow',
          code: `make start-all-local
make restart-all-local
make stop-all-local

tail -f /tmp/probara-*.log
tail -f /tmp/probara-ui.log`,
        },
        {
          type: 'code',
          language: 'bash',
          title: 'Docker-backed workflow',
          code: `make start-all
make ps
make logs
make logs-api
make logs-scheduler
make logs-worker
make logs-status
make restart-all
make stop-all`,
        },
        {
          type: 'code',
          language: 'bash',
          title: 'Kubernetes workload inspection',
          code: `kubectl -n probara get pods,deployments,statefulsets,jobs
kubectl -n probara get events --sort-by=.lastTimestamp
kubectl -n probara logs deployment/probara-api --all-containers --tail=200
kubectl -n probara logs deployment/probara-scheduler --all-containers --tail=200
kubectl -n probara rollout status deployment/probara-api
kubectl -n probara rollout status deployment/probara-worker`,
        },
        {
          type: 'callout',
          tone: 'warning',
          title: 'Use clean only when data loss is intended',
          text:
            '`make clean` runs `docker compose down -v` and deletes repository-scoped PostgreSQL, NATS, and artifact volumes. Back up first. Ordinary stops preserve volumes.',
        },
      ],
    },
    {
      id: 'health-readiness',
      title: 'Health, readiness, and listener truth',
      intro:
        '`/healthz` answers liveness, `/readyz` checks required dependencies, and `/metrics` exposes Prometheus data. The port carrying those routes differs by service.',
      blocks: [
        {
          type: 'table',
          columns: ['Service', 'Local/Compose URL', 'Liveness semantics', 'Readiness semantics'],
          rows: [
            [
              'API',
              '`http://localhost:8080`',
              'Process HTTP handler is alive.',
              'PostgreSQL responds within five seconds. NATS is not part of normal API readiness.',
            ],
            [
              'Scheduler',
              '`http://localhost:9091`',
              'Process operational listener is alive.',
              'Both PostgreSQL and NATS respond within five seconds.',
            ],
            [
              'Worker',
              '`http://localhost:9092`',
              'Healthy while NATS is connected or reconnecting; unhealthy after permanent closure.',
              'NATS responds, plus PostgreSQL when the worker was configured with DB access.',
            ],
            [
              'Alerter',
              '`http://localhost:9094`',
              'Alive; nil/optional NATS is accepted, but a permanently closed configured connection is unhealthy.',
              'PostgreSQL responds; NATS also responds when configured.',
            ],
            [
              'Status page',
              '`http://localhost:9093`',
              'Operational listener is alive.',
              'PostgreSQL responds within five seconds.',
            ],
            [
              'NATS',
              '`http://localhost:8222/healthz`',
              'Broker monitor endpoint.',
              'Use JetStream/consumer checks in addition to HTTP liveness.',
            ],
          ],
        },
        {
          type: 'code',
          language: 'bash',
          title: 'Probe manually',
          code: `curl -fsS http://localhost:8080/healthz
curl -fsS http://localhost:8080/readyz
curl -fsS http://localhost:9091/readyz
curl -fsS http://localhost:9092/readyz
curl -fsS http://localhost:9093/readyz
curl -fsS http://localhost:9094/readyz

make healthcheck`,
        },
        {
          type: 'callout',
          tone: 'warning',
          title: 'Some declared ports have no listener',
          text:
            'API serves health, readiness, and metrics on `HTTP_PORT` 8080; the mapped/chart metrics port 9090 is not a separate API server. Scheduler listens only on metrics port 9091 despite Compose mapping 8081. Alerter listens only on 9094. Scrape and probe the endpoints that actually answer.',
        },
        {
          type: 'paragraph',
          text:
            'Worker also serves `/mesh/echo` on the metrics listener. When its `HTTP_PORT` differs, it starts a second listener containing only mesh echo and `/healthz`, allowing mesh exposure without publishing `/metrics`.',
        },
      ],
    },
    {
      id: 'metrics',
      title: 'Prometheus metrics',
      blocks: [
        {
          type: 'paragraph',
          text:
            'Custom metrics use namespace `probara` and a sanitized service subsystem, for example `probara_scheduler_loops_total`. Go runtime and process collectors are also registered.',
        },
        {
          type: 'table',
          columns: ['Area', 'Key metrics', 'What to alert on'],
          rows: [
            [
              'Scheduler loop',
              '`probara_scheduler_loops_total`, `monitors_scheduled_total`, `loop_duration_seconds`, `monitors_in_batch`',
              'No loop increments, duration approaching schedule interval, or sustained full batches.',
            ],
            [
              'Scheduler failures',
              '`jobs_publish_errors_total`, `db_errors_total`, `mesh_publish_errors_total`',
              'Any sustained nonzero rate.',
            ],
            [
              'Result ingest',
              '`results_ingested_total`, `result_ingest_errors_total`, `result_ingest_duplicates_total`, `mesh_results_ingested_total`',
              'Publish success without ingest growth, ingest errors, or unexpectedly high duplicates.',
            ],
            [
              'Retention and rollups',
              '`retention_cleanup_runs_total`, `retention_cleanup_rows_total`, `rollup_runs_total`, `rollup_rows_total`, `rollup_errors_total`, `rollup_rows_skipped_total`, `rollup_duration_seconds`, `rollup_cursor_unix`',
              'Stale cursor, error growth, poison-row skips, or runs exceeding the maintenance window.',
            ],
            [
              'Monitor purge',
              '`monitor_purge_runs_total`, `monitor_purge_rows_total`, `monitor_purge_monitors_total`, `monitor_purge_errors_total`, `monitor_purge_run_duration_seconds`',
              'Growing errors or deleted monitors never becoming fully purged.',
            ],
            [
              'Worker execution',
              '`probara_worker_jobs_total{status=…}`, `job_duration_seconds`, `http_request_duration_seconds`, `http_errors_total{reason=…}`',
              'Failure-rate changes, high latency, timeouts, and policy blocks.',
            ],
            [
              'Worker NATS/results',
              '`nats_ack_total`, `nats_nak_total`, `result_publish_errors_total`',
              'NAK or publish-error growth and falling ACK throughput.',
            ],
            [
              'Status live updates',
              '`probara_status_page_sse_subscriber_connected`',
              'Zero when low-latency status invalidation is expected.',
            ],
          ],
        },
        {
          type: 'code',
          language: 'bash',
          title: 'Inspect metrics locally',
          code: `curl -fsS http://localhost:8080/metrics | grep '^probara_'
curl -fsS http://localhost:9091/metrics | grep '^probara_scheduler_'
curl -fsS http://localhost:9092/metrics | grep '^probara_worker_'
curl -fsS http://localhost:9093/metrics | grep '^probara_status_page_'`,
        },
        {
          type: 'callout',
          tone: 'warning',
          title: 'Verify chart scrape annotations against real ports',
          text:
            'The chart applies a default Prometheus annotation for port 9090 to workloads. That is correct for most chart metrics listeners, but API metrics are actually served on its HTTP port. Prefer ServiceMonitor/PodMonitor resources with per-component ports after verifying responses.',
        },
      ],
    },
    {
      id: 'logs',
      title: 'Logs and diagnostic context',
      blocks: [
        {
          type: 'list',
          items: [
            'Use `LOG_LEVEL=debug` temporarily for connection, scheduling, and readiness diagnosis; restore `info` after the incident.',
            'Correlate monitor ID, tenant ID, location ID, alert ID, NATS stream/subject, and timestamps across services.',
            'Never paste location-authenticated NATS URLs, API keys, SMTP passwords, webhook secrets, OIDC secrets, JWT secrets, or encrypted configuration keys into tickets or chat.',
            'A warning that status-update publishing failed means core CRUD may work while public live invalidation is degraded.',
            'A scheduler warning that `RESULT_INGEST_ENABLED=false` means workers can run checks but results will not enter PostgreSQL.',
            'A plaintext/NoOp encryption warning requires immediate review before storing or updating sensitive configurations.',
          ],
        },
        {
          type: 'code',
          language: 'bash',
          title: 'Focused Compose logs',
          code: `docker compose logs --since=15m api scheduler worker alerter status-page
docker compose logs -f --tail=200 scheduler worker`,
        },
      ],
    },
    {
      id: 'database-migrations',
      title: 'Database migrations and administrator bootstrap',
      blocks: [
        {
          type: 'code',
          language: 'bash',
          title: 'Run migrations',
          code: `make migrate

# Direct local migration command
POSTGRES_URL='postgres://probara:probara@localhost:5432/probara?sslmode=disable' \\
MIGRATIONS_PATH='./shared/db/migrations' \\
go run ./cmd/migrate`,
        },
        {
          type: 'paragraph',
          text:
            'The migration CLI requires `POSTGRES_URL`, defaults `MIGRATIONS_PATH` to `./migrations`, and retries the initial database connection up to 30 times at one-second intervals. API startup also runs the shared migration set. The [Helm migration Job](/docs/deployment/#migrations-upgrades) is a post-install/post-upgrade hook.',
        },
        {
          type: 'code',
          language: 'bash',
          title: 'Create or update the initial administrator',
          code: `POSTGRES_URL='postgres://probara:probara@localhost:5432/probara?sslmode=disable' \\
ADMIN_USERNAME='admin' \\
ADMIN_PASSWORD='use-a-password-manager-generated-value' \\
ADMIN_BCRYPT_COST='12' \\
go run ./cmd/admin`,
        },
        {
          type: 'callout',
          tone: 'warning',
          title: 'Helm initialApiKey is not a bootstrap mechanism',
          text:
            'The chart currently stores and prints `secrets.initialApiKey`, but no job or service creates a matching database API key. Bootstrap an administrator and create API keys through the authenticated product/API.',
        },
      ],
    },
    {
      id: 'backup-restore',
      title: 'Backup and restore',
      blocks: [
        {
          type: 'code',
          language: 'bash',
          title: 'Compose PostgreSQL backup and restore',
          code: `make db-backup       # writes ./backup.sql

# Inspect and copy the backup away from the workstation before maintenance.
ls -lh backup.sql

# Restore into the configured Compose database.
make db-restore`,
        },
        {
          type: 'list',
          ordered: true,
          items: [
            'Quiesce writes or capture a database-consistent snapshot appropriate to your PostgreSQL topology.',
            'Back up PostgreSQL, including schema migration state, tenants, monitor configuration, alert state, users, API keys, audit data, and encrypted ciphertext.',
            'Back up the complete [encryption keyring](/docs/security/#encryption-at-rest) separately. A database backup without the historical keys may be unrecoverable.',
            'Protect JetStream state or accept that queued checks/results/notifications may be replayed or lost after recovery.',
            'Preserve browser artifacts separately if they are part of your incident evidence policy.',
            'Restore into an isolated environment, run readiness and data-integrity checks, then perform a documented cutover.',
          ],
        },
        {
          type: 'callout',
          tone: 'warning',
          title: 'Test restores, not only backups',
          text:
            '`make db-restore` sends `backup.sql` directly into the local database and may conflict with existing objects/data. Confirm the target, take a fresh backup, and prefer an isolated empty database for recovery drills.',
        },
        {
          type: 'paragraph',
          text:
            'The bundled PostgreSQL and NATS StatefulSets use PVCs but provide no scheduled backup controller, point-in-time recovery, replication, or cross-zone failover. Production recovery objectives require managed services, an operator, or external backup automation.',
        },
      ],
    },
    {
      id: 'retention',
      title: 'Retention, rollups, and deletion',
      blocks: [
        {
          type: 'definitions',
          items: [
            {
              term: 'Tenant telemetry retention',
              description:
                'Each tenant’s `data_retention_days` controls raw check results and mesh results. A value of `0` preserves them; the public validator otherwise accepts 30–3650 days. It is configured in [tenant settings](/docs/administration/#tenant-settings).',
            },
            {
              term: 'Audit retention',
              description:
                '`AUDIT_RETENTION_DAYS` is a separate compliance control, defaults to 365 days, and uses `0` to retain forever.',
            },
            {
              term: 'Monitor purge',
              description:
                'Soft-deleted monitors are later purged with dependent rows in bounded batches.',
            },
            {
              term: 'Rollups',
              description:
                'Scheduler maintenance processes result history into aggregate data and tracks a cursor; poisoned rows may be skipped and counted.',
            },
          ],
        },
        {
          type: 'paragraph',
          text:
            'Scheduler checks once per minute whether the configured UTC cleanup hour is due, runs at most once per UTC date, uses a PostgreSQL advisory lock, and applies a 30-minute cleanup timeout. Batch size and maximum rows per run bound database pressure.',
        },
        {
          type: 'table',
          columns: ['Control', 'Default', 'Operator effect'],
          rows: [
            ['`RETENTION_CLEANUP_ENABLED`', '`true`', 'Enable daily tenant telemetry cleanup.'],
            ['`RETENTION_CLEANUP_HOUR_UTC`', '`2`', 'Daily target hour, 0–23 UTC.'],
            ['`RETENTION_CLEANUP_BATCH_SIZE`', '`5000`', 'Delete batch size.'],
            ['`RETENTION_CLEANUP_MAX_ROWS_PER_RUN`', '`200000`', 'Per-run cap; backlog can require multiple days/runs.'],
            ['`AUDIT_RETENTION_DAYS`', '`365`', 'Separate hourly API audit pruner; `0` disables pruning.'],
            ['`MONITOR_PURGE_INTERVAL_SECONDS`', '`30`', 'Soft-delete purge cadence.'],
            ['`MONITOR_PURGE_BATCH_SIZE`', '`5000`', 'Child-row purge batch size.'],
            ['`MONITOR_PURGE_MAX_ROWS_PER_RUN`', '`200000`', 'Purge safety cap.'],
          ],
        },
        {
          type: 'callout',
          tone: 'info',
          title: 'Lower retention gradually',
          text:
            'A large policy reduction can create a deletion backlog and heavy database I/O. Observe retention duration, deleted rows, replication lag, locks, and disk reclamation; tune batches/caps rather than removing safeguards.',
        },
      ],
    },
    {
      id: 'nats-jetstream',
      title: 'NATS and JetStream operations',
      blocks: [
        {
          type: 'table',
          columns: ['Contract', 'Code default', 'Purpose'],
          rows: [
            ['`CHECK_JOBS` / `check.jobs`', 'Work-queue stream/subject', 'Scheduler and API publish; workers consume.'],
            ['`CHECK_RESULTS` / `check.results`', 'Work-queue stream/subject', 'Workers publish; scheduler persists.'],
            ['`ALERTS` / `alerts`', 'Alert event stream/subject', 'Alerter publishes; API live subscriber consumes.'],
            ['`NOTIFICATIONS` / `alerts.dispatch.>`', 'Optional work queue', 'Async alerter-to-worker dispatch.'],
            ['`AI_RCA` / `ai.rca.jobs`', 'AI work queue', 'API publishes tenant RCA work; DB-connected workers consume.'],
            ['Core NATS `statuspage.updates`', 'Non-JetStream fan-out', 'Cache/status update invalidation.'],
          ],
        },
        {
          type: 'code',
          language: 'bash',
          title: 'Inspect with the NATS CLI',
          code: `nats --server "$NATS_URL" server check connection
nats --server "$NATS_URL" stream ls
nats --server "$NATS_URL" stream info CHECK_JOBS
nats --server "$NATS_URL" consumer ls CHECK_JOBS
nats --server "$NATS_URL" stream info CHECK_RESULTS
nats --server "$NATS_URL" consumer info CHECK_RESULTS result-ingest`,
        },
        {
          type: 'list',
          items: [
            'Compare the actual stream subjects with every publisher and consumer environment before deleting or recreating streams.',
            'Watch pending, redelivered, and unacknowledged counts; queue depth without scheduler/worker progress indicates a contract or connectivity failure.',
            '`CHECK_JOB_LEGACY_CONSUMERS` causes scheduler startup to delete named filterless consumers. Review the list before renaming durable consumers.',
            'JetStream PVC persistence does not replace a broker backup/replication strategy.',
            'Core NATS status updates are ephemeral by design; status cache TTL is the fallback.',
          ],
        },
      ],
    },
    {
      id: 'artifacts',
      title: 'Synthetic-browser artifacts',
      blocks: [
        {
          type: 'list',
          items: [
            'Workers write failure screenshots beneath `SYNTHETIC_BROWSER_ARTIFACTS_DIR`; API reads them for authenticated retrieval. Trace and HAR requests currently emit warnings and do not create artifacts.',
            'Compose mounts a shared named volume into API and worker.',
            'Local-process services share the same host directory.',
            'The current Helm workloads do not mount shared artifact storage, so cross-pod and cross-node retrieval is unreliable.',
            'Set retention and [access controls](/docs/security/#status-pages-artifacts) appropriate for screenshots because they can capture page content, tokens, personal data, or internal application state.',
          ],
        },
        {
          type: 'callout',
          tone: 'warning',
          title: 'Treat artifacts as sensitive evidence',
          text:
            'Do not place the artifact directory on a public static volume. Encrypt storage, restrict API access, define deletion policy, and avoid collecting secrets in synthetic scripts.',
        },
      ],
    },
    {
      id: 'testing',
      title: 'Verification and tests',
      blocks: [
        {
          type: 'code',
          language: 'bash',
          title: 'Main Go module',
          code: `make test
make lint

# make fmt is a check, not a formatter.
gofmt -w path/to/changed.go
make fmt
make vet`,
        },
        {
          type: 'code',
          language: 'bash',
          title: 'Nested agent, frontend, and chart',
          code: `(cd agent && go test ./...)

(cd web && npm run lint && npm run build)
(cd website && npm run typecheck && npm run build)

helm lint ./helm/monitoring-platform \\
  --set secrets.adminJwtSecret='replace-with-32-plus-char-secret-value' \\
  --set api.publicBaseURL='https://probara.example.com'`,
        },
        {
          type: 'list',
          items: [
            '`make test` runs the main module with race detection and writes `coverage.out`; it excludes script packages and the nested agent module.',
            'Database integration tests use Testcontainers and require a working Docker daemon capable of starting PostgreSQL 16.',
            'Run `make test-cover` to open the HTML coverage report.',
            'Frontend changes require both lint and a production build; documentation site changes should at least run `npm run typecheck` and `npm run build` from `website/`.',
            'Helm lint requires the chart’s mandatory JWT and public base URL values.',
            'For a release candidate, verify scheduled checks, on-demand checks, all touched monitor types, result ingest, alerts, notification delivery, status pages, OIDC, imports, private locations, and backup restore.',
          ],
        },
      ],
    },
    {
      id: 'troubleshooting',
      title: 'Troubleshooting guide',
      blocks: [
        {
          type: 'table',
          columns: ['Symptom', 'Likely cause', 'Checks and resolution'],
          rows: [
            [
              'Configuration exits immediately',
              'Missing ports/JWT, short or placeholder JWT, invalid bool/int/CIDR, incomplete OIDC, or location ID/credential mismatch',
              'Read the first fatal line; compare the process environment with the [configuration reference](/docs/configuration/).',
            ],
            [
              'Compose command fails before containers start',
              'Required `${ADMIN_JWT_SECRET:?…}` or `${PUBLIC_BASE_URL:?…}` interpolation is missing',
              'Populate `.env` before any Compose-backed Make target.',
            ],
            [
              'PostgreSQL authentication fails after old local experiments',
              'Existing volume was initialized with different credentials',
              'Back up first. If disposable, `docker compose down -v` reinitializes all repo-scoped volumes and destroys local data.',
            ],
            [
              'Scheduled checks work, Run now does not',
              'API publishes on a different `CHECK_JOB_SUBJECT` than the workers consume (a custom override applied to only some services)',
              'Align `CHECK_JOB_SUBJECT` and stream configuration on API, scheduler, and worker; the shipped Compose file and Helm chart set all three identically. Inspect actual JetStream subjects.',
            ],
            [
              'Checks execute but no history appears',
              'Result-ingest disabled, scheduler unavailable, result subject mismatch, DB failure, or ingest backlog',
              'Check scheduler readiness/logs and `results_ingested_total` / `result_ingest_errors_total`; inspect `CHECK_RESULTS` consumer state.',
            ],
            [
              'Private location does not start',
              'Missing credential, invalid UUID, unsafe public NATS URL, broker authentication/TLS issue, or no external broker route',
              'Regenerate deployment info; require both location ID and credential; test the TLS/WSS endpoint from that network.',
            ],
            [
              'Encrypted monitor fails only in scheduler',
              'Scheduler lacks the current/historical encryption keyring',
              'Provide the identical `PROBARA_SECRETS_KEY*` set to scheduler. The shipped Compose file and Helm chart wire the base key; add rotation keys (`_V2`+) through the chart\'s top-level `extraEnv` so every service receives the same keyring.',
            ],
            [
              'Browser screenshot is 404/missing',
              'API and worker use different filesystems/pods or artifact expired',
              'Verify identical artifact directory and shared storage. Helm does not currently provide it.',
            ],
            [
              'Email fails on port 587',
              '`SMTP_USE_TLS=true` selects implicit TLS rather than STARTTLS',
              'Confirm provider protocol. Use the correct implicit-TLS port or a supported non-TLS/private relay setting.',
            ],
            [
              'OIDC redirects or callback fails',
              'Issuer/client secret/callback mismatch, bad public base URL, or JIT tenant/role error',
              'Compare the exact HTTPS callback with the IdP registration and inspect OIDC discovery.',
            ],
            [
              'SSO user loses roles (or gains none) after login',
              'OIDC group mappings exist but the ID token carries no groups claim — `groups` scope not requested, wrong `OIDC_GROUPS_CLAIM`, or the IdP does not embed groups in the ID token',
              'Check API logs for the missing-groups warning, add `groups` to `OIDC_SCOPES`, and verify the [group-mapping requirements](/docs/administration/#oidc-group-mappings). Deleting all mappings restores manual role management.',
            ],
            [
              'Status page updates slowly',
              'NATS status subscriber disconnected or update subject differs',
              'Check `sse_subscriber_connected`, subscriber logs, shared subject, and cache TTL.',
            ],
            [
              'Frontend links point to old origin',
              '`NEXT_PUBLIC_*` changed only at runtime',
              'Rebuild the frontend client bundle with the desired public values or use stable relative routing.',
            ],
            [
              'Agent container reports literal placeholders',
              'JSON-form CMD does not expand environment variables',
              'Pass `-backend-url`, `-agent-id`, and `-api-key` CLI flags explicitly.',
            ],
            [
              'Prometheus cannot scrape API on 9090',
              'API has no separate metrics listener',
              'Scrape `/metrics` on API HTTP port 8080 or fix the workload/service topology.',
            ],
          ],
        },
      ],
    },
    {
      id: 'incident-checklist',
      title: 'Incident response checklist',
      blocks: [
        {
          type: 'list',
          ordered: true,
          items: [
            'Identify the affected tenant, monitor/location, time window, and whether the failure is control plane, check execution, result persistence, alerting, or presentation.',
            'Check liveness and readiness on the real service listeners; preserve logs and relevant metrics before restarting.',
            'Inspect PostgreSQL availability and NATS stream/consumer state without deleting consumers or messages.',
            'Compare deployed queue variables and encryption-key versions across all replicas.',
            'Reduce blast radius with reversible actions: pause a broken monitor/policy, scale a healthy worker fleet, or disable optional async paths.',
            'Restore service, verify end-to-end from schedule through status/alert delivery, then reconcile delayed or duplicate jobs.',
            'Record the operator action in [incident notes](/docs/alerting/#incidents) and retain the audit/log/artifact evidence according to policy.',
          ],
        },
      ],
    },
  ],
};
