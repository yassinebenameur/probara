import type { DocPage } from '../types';

export const CONFIGURATION_PAGE: DocPage = {
  slug: 'configuration',
  group: 'Build & operate',
  title: 'Configuration variables',
  description:
    'A complete reference for Probara service, frontend, agent, maintenance, and deployment environment variables, including code defaults and the variables that Docker Compose and Helm actually wire.',
  eyebrow: 'Configuration reference',
  readingTime: '24 min read',
  keywords: [
    'environment variables',
    'configuration',
    'Docker Compose',
    'Helm',
    'OIDC',
    'NATS',
    'SMTP',
    'LLM',
  ],
  sections: [
    {
      id: 'configuration-model',
      title: 'How configuration is resolved',
      intro:
        'Each Go process reads environment variables directly. A code default only applies after the variable reaches the process; [Docker Compose](/docs/deployment/#docker-compose) and the [Helm chart](/docs/deployment/#helm-install) expose smaller, different subsets of the complete runtime surface.',
      blocks: [
        {
          type: 'definitions',
          items: [
            {
              term: 'Code default',
              description:
                'The value selected by `shared/config` when the process environment does not contain the variable.',
            },
            {
              term: 'Docker wiring',
              description:
                'A variable explicitly listed beneath a service in `docker-compose.yml`. The root `.env` file is used for Compose interpolation, but it is not passed wholesale into containers.',
            },
            {
              term: 'Helm wiring',
              description:
                'A chart value rendered into a workload environment. The chart has no general `extraEnv` escape hatch in the current dev state.',
            },
            {
              term: 'Local-process wiring',
              description:
                'Defaults exported by `scripts/start-local-services.sh` before it launches locally built binaries.',
            },
          ],
        },
        {
          type: 'callout',
          tone: 'warning',
          title: 'A populated .env file does not configure every Docker service',
          text:
            'Compose has no `env_file` declaration. LLM, SMTP, notification, mesh, purge, and many retention variables in your shell or `.env` have no container effect unless `docker-compose.yml` explicitly forwards them. `PROBARA_SECRETS_KEY` is forwarded to the api, scheduler, worker, and alerter services.',
        },
        {
          type: 'callout',
          tone: 'warning',
          title: 'Keep queue names identical across publishers and consumers',
          text:
            'The code defaults are `CHECK_JOBS` and `check.jobs`, while the shipped Compose and Helm values are `check-jobs` and `check.job` — set identically on the API, scheduler, and workers so on-demand “run now” jobs land where workers consume them. If you override either value, treat stream and subject as one cross-service contract and change them everywhere at once.',
        },
      ],
    },
    {
      id: 'common-service-variables',
      title: 'Common service variables',
      blocks: [
        {
          type: 'paragraph',
          text:
            'Where a table row below says a variable is “not exposed” by a deployment method, it means there is no dedicated Compose declaration or Helm value for it. In Helm, any such variable can still be injected without template changes via `extraEnv` (all workloads) or `<service>.extraEnv` — see [Helm values](/docs/deployment/#helm-values-auth-services). In Compose, add it to the service `environment` block explicitly.',
        },
        {
          type: 'table',
          columns: ['Variable', 'Code default / validation', 'Used by', 'Deployment notes'],
          rows: [
            [
              '`HTTP_PORT`',
              'Required integer; no code default',
              'Every Go service',
              'Required even for scheduler and alerter, whose operational endpoint is normally the metrics listener.',
            ],
            [
              '`METRICS_PORT`',
              'Required integer; no code default',
              'Every Go service',
              'Hosts `/healthz`, `/readyz`, and `/metrics` for scheduler, worker, alerter, and the status service.',
            ],
            [
              '`LOG_LEVEL`',
              '`info`',
              'Every Go service',
              'Compose and Helm set this explicitly. Use a supported structured logger level such as `debug`, `info`, `warn`, or `error`.',
            ],
            [
              '`POSTGRES_URL`',
              'Empty',
              'API, scheduler, worker, alerter, status page, CLIs',
              'Required by API, scheduler, alerter, and status page. Optional for workers; a worker without it cannot run notification or AI-RCA side consumers.',
            ],
            [
              '`NATS_URL`',
              '`nats://localhost:4222`',
              'API, scheduler, worker, alerter, status page updates',
              'Scheduler and worker require NATS. API and status live-update paths degrade when it is unavailable; alerter can run synchronous dispatch without it.',
            ],
          ],
        },
        {
          type: 'callout',
          tone: 'info',
          title: 'Validation is intentionally narrow',
          text:
            'The common loader checks that both port values are integers but does not enforce a positive range or detect port collisions. Deployment manifests remain responsible for supplying usable, distinct ports.',
        },
      ],
    },
    {
      id: 'api-authentication',
      title: 'API authentication, sessions, and audit',
      blocks: [
        {
          type: 'table',
          columns: ['Variable', 'Code default / validation', 'Docker / Helm wiring'],
          rows: [
            [
              '`ADMIN_JWT_SECRET`',
              'Required; validation only rejects an empty value. Use at least 32 random characters.',
              'Compose ships an insecure hardcoded default and the Helm values ship a placeholder default; override both for production.',
            ],
            [
              '`ADMIN_ACCESS_TTL_MINUTES`',
              '`15`; integer',
              'Local-process script sets it. Compose and Helm rely on the code default.',
            ],
            [
              '`ADMIN_REFRESH_TTL_DAYS`',
              '`30`; integer',
              'Local-process script sets it. Compose and Helm rely on the code default.',
            ],
            [
              '`ADMIN_COOKIE_SECURE`',
              '`false`; boolean',
              'Local-process script sets `false`. Compose and Helm do not expose it; production HTTPS deployments should wire `true`.',
            ],
            [
              '`ADMIN_BCRYPT_COST`',
              '`12`; integer',
              'Not exposed by Compose or Helm. Also used by the admin creation CLI.',
            ],
            [
              '`AUDIT_RETENTION_DAYS`',
              '`365`; nonnegative integer; `0` keeps records forever',
              'Compose and Helm expose the audit-retention setting; the API owns pruning.',
            ],
          ],
        },
        {
          type: 'paragraph',
          text:
            'The access cookie is HttpOnly and SameSite=Lax. `ADMIN_COOKIE_SECURE` controls its Secure attribute. Access and refresh TTL values are only parsed as integers; the loader does not reject zero or negative values, so use deliberate positive settings.',
        },
      ],
    },
    {
      id: 'public-urls-and-private-location-auth',
      title: 'Public URLs and private-location authorization',
      blocks: [
        {
          type: 'table',
          columns: ['Variable', 'Code default / validation', 'Purpose and caveats'],
          rows: [
            [
              '`PUBLIC_BASE_URL`',
              'Empty; surrounding whitespace and trailing slash removed',
              'Externally reachable API origin used in agent installers, push webhooks, and the default OIDC callback. Compose and Helm require it for normal deployment.',
            ],
            [
              '`PUBLIC_NATS_URL`',
              'Empty',
              'Broker address embedded in private-location deployment instructions. When deployment information is generated, only `tls://` and `wss://` URLs are accepted; pre-existing URL credentials are rejected.',
            ],
            [
              '`NATS_LOCATION_AUTH_ISSUER_SEED`',
              'Empty',
              'Required whenever `PUBLIC_NATS_URL` is nonempty. It must be a NATS account seed for the API-hosted authorization callout.',
            ],
            [
              '`WORKER_LOCATION_ID`',
              'Empty or UUID',
              'Pins a worker to one private location. Empty selects the default platform worker fleet.',
            ],
            [
              '`LOCATION_CREDENTIAL`',
              'Empty',
              'Required if and only if `WORKER_LOCATION_ID` is set. Generated credentials are URL-safe base64 for 32 random bytes.',
            ],
          ],
        },
        {
          type: 'callout',
          tone: 'warning',
          title: 'A public URL does not expose NATS for you',
          text:
            'The embedded Helm NATS Service is ClusterIP-only, and local Compose NATS is unauthenticated plaintext. Production private locations need an independently exposed [TLS/WSS broker path](/docs/security/#private-locations-nats), authentication enabled, and the issuer/key configuration shared with the API.',
        },
      ],
    },
    {
      id: 'oidc',
      title: 'OIDC single sign-on',
      blocks: [
        {
          type: 'table',
          columns: ['Variable', 'Default / validation', 'Description'],
          rows: [
            ['`OIDC_ENABLED`', '`false`; boolean', 'Enables platform-wide OIDC discovery and login.'],
            [
              '`OIDC_ISSUER_URL`',
              'Required when enabled',
              'Issuer URL used for OIDC discovery.',
            ],
            [
              '`OIDC_CLIENT_ID`',
              'Required when enabled',
              'Registered OAuth/OIDC client identifier.',
            ],
            [
              '`OIDC_CLIENT_SECRET`',
              'Required when enabled',
              'Confidential client secret; store in a secret manager or Kubernetes Secret.',
            ],
            [
              '`OIDC_REDIRECT_URL`',
              'Defaults to `PUBLIC_BASE_URL` + `/api/v1/auth/oidc/callback`',
              'Must be supplied explicitly if `PUBLIC_BASE_URL` is empty.',
            ],
            [
              '`OIDC_SCOPES`',
              '`openid profile email`',
              'Whitespace-separated scopes.',
            ],
            ['`OIDC_PROVIDER_LABEL`', '`SSO`', 'Human-readable login-provider label.'],
            [
              '`OIDC_JIT_PROVISION`',
              '`true`; boolean',
              'Creates a local user record for a valid first-time OIDC identity.',
            ],
            [
              '`OIDC_JIT_DEFAULT_ROLE`',
              '`viewer`; one of `admin`, `editor`, `viewer`',
              'Default tenant role for JIT users.',
            ],
            [
              '`OIDC_JIT_DEFAULT_TENANT_ID`',
              '`00000000-0000-0000-0000-000000000001`',
              'Tenant receiving JIT users. The loader does not validate UUID syntax; verify it refers to the intended tenant.',
            ],
          ],
        },
        {
          type: 'paragraph',
          text:
            'Compose includes development-oriented Dex defaults under its optional profile. Helm exposes the platform OIDC settings and stores the client secret separately. In production, register an exact HTTPS callback and choose the [least-privileged JIT role](/docs/security/#oidc-security).',
        },
      ],
    },
    {
      id: 'encryption-keys',
      title: 'Encryption-at-rest keys',
      blocks: [
        {
          type: 'table',
          columns: ['Variable', 'Format', 'Behavior'],
          rows: [
            [
              '`PROBARA_SECRETS_KEY`',
              'Base64 encoding of exactly 32 bytes',
              'Version 1 key for encrypted channel, monitor, location, and AI credentials.',
            ],
            [
              '`PROBARA_SECRETS_KEY_V2` … `PROBARA_SECRETS_KEY_V100`',
              'Each is base64 encoding of exactly 32 bytes',
              'Optional rotation keys. The highest configured version encrypts new writes; older keys remain available for decryption.',
            ],
          ],
        },
        {
          type: 'callout',
          tone: 'warning',
          title: 'Every data-handling service needs the same keyring',
          text:
            'API, scheduler, worker, and alerter can all read [encrypted configuration](/docs/security/#encryption-at-rest). Missing keys may allow new plaintext operation in some direct runtimes, but they cannot decrypt existing ciphertext. The current Helm chart omits the key from the scheduler deployment.',
        },
        {
          type: 'code',
          language: 'bash',
          title: 'Generate a 32-byte base key',
          code: `openssl rand -base64 32`,
        },
      ],
    },
    {
      id: 'api-queue-ai-artifacts',
      title: 'API queues, AI fallback, mesh, and artifacts',
      blocks: [
        {
          type: 'table',
          columns: ['Variable', 'Code default', 'Use / deployment status'],
          rows: [
            [
              '`ALERT_STREAM`',
              '`ALERTS`',
              'Loaded by the API but not used by a current API runtime path.',
            ],
            [
              '`ALERT_SUBJECT`',
              '`alerts`',
              'API alert live-update subscriber subject.',
            ],
            [
              '`ALERT_CONSUMER_NAME`',
              '`api-alerts`',
              'Loaded but not currently used.',
            ],
            [
              '`CHECK_JOB_STREAM`',
              '`CHECK_JOBS`',
              'NATS authorization permissions and job topology. Compose and Helm pass the scheduler stream to API.',
            ],
            [
              '`CHECK_JOB_SUBJECT`',
              '`check.jobs`',
              'On-demand check publication. Compose and Helm pass the scheduler/worker subject to API; keep all three aligned.',
            ],
            [
              '`CHECK_RESULT_SUBJECT`',
              '`check.results`',
              'Private-location result permission and result contract.',
            ],
            [
              '`AI_RCA_SUBJECT`',
              '`ai.rca.jobs`',
              'AI root-cause job publication; must match worker.',
            ],
            [
              '`MESH_PROBE_INTERVAL_SECONDS`',
              '`30`; positive integer',
              'Mirrors scheduler cadence so API staleness calculations agree.',
            ],
            [
              '`MESH_PROBE_TIMEOUT_SECONDS`',
              '`5`; positive integer',
              'Mirrors scheduler probe timeout.',
            ],
            [
              '`SYNTHETIC_BROWSER_ARTIFACTS_DIR`',
              'OS temporary directory + `probara/synthetic-browser-artifacts`',
              'API reads and workers write browser artifacts. Compose shares a volume; Helm currently does not.',
            ],
          ],
        },
        {
          type: 'table',
          columns: ['Global AI fallback variable', 'Default / validation', 'Notes'],
          rows: [
            ['`LLM_PROVIDER`', '`openai_compat`', 'Accepted runtime providers: `openai_compat` and `openai`.'],
            ['`LLM_BASE_URL`', 'Empty', 'Base endpoint for an OpenAI-compatible provider.'],
            ['`LLM_API_KEY`', 'Empty', 'Provider secret.'],
            ['`LLM_MODEL`', 'Empty', 'Required when a base URL is configured.'],
            [
              '`LLM_JSON_MODE`',
              'Empty',
              'Product settings support empty/off, `json_object`, and `json_schema`.',
            ],
            ['`LLM_MAX_TOKENS`', '`1024`; positive integer', 'Maximum response tokens.'],
            ['`LLM_TIMEOUT_SECONDS`', '`60`; positive integer', 'Provider request timeout.'],
          ],
        },
        {
          type: 'paragraph',
          text:
            'These LLM values are an optional global fallback. Tenant-specific AI settings stored in PostgreSQL take precedence. Compose and Helm do not currently inject the fallback variables into API or worker pods. The API derives an `AIAnalysisEnabled` flag from `LLM_BASE_URL`, but that flag has no present runtime use.',
        },
      ],
    },
    {
      id: 'scheduler',
      title: 'Scheduler and result-ingest variables',
      blocks: [
        {
          type: 'table',
          columns: ['Variable', 'Code default / validation', 'Compose / Helm notes'],
          rows: [
            [
              '`SCHEDULE_INTERVAL_SECONDS`',
              '`2`; positive integer',
              'Compose and Helm override to `5`.',
            ],
            [
              '`SCHEDULER_BATCH_SIZE`',
              '`500`; positive integer',
              'Not exposed by Compose or Helm.',
            ],
            [
              '`CHECK_JOB_STREAM`',
              '`CHECK_JOBS`',
              'Compose and Helm override to `check-jobs`.',
            ],
            [
              '`CHECK_JOB_SUBJECT`',
              '`check.jobs`',
              'Compose and Helm override to `check.job`.',
            ],
            ['`CHECK_RESULT_STREAM`', '`CHECK_RESULTS`', 'Not exposed by Compose or Helm.'],
            ['`CHECK_RESULT_SUBJECT`', '`check.results`', 'Not exposed by Compose or Helm.'],
            [
              '`RESULT_INGEST_CONSUMER_NAME`',
              '`result-ingest`',
              'Not exposed by Compose or Helm.',
            ],
            [
              '`RESULT_INGEST_CONCURRENCY`',
              '`10`; positive integer',
              'Not exposed by Compose or Helm.',
            ],
            [
              '`RESULT_INGEST_ENABLED`',
              '`true`; boolean',
              'Not exposed by Compose or Helm. `false` means worker results are published but not persisted.',
            ],
            [
              '`CHECK_JOB_LEGACY_CONSUMERS`',
              '`check-workers,worker`',
              'Comma-separated old filterless durable consumers deleted at scheduler startup.',
            ],
            [
              '`RETENTION_CLEANUP_ENABLED`',
              '`true`; boolean',
              'Exposed by Compose and Helm.',
            ],
            [
              '`RETENTION_CLEANUP_HOUR_UTC`',
              '`2`; integer 0–23',
              'Exposed by Compose and Helm.',
            ],
            [
              '`RETENTION_CLEANUP_BATCH_SIZE`',
              '`5000`; positive integer',
              'Exposed by Compose and Helm.',
            ],
            [
              '`RETENTION_CLEANUP_MAX_ROWS_PER_RUN`',
              '`200000`; positive integer',
              'Exposed by Compose and Helm.',
            ],
            [
              '`MONITOR_PURGE_ENABLED`',
              '`true`; boolean',
              'Not exposed by Compose or Helm.',
            ],
            [
              '`MONITOR_PURGE_INTERVAL_SECONDS`',
              '`30`; positive integer',
              'Not exposed by Compose or Helm.',
            ],
            [
              '`MONITOR_PURGE_BATCH_SIZE`',
              '`5000`; positive integer',
              'Not exposed by Compose or Helm.',
            ],
            [
              '`MONITOR_PURGE_MAX_ROWS_PER_RUN`',
              '`200000`; positive integer',
              'Not exposed by Compose or Helm.',
            ],
            ['`MESH_ENABLED`', '`true`; boolean', 'Not exposed by Compose or Helm.'],
            [
              '`MESH_PROBE_INTERVAL_SECONDS`',
              '`30`; positive integer',
              'Not exposed by Compose or Helm.',
            ],
            [
              '`MESH_PROBE_TIMEOUT_SECONDS`',
              '`5`; positive integer',
              'Not exposed by Compose or Helm.',
            ],
            [
              '`MESH_FAILURE_THRESHOLD`',
              '`3`; positive integer',
              'Not exposed by Compose or Helm.',
            ],
            [
              '`MESH_SCHEDULE_BATCH_SIZE`',
              '`500`; positive integer',
              'Not exposed by Compose or Helm.',
            ],
          ],
        },
      ],
    },
    {
      id: 'worker',
      title: 'Worker execution and network-policy variables',
      blocks: [
        {
          type: 'table',
          columns: ['Variable', 'Code default / validation', 'Notes'],
          rows: [
            ['`WORKER_CONCURRENCY`', '`10`; positive integer', 'Concurrent check-job handlers.'],
            [
              '`NATS_CONSUMER_NAME`',
              '`check-workers`',
              'Compose and Helm override to `worker` for the default fleet.',
            ],
            ['`CHECK_JOB_STREAM`', '`CHECK_JOBS`', 'Must match scheduler.'],
            ['`CHECK_JOB_SUBJECT`', '`check.jobs`', 'Must match scheduler and API.'],
            ['`CHECK_RESULT_STREAM`', '`CHECK_RESULTS`', 'Result work-queue stream.'],
            ['`CHECK_RESULT_SUBJECT`', '`check.results`', 'Result publication subject.'],
            [
              '`MAX_HTTP_TIMEOUT_SECONDS`',
              '`30`; integer',
              'Loaded but currently unused outside the configuration object.',
            ],
            [
              '`MAX_BODY_SIZE_BYTES`',
              '`1048576`; positive integer',
              'Maximum HTTP response body accepted by the worker.',
            ],
            [
              '`HTTP_BLOCK_PRIVATE_IPS`',
              '`false`; boolean',
              'When enabled, blocks private, loopback, link-local, and reserved destinations across networked check types.',
            ],
            [
              '`HTTP_ALLOWED_CIDRS`',
              'Empty; comma-separated CIDRs',
              'Narrow exceptions to the block policy. Any invalid CIDR fails startup.',
            ],
            [
              '`CHROME_BIN`',
              'Autodetect common Chromium/Chrome names',
              'Override the browser executable. The worker image sets `/usr/bin/chromium-browser`.',
            ],
            [
              '`SIP_LOCALHOST_AS_HOST_GATEWAY`',
              '`false`',
              'Truthy values are `1`, `true`, `yes`, and `on`. Compose/local set it for host-gateway development.',
            ],
          ],
        },
        {
          type: 'callout',
          tone: 'warning',
          title: 'The HTTP prefix is historical',
          text:
            '`HTTP_BLOCK_PRIVATE_IPS` and `HTTP_ALLOWED_CIDRS` protect HTTP, browser, TCP, gRPC, database, broker, and other [outbound dial paths](/docs/security/#ssrf-network-policy). Disabling the policy grants broad internal-network reach to monitor configuration.',
        },
      ],
    },
    {
      id: 'worker-ai-notifications',
      title: 'Worker AI and asynchronous notifications',
      blocks: [
        {
          type: 'table',
          columns: ['Variable', 'Code default', 'Notes'],
          rows: [
            ['`AI_RCA_STREAM`', '`AI_RCA`', 'AI root-cause work-queue stream.'],
            ['`AI_RCA_SUBJECT`', '`ai.rca.jobs`', 'Must match API.'],
            ['`AI_RCA_CONSUMER_NAME`', '`ai-rca-workers`', 'Durable AI consumer.'],
            [
              '`NOTIFICATIONS_ENABLED`',
              '`false`',
              'Starts the worker notification consumer only when PostgreSQL is also configured.',
            ],
            ['`NOTIFICATIONS_STREAM`', '`NOTIFICATIONS`', 'Async notification work queue.'],
            [
              '`NOTIFICATIONS_SUBJECT_GLOB`',
              '`alerts.dispatch.>`',
              'Subject filter shared with alerter.',
            ],
            [
              '`NOTIFICATIONS_CONSUMER_NAME`',
              '`notifications-worker`',
              'Durable dispatch consumer.',
            ],
          ],
        },
        {
          type: 'paragraph',
          text:
            'The worker uses the same `LLM_*` and `SMTP_*` variables documented on this page. Compose and Helm do not currently wire AI, notification, or SMTP variables into the worker.',
        },
      ],
    },
    {
      id: 'alerter-and-smtp',
      title: 'Alerter and SMTP variables',
      blocks: [
        {
          type: 'table',
          columns: ['Variable', 'Code default / validation', 'Description'],
          rows: [
            ['`ALERT_STREAM`', '`ALERTS`', 'Alert-event stream.'],
            ['`ALERT_SUBJECT`', '`alerts`', 'Alert-event subject.'],
            [
              '`ALERT_EVAL_INTERVAL_SECONDS`',
              '`30`; positive integer',
              'Current alert-lifecycle evaluation cadence.',
            ],
            [
              '`ALERTER_LATENCY_ANOMALY_ENABLED`',
              '`true`; boolean',
              'Enables latency-anomaly evaluation.',
            ],
            [
              '`ALERT_REMINDER_INTERVAL_SECONDS`',
              '`3600`; positive integer',
              'Legacy variable used only by the retired alert-evaluation path; current reminders use each tenant’s `alert_reminder_seconds` setting.',
            ],
            [
              '`ALERT_GROUP_WINDOW_SECONDS`',
              '`60`; positive integer',
              'Legacy grouping-window variable used only by the retired alert-evaluation path; currently unused.',
            ],
            [
              '`ALERT_GROUP_MAX_CHILDREN`',
              '`5`; positive integer',
              'Legacy grouped-child limit used only by the retired alert-evaluation path; currently unused. Current lifecycle handling uses group rollup.',
            ],
            [
              '`ALERT_EMAIL_TO`',
              'Empty',
              'Comma-separated fallback recipients for built-in email.',
            ],
            [
              '`ALERTER_ASYNC_DISPATCH`',
              '`false`; boolean',
              'Publishes channel dispatch jobs to NATS instead of sending synchronously.',
            ],
            ['`NOTIFICATIONS_STREAM`', '`NOTIFICATIONS`', 'Must match worker.'],
            [
              '`NOTIFICATIONS_SUBJECT_GLOB`',
              '`alerts.dispatch.>`',
              'Must match worker.',
            ],
          ],
        },
        {
          type: 'table',
          columns: ['SMTP variable', 'Code default', 'Important behavior'],
          rows: [
            ['`SMTP_HOST`', 'Empty', 'Required for usable built-in email delivery.'],
            ['`SMTP_PORT`', '`587`', 'Integer port.'],
            ['`SMTP_USERNAME`', 'Empty', 'Optional SMTP authentication username.'],
            ['`SMTP_PASSWORD`', 'Empty', 'SMTP authentication secret.'],
            ['`SMTP_FROM`', '`SMTP_USERNAME` when empty', 'Required effective sender address.'],
            [
              '`SMTP_USE_TLS`',
              '`true`; boolean',
              '`true` selects direct implicit TLS, not STARTTLS. Verify the provider and port pairing.',
            ],
          ],
        },
        {
          type: 'callout',
          tone: 'warning',
          title: 'Async mode requires both sides',
          text:
            'Set `ALERTER_ASYNC_DISPATCH=true` on alerter and `NOTIFICATIONS_ENABLED=true` on database-connected workers, with identical stream/subject settings. The current Compose and Helm templates expose neither complete side.',
        },
      ],
    },
    {
      id: 'status-page-service',
      title: 'Status-page and live-update variables',
      blocks: [
        {
          type: 'table',
          columns: ['Variable', 'Code default / validation', 'Description'],
          rows: [
            [
              '`STATUS_PAGE_BASE_URL`',
              'Empty',
              'Loaded into status config but currently has no runtime effect.',
            ],
            [
              '`STATUS_PAGE_API_BASE_URL`',
              'Empty',
              'Enables the status service’s restricted API reverse proxy.',
            ],
            [
              '`STATUS_PAGE_READ_TIMEOUT_SECONDS`',
              '`15`',
              'Invalid or nonpositive input silently falls back to 15 seconds.',
            ],
            [
              '`STATUS_PAGE_WRITE_TIMEOUT_SECONDS`',
              '`60`',
              'Invalid or nonpositive input silently falls back to 60 seconds.',
            ],
            [
              '`STATUS_PAGE_CACHE_TTL`',
              '`10s` Go duration',
              'Invalid or nonpositive input falls back to 10 seconds.',
            ],
            [
              '`STATUS_PAGE_PREVIEW_SECRET`',
              'Empty',
              'HMAC secret shared with API for one-hour [preview tokens](/docs/status-pages/#preview-security). With no secret, preview verification is not enforced.',
            ],
            [
              '`STATUSPAGE_UPDATES_SUBJECT`',
              '`statuspage.updates`',
              'Core NATS live-invalidation subject shared by API, scheduler, and status service.',
            ],
          ],
        },
        {
          type: 'paragraph',
          text:
            'The restricted proxy permits GET under `/api/v1/monitors` and PATCH under `/api/v1/status-pages/…`; it is not a general API proxy. Helm currently wires only basic status values and the status base URL, not the API proxy, cache, preview, timeout, or update-subject variables.',
        },
      ],
    },
    {
      id: 'frontend',
      title: 'Frontend variables',
      blocks: [
        {
          type: 'table',
          columns: ['Variable', 'Default', 'Runtime behavior'],
          rows: [
            [
              '`API_PROXY_TARGET`',
              '`http://localhost:8080`',
              'Server-side destination for `/api` and downloadable agent-binary proxy routes.',
            ],
            [
              '`NEXT_PUBLIC_API_URL`',
              '`/api`',
              'Browser API base. Compiled into the Next.js client bundle.',
            ],
            [
              '`NEXT_PUBLIC_STATUS_PAGE_URL`',
              'Empty',
              'Status-page link origin; falls back to the current/relative origin or localhost during local use. Compiled into the client bundle.',
            ],
            [
              '`NEXT_PUBLIC_DEBUG_INGEST_URL`',
              'Empty',
              'Developer-only debug timing sink. If set, monitor IDs and browser timing events are sent to it.',
            ],
            [
              '`NODE_ENV`',
              'Docker image sets `production`',
              'Next.js runtime mode.',
            ],
            ['`PORT`', 'Docker image sets `3000`', 'Next.js listener port.'],
            ['`HOSTNAME`', 'Docker image sets `0.0.0.0`', 'Next.js bind address.'],
          ],
        },
        {
          type: 'callout',
          tone: 'warning',
          title: 'NEXT_PUBLIC variables are build-time inputs',
          text:
            'The Helm chart sets public variables as pod runtime environment, which generally cannot change an already-built browser bundle. Build the frontend image with the intended values or keep `/api` and relative status-page routing stable. Alert SSE is currently hardcoded to `/api`; there is no active `NEXT_PUBLIC_SSE_URL` setting.',
        },
      ],
    },
    {
      id: 'agent',
      title: 'Standalone host agent settings',
      blocks: [
        {
          type: 'table',
          columns: ['CLI flag', 'Default', 'Description'],
          rows: [
            ['`-backend-url`', 'None; required', 'Externally reachable API base URL.'],
            ['`-agent-id`', 'None; required', 'Agent monitor identifier.'],
            ['`-api-key`', 'None; required', 'Bearer API key used for reporting.'],
            ['`-interval`', '`60`', 'Reporting interval in seconds.'],
            ['`-disk-path`', '`/`', 'Filesystem path used for disk telemetry.'],
            [
              '`-allow-remote-disable`',
              '`false`',
              'Allows a server HTTP 410 response to initiate local disable/uninstall.',
            ],
            [
              '`-remote-disable-command`',
              'Empty',
              'Script/command used only when remote disable is explicitly allowed.',
            ],
          ],
        },
        {
          type: 'callout',
          tone: 'warning',
          title: 'CLI flags are authoritative',
          text:
            'The current agent executable does not read `BACKEND_URL`, `AGENT_ID`, `API_KEY`, `INTERVAL`, or `DISK_PATH` from the environment, despite older README and Dockerfile claims. The JSON-form container command also does not shell-expand `${…}` placeholders. Supply the CLI flags explicitly.',
        },
      ],
    },
    {
      id: 'maintenance-and-tooling',
      title: 'Maintenance, build, and test variables',
      blocks: [
        {
          type: 'table',
          columns: ['Variable', 'Default / requirement', 'Command or scope'],
          rows: [
            ['`MIGRATIONS_PATH`', '`./migrations`', '`go run ./cmd/migrate`; local workflow supplies `./shared/db/migrations`.'],
            ['`ADMIN_USERNAME`', 'Required', '`go run ./cmd/admin` administrator upsert.'],
            ['`ADMIN_PASSWORD`', 'Required', '`go run ./cmd/admin`; do not expose in shell history in production.'],
            ['`BOOTSTRAP_DB_USER`', '`probara`', '`scripts/bootstrap-local-db.sh`.'],
            ['`VERSION`', '`1.0.0`', '`scripts/build-agent.sh`.'],
            ['`BUILD_DIR`', '`./static/agent`', 'Output directory for downloadable agent binaries.'],
            [
              '`GHCR_OWNER`',
              'Falls back to `GITHUB_REPOSITORY_OWNER`; then required',
              'Helm OCI publication.',
            ],
            [
              '`GITHUB_REPOSITORY_OWNER`',
              'GitHub Actions context or shell value',
              'Fallback owner used by Helm OCI publication when `GHCR_OWNER` is empty.',
            ],
            [
              '`GHCR_HELM_REPO`',
              '`oci://ghcr.io/<owner>/charts`',
              'Helm OCI destination.',
            ],
            [
              '`SEMANTIC_RELEASE_TOKEN`',
              'Required GitHub Actions secret for release workflow',
              'Used as semantic-release, GHCR/Helm registry, `GH_TOKEN`, and `GITHUB_TOKEN` credential. Scope it to the repository/packages required by the workflow.',
            ],
            ['`API`', '`http://localhost:8080`', '`scripts/verify-auth.sh`.'],
            ['`ADMIN_USER`', '`admin`', 'Authentication verification helper.'],
            ['`ADMIN_PASS`', '`change-me`', 'Authentication verification helper only; never a production default.'],
            ['`VERIFY_SSO`', '`0`', 'Enable SSO checks in the auth verification helper.'],
            ['`API_URL`', '`http://localhost:8080`', 'Dependency-graph seed helper.'],
            ['`TENANT_ID`', 'Auto-select first tenant when supported', 'Seed/test helpers.'],
            [
              '`API_KEY`',
              'Placeholder in `scripts/test-group-creation.sh`',
              'Test helper credential only; never use a production key in a disposable script shell history.',
            ],
            ['`WRITE_PREVIEW`', 'Empty', 'Test-only flag that writes rendered status preview HTML.'],
          ],
        },
        {
          type: 'paragraph',
          text:
            'GitHub’s `PR_TITLE`, `PR_BODY`, `TARGET_BRANCH`, `GITHUB_OUTPUT`, and `GITHUB_STEP_SUMMARY` variables are workflow-internal inputs for release-preview classification, not product runtime configuration. Docker build workflow values such as `REGISTRY` and `IMAGE_PREFIX` are likewise CI-owned defaults.',
        },
      ],
    },
    {
      id: 'wiring-summary',
      title: 'Deployment wiring summary',
      blocks: [
        {
          type: 'table',
          columns: ['Area', 'Docker Compose', 'Helm chart'],
          rows: [
            [
              'Core ports, DB, NATS, log level',
              'Wired',
              'Wired through values and generated connection URLs',
            ],
            [
              'Admin JWT and public API URL',
              'Required interpolation',
              'Required chart validation',
            ],
            ['OIDC', 'Development Dex-oriented variables wired', 'Primary OIDC values and client Secret wired'],
            ['Encryption keyring', 'Not wired', 'Base key wired to API/worker/alerter, but not scheduler; rotation keys not exposed'],
            ['AI fallback', 'Not wired', 'Not wired'],
            ['SMTP and notification mode', 'Not wired', 'Not wired'],
            ['Scheduler result ingest', 'Code defaults only', 'Code defaults only'],
            ['Scheduler purge and mesh', 'Code defaults only', 'Code defaults only'],
            ['Worker result stream and AI/notification queues', 'Code defaults only', 'Code defaults only'],
            ['Status cache, proxy, preview, timeout, update subject', 'Preview only; most defaults', 'Not wired'],
            ['Browser artifact sharing', 'Shared volume', 'No shared volume'],
            ['Arbitrary extra environment', 'Requires editing Compose', 'No `extraEnv`; requires chart template change'],
          ],
        },
        {
          type: 'callout',
          tone: 'info',
          title: 'Document the effective profile, not only the loader',
          text:
            'For reproducible installations, record the exact values that reach each service and keep API, scheduler, worker, alerter, and status contracts aligned. A valid variable name in this reference is not proof that the [bundled deployment](/docs/deployment/#known-chart-gaps) currently forwards it.',
        },
      ],
    },
  ],
};
