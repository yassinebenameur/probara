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
    'local AI',
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
            'Compose has no `env_file` declaration. SMTP, notification, mesh, purge, and many retention variables in your shell or `.env` have no container effect unless `docker-compose.yml` explicitly forwards them. `PROBARA_SECRETS_KEY` is forwarded to the api, scheduler, worker, and alerter services.',
        },
        {
          type: 'callout',
          tone: 'warning',
          title: 'Keep queue names identical across publishers and consumers',
          text:
            'The code defaults are `CHECK_JOBS` and `check.jobs`, while the shipped Compose and Helm values are `check-jobs` and `check.job` — set identically on the API, scheduler, and workers so on-demand “run now” jobs land where workers consume them. If you override either value, treat stream and subject as one cross-service contract and change them everywhere at once. The Helm chart does this for you: `scheduler.checkJobStream` and `scheduler.checkJobSubject` set both variables on every workload, and a `worker.checkJobStream` or `worker.checkJobSubject` that differs from them fails the render.',
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
              'Required by API, scheduler, alerter, and status page. Optional for workers; a worker without it cannot run the notification, AI-RCA or alert-triage (relay and consumer) side consumers.',
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
              'Local-process script and Compose set `false`. Helm renders `true` when `api.publicBaseURL` is an `https://` origin; set `api.cookieSecure` to force it either way. A Secure cookie is never sent over plain HTTP, so `true` on an HTTP-only deployment makes login impossible.',
            ],
            [
              '`BOOTSTRAP_SETUP_CODE`',
              'Empty',
              'One-time code the first-administrator form asks for, so a reachable install cannot be claimed by whoever gets there first. At least 16 characters besides spaces and dashes, or the API refuses to start. Compared in constant time, ignoring case, spaces and dashes; missing and wrong codes get the same 403 and are logged without the submitted value. Empty leaves the form open until an administrator exists. Has no effect once a superadministrator has existed: bootstrap never reopens after that. The release installer generates one; Compose passes it through; Helm: `secrets.bootstrapSetupCode` or an existing Secret with key `bootstrap_setup_code`.',
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
            [
              '`TRUSTED_PROXY_CIDRS`',
              'Empty (trust no proxy); comma-separated CIDRs or bare IPs, IPv4 or IPv6; an invalid entry stops startup',
              'The reverse proxies whose `X-Forwarded-For` / `X-Real-IP` resolve the [client address](/docs/security/#audit-proxy-trust) for audit records, session IPs and rate limits, and whose `X-Forwarded-Proto: https` earns HSTS. Read by the API and status-page. Compose passes it through from the shell to both; Helm renders the top-level `trustedProxyCIDRs` into both.',
            ],
            [
              '`SECURITY_HSTS_MAX_AGE`',
              '`0` (off); nonnegative integer seconds',
              '[HSTS](/docs/security/#edge-controls) max-age for API and status-page responses, sent only on HTTPS requests. Compose defaults to `0`; Helm renders `security.hstsMaxAge`, which when empty is one year with an ingress `tls` block and `0` otherwise.',
            ],
            [
              '`API_RATE_LIMIT_PER_MINUTE`',
              '`600`; nonnegative integer; `0` disables the limiter',
              'General [API rate limit](/docs/security/#edge-controls): per credential; per client address only for anonymous requests and verified failures, and only when the address names a client (a public peer, or forwarded by a trusted proxy). In memory on each API replica. Compose and Helm (`security.apiRateLimitPerMinute`) wire it.',
            ],
            [
              '`API_RATE_LIMIT_BURST`',
              '`120`; nonnegative integer',
              'Bucket size of the same limiter. Compose and Helm (`security.apiRateLimitBurst`) wire it.',
            ],
            [
              '`API_ALLOWED_ORIGINS`',
              'Empty; comma-separated `scheme://host[:port]`; `*` and paths are refused',
              'Browser origins allowed credentialed [cross-origin](/docs/security/#passwords-api-keys-sessions) API calls. Empty means the `APP_BASE_URL` origin, or same-origin only when that is unset too. Compose passes it through; Helm renders `security.apiAllowedOrigins`.',
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
              'Externally reachable API origin used in agent installers, push webhooks, the default OIDC callback, and the [MCP](/docs/mcp/#authentication) OAuth issuer and discovery metadata (derived from the request when empty). Compose and Helm require it for normal deployment.',
            ],
            [
              '`PUBLIC_NATS_URL`',
              'Empty',
              'Broker address embedded in private-location deployment instructions. When deployment information is generated, only `tls://` and `wss://` URLs are accepted; pre-existing URL credentials are rejected. `wss://host[/path]` is the broker\'s [websocket listener](/docs/deployment/#nats-websocket) behind an HTTPS ingress, reachable over 443. Helm derives it from `nats.websocket.ingress.*` when `api.publicNatsURL` is empty.',
            ],
            [
              '`LOCATION_WORKER_IMAGE`',
              'Empty: `ghcr.io/yassinebenameur/probara-worker:<API version>`, or `:alpha` for an unreleased build',
              'Image the generated location-worker snippets reference. Released API images default to the worker at their own version, so a new location runs the same release as the platform; builds without a release version (local, Compose without `PROBARA_VERSION`) fall back to the moving `alpha` channel tag, which follows the latest `-alpha.N` release. Never `:latest`. Helm sets it to `{image.repository}-worker:{image tag}` (override with `api.locationWorkerImage`); Compose passes it through from the shell. Set it for a mirror registry.',
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
              'Whitespace-separated scopes. Add `groups` when using OIDC group mappings — many IdPs (Okta among them) only emit the groups claim when the scope is requested.',
            ],
            [
              '`OIDC_GROUPS_CLAIM`',
              '`groups`',
              'ID-token claim read for group→role mappings. The mapping rules themselves live in the database (Settings → Access → OIDC group mappings), not in environment variables.',
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
              'Default tenant role for JIT users. Ignored whenever any OIDC group mappings exist — mapped roles replace the JIT defaults entirely.',
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
              'Version 1 key for encrypted channel, monitor and location credentials (and any retired AI provider key kept for rollback). The API, alerter and worker also derive the signing key for [acknowledge links](/docs/alerting/#acknowledge-links) from it; without it notifications carry none. The API signs the unsubscribe links of [scheduled SLA reports](/docs/slas/#schedules) with it too, so without it no report schedule can be enabled.',
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
      title: 'API queues, local AI, mesh, and artifacts',
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
          columns: ['Local AI variable', 'Default / validation', 'Read by and deployment wiring'],
          rows: [
            ['`LOCAL_AI_DISABLED`', '`false`', 'API, worker, alerter and the inference service. `true` overrides every workspace switch, and the service then loads no model (readiness answers 503); the alerter only reads it to stop [suspected-cause follow-ups](/docs/alerting/#ai-triage). Compose forwards the shell value to all four; Helm renders `true` on API, worker and alerter when `localAI.enabled=false` and no `localAI.externalUrl` is set.'],
            ['`LOCAL_AI_URL`', '`http://127.0.0.1:8090`; `http(s)://host[:port]` with no path, query or credentials', 'API and worker. Compose sets `http://local-ai:8090`; Helm sets `http://<release>-local-ai:8090` or `localAI.externalUrl`; the local-process stack uses the default (the container is published on loopback).'],
            ['`LOCAL_AI_TOKEN`', 'At least 16 characters', 'API, worker and the inference service, which must all hold the same value. Surrounding whitespace, such as the trailing newline of a Secret created from a file, is ignored on every side. The service refuses to start without a valid token; API and worker start anyway and report local AI as not configured correctly. See [token handling](/docs/deployment/#helm-values-local-ai) for Compose, local and Helm defaults.'],
            ['`LOCAL_AI_TIMEOUT_SECONDS`', '`90`; 1–300', 'API and worker request timeout. An out-of-range value stops startup. Helm: `localAI.timeoutSeconds`.'],
            ['`LOCAL_AI_THREADS`', '`4`; 1–4', 'Inference service only: CPU threads for the single inference process. The model is CPU-validated at 4. Compose defaults to 4 and the Make targets to min(host CPUs, 4); Helm: `localAI.threads`.'],
            ['`LOCAL_AI_QUEUE_CAPACITY`', '`4`; 1–32', 'Inference service only: batches allowed to wait behind the one executing; beyond that the service answers 429. Helm: `localAI.queueCapacity`.'],
            ['`LOCAL_AI_QUEUE_TIMEOUT_SECONDS`', '`60`; above 0, at most 300', 'Inference service only: how long a waiting batch may wait for the executing slot before a 429. A full 50-decision batch takes about 15–20 seconds on 4 CPU threads; keep it below `LOCAL_AI_TIMEOUT_SECONDS` so a queued caller gets a retryable 429 rather than its own timeout (Helm enforces this). Compose forwards the shell value (default 60); Helm: `localAI.queueTimeoutSeconds`.'],
            ['`LOCAL_AI_MAX_INFERENCE_SECONDS`', '`120`; above 0, at most 600', 'Inference service only: a batch still running after this long is treated as wedged, and `/healthz` and `/readyz` answer 503 until it finishes, so the Kubernetes liveness probe restarts the pod. Compose forwards the shell value (default 120) and marks the container unhealthy; Docker does not restart an unhealthy container by itself. Helm: `localAI.maxInferenceSeconds`.'],
            ['`LOCAL_AI_IMAGE`, `LOCAL_AI_CPUS`', '`ghcr.io/yassinebenameur/probara-local-ai:latest`; `4`', 'Compose interpolation only: the image to pull (built from `ai-service/` when the pull fails) and the container CPU limit. `latest` is pushed only by stable releases, so on the prerelease line set `LOCAL_AI_IMAGE` to an `:alpha` or version tag, or Compose builds the image locally (Git LFS head weights required); a local build keeps the tag, so rebuild it (`docker compose build local-ai`) after an `ai-service/` change. Docker requires the CPU limit to be at most the host CPU count. The Make targets and `scripts/start-local-services.sh` default it to min(host CPUs, 4).'],
          ],
        },
        {
          type: 'paragraph',
          text: 'Local AI uses one evaluated Probara checkpoint, served by the installation-owned inference service (`ai-service/`), for every AI surface: incident analysis, automatic alert triage and dependency suggestions. Range errors in the service variables stop the service at startup; Helm checks `threads`, `queueCapacity`, `queueTimeoutSeconds`, `maxInferenceSeconds` and `timeoutSeconds` (and that the queue timeout is below the client timeout) at render time instead. Workspace rows store only effective enablement: explicit enabled/disabled settings are preserved and absent settings default enabled. External providers and `LLM_*` execution are retired; old columns are retained for rollback. Explanations come from measured evidence and maintained templates.',
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
              'Narrow exceptions to the block policy. Entries also admit a private media address that a public SIP peer names in its SDP answer, which a [test call with `call.media`](/docs/security/#ssrf-network-policy) otherwise refuses even with blocking off. Any invalid CIDR fails startup.',
            ],
            [
              '`CHROME_BIN`',
              'Autodetect common Chromium/Chrome names',
              'Override the browser executable. The worker image sets `/usr/bin/chromium-browser`.',
            ],
            [
              '`SIP_LOCALHOST_AS_HOST_GATEWAY`',
              '`false`',
              'Rewrites each SIP target host that is `localhost` or a loopback IP (the monitor host or, under `resolution: rfc3263`, an SRV target) to `host.docker.internal` before it is resolved, so a containerized worker can reach a SIP server on the developer machine. Truthy values are `1`, `true`, `yes`, and `on`. The Compose worker turns it on. `make start-all-local` leaves it off: its worker runs on the host, where loopback already reaches the developer machine and the rewrite would break SIP to `localhost`. Leave it off in production.',
            ],
            [
              '`SIP_CALL_ENABLED`',
              '`true` on platform workers, `false` on private-location workers (`WORKER_LOCATION_ID` set)',
              'Lets the worker place [SIP test calls](/docs/monitors/#network-protocols) (`method: invite`), which are real and possibly billed. A disabled worker fails each one with `sip_call: disabled on this worker` before sending anything. It also gates call quality (`call.media`, which sends RTP and RTCP during the hold): there is no separate media switch. A private-location operator opts in with `SIP_CALL_ENABLED=true` (Helm: `worker.locations[].extraEnv`). Any value `strconv.ParseBool` rejects fails startup.',
            ],
            [
              '`SIP_CALL_ALLOWED_DESTINATIONS`',
              'Empty (every destination)',
              'Comma-separated regular expressions; a test call is placed only when the called user part (`call.number`, or the decoded user part of `call.target_uri`, which may carry no `;parameters` or `:password`) matches one in full (each is anchored), else it fails with `policy: destination not allowed by worker policy` before sending anything. An invalid expression fails startup.',
            ],
          ],
        },
        {
          type: 'callout',
          tone: 'warning',
          title: 'The HTTP prefix is historical',
          text:
            '`HTTP_BLOCK_PRIVATE_IPS` and `HTTP_ALLOWED_CIDRS` protect HTTP, synthetic API, TCP, gRPC, database, broker, ping, DNS nameserver, and other [outbound dial paths](/docs/security/#ssrf-network-policy); synthetic browser journeys are not covered. Disabling the policy grants broad internal-network reach to monitor configuration.',
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
              '`AI_TRIAGE_ENABLED`',
              '`true`; boolean, invalid values fail startup',
              'Platform kill switch for [automatic alert triage](/docs/alerting/#ai-triage), read by the worker, the alerter and the API. On the worker `false` stops new claims only; queued triages finish as skipped without calling the model. On the alerter `false` stops every suspected-cause follow-up, including from triages that were already ready. On the API `false` (or `LOCAL_AI_DISABLED=true`) keeps [SLA reports](/docs/slas/#suspected-causes) from listing an alert with no triage as Triage pending. Compose forwards the shell value to all three; Helm renders `localAI.triageEnabled` into all three.',
            ],
            [
              '`AI_TRIAGE_STREAM`',
              '`AI_TRIAGE`',
              'Alert triage work-queue stream (1-hour max age). Must differ from `AI_RCA_STREAM`, or startup fails.',
            ],
            [
              '`AI_TRIAGE_SUBJECT`',
              '`ai.triage.jobs`',
              'Worker-internal: the relay publishes and the triage consumer reads it, so the API does not need it. Must differ from `AI_RCA_SUBJECT`, or startup fails.',
            ],
            ['`AI_TRIAGE_CONSUMER_NAME`', '`ai-triage-workers`', 'Durable triage consumer.'],
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
            'The database-connected worker uses the same `LOCAL_AI_*` client variables as the API; Compose and Helm wire them to both, and Helm wires `SMTP_*` through its `smtp` values block. The `AI_RCA_*` variables and the `AI_TRIAGE_*` stream names are not wired, so their code defaults apply; set them through `extraEnv` only to rename the streams. `AI_TRIAGE_ENABLED` is wired to the worker, the alerter and the API. Only database-connected platform workers read `AI_TRIAGE_*`: they run the triage relay and consumer, and location workers never do. Private-location workers receive no database or AI credentials and execute checks independently. Notification-mode variables still require `extraEnv`.',
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
            [
              '`LOCAL_AI_DISABLED` / `AI_TRIAGE_ENABLED`',
              '`false` / `true`; boolean, invalid values fail startup',
              'The local AI kill switches. The alerter never calls local AI; with `LOCAL_AI_DISABLED=true` or `AI_TRIAGE_ENABLED=false` it sends no [suspected-cause follow-ups](/docs/alerting/#ai-triage), whatever the workspace settings say.',
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
          type: 'paragraph',
          text:
            'The `SMTP_*` variables are read identically by three services, and each one needs them for a different reason: the **alerter** delivers alert email from its evaluation loop, the **worker** delivers it when asynchronous dispatch is enabled, and the **API** serves `POST /api/v1/alert-channels/{id}/test`, which runs the same email plugin in-process. An install that sets SMTP only on the alerter delivers alerts correctly but fails every email channel test with `mailer not configured`. The API also mails [scheduled SLA reports](/docs/slas/#schedules), which additionally need `APP_BASE_URL` and `PROBARA_SECRETS_KEY` on the API; without all three no schedule can be enabled. It also mails [status-page email subscribers](/docs/status-pages/#email-subscribers), which need those three plus `STATUS_PAGE_BASE_URL` on the API; without them no page can turn subscriptions on. The Helm `smtp` values block renders the variables into all three at once; set it there rather than in a single service\'s `extraEnv`.',
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
              '`SMTP_FROM_NAME`',
              '`Probara Alerts`',
              'Display name on the From header. The envelope sender stays `SMTP_FROM`.',
            ],
            [
              '`SMTP_USE_TLS`',
              '`true`; boolean',
              '`true` selects direct implicit TLS, not STARTTLS. Verify the provider and port pairing.',
            ],
            [
              '`APP_BASE_URL`',
              'Empty',
              'Public origin of the operator UI. Adds an "open the monitor" link to alert email and every chat, paging and SMS channel, and, with `PROBARA_SECRETS_KEY` set, the one-click [acknowledge link](/docs/alerting/#acknowledge-links) on pages; both are omitted when unset. Read by the same three services. The API also sends [MCP](/docs/mcp/#authentication) sign-ins to the consent screen at this origin (default: the API origin, which is right when the UI and API share one), and needs it for [scheduled SLA reports](/docs/slas/#schedules), whose emails link to the report and to their unsubscribe page there.',
            ],
          ],
        },
        {
          type: 'paragraph',
          text:
            'The same three services also read the [notification egress policy](/docs/security/#ssrf-network-policy), because each one can deliver through a channel plugin. It is independent of the worker\'s `HTTP_BLOCK_PRIVATE_IPS` and, unlike it, blocks by default.',
        },
        {
          type: 'table',
          columns: ['Notification variable', 'Code default', 'Important behavior'],
          rows: [
            [
              '`NOTIFICATION_BLOCK_PRIVATE_IPS`',
              '`true`; boolean',
              'Refuse private, loopback, link-local and reserved destinations for every notification channel. Set `false` only when channels must reach internal hosts and network policy already contains them.',
            ],
            [
              '`NOTIFICATION_ALLOWED_CIDRS`',
              'Empty; comma-separated CIDRs',
              'Destinations that stay reachable while blocking is on, e.g. an internal webhook receiver. Any invalid CIDR fails startup.',
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
              'Public origin of the status service. Supplies the deep link in [visitor push notifications](/docs/status-pages/#browser-notifications) and each page\'s canonical link, `og:url` and absolute [feed and summary links](/docs/status-pages/#feed-summary-badges); when empty, the notification opens the page via the service worker scope, the head carries no canonical link or `og:url`, and the feed links by path, rather than a guessed host.',
            ],
            [
              '`STATUS_PAGE_API_BASE_URL`',
              'Empty',
              'Enables the status service’s restricted API reverse proxy.',
            ],
            [
              '`STATUS_PAGE_INTERNAL_API_URL`',
              '`STATUS_PAGE_API_BASE_URL` when empty',
              'Where the status service forwards visitors\' [email subscription](/docs/status-pages/#email-subscribers) requests (the API stores subscribers; the status service stays read-only). Empty, and no `STATUS_PAGE_API_BASE_URL` either: no page shows a subscribe form. Helm sets it to the in-cluster API service; Compose to `http://api:8080`.',
            ],
            [
              '`STATUS_PAGE_BASE_URL` (API)',
              'Empty',
              'The same public status origin, read by the API: every link in a [subscriber email](/docs/status-pages/#email-subscribers) points to the page under it. Required to turn subscriptions on. Helm renders `statusPage.baseUrl` into both services; Compose sets both to the same value.',
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
              'HMAC secret shared with API for one-hour [preview tokens](/docs/status-pages/#preview-security). With no secret, preview verification is not enforced. Helm: `secrets.statusPagePreviewSecret` (or its `ExistingSecret` reference, canonical key `status_page_preview_secret`) renders it into both the API and the status service.',
            ],
            [
              '`STATUS_PAGE_SESSION_SECRET`',
              'Empty; at least 32 characters when set, or the service refuses to start',
              'Status service only. Signs visitor sessions on [password and private pages](/docs/status-pages/#visibility). Unset: those pages cannot be opened (password pages answer `503`, private pages `404`) and a warning is logged, while public and unlisted pages serve as usual. Changing it signs every visitor out. Generate with `openssl rand -hex 32`; the release installer and `scripts/start-local-services.sh` generate one. Helm derives one from a chart-managed `adminJwtSecret` unless `secrets.statusPageSessionSecret` (or `statusPageSessionSecretExistingSecret`, canonical key `status_page_session_secret`) is set; see [Helm values](/docs/deployment/).',
            ],
            [
              '`STATUS_PAGE_COOKIE_SECURE`',
              'Empty (derived); `true`/`false`, anything else refuses to start',
              'Secure attribute of the visitor session cookie. Empty: on when `STATUS_PAGE_BASE_URL` is `https://`, else set per request when it arrived over HTTPS (TLS terminated here, or `X-Forwarded-Proto: https` from a `TRUSTED_PROXY_CIDRS` peer). Set `true` behind a TLS-terminating proxy when the base URL is unset; `false` forces it off. Helm: `statusPage.cookieSecure`.',
            ],
            [
              '`STATUSPAGE_UPDATES_SUBJECT`',
              '`statuspage.updates`',
              'Core NATS live-invalidation subject shared by API, scheduler, and status service. Also wakes the push sender early; delivery does not depend on it.',
            ],
            [
              '`STATUS_PAGE_VAPID_PUBLIC_KEY`',
              'Empty',
              'VAPID application server key for [visitor notifications](/docs/status-pages/#browser-notifications). Published inside every rendered page, so it is not a secret. Generate with `go run ./cmd/admin/gen_vapid_keys`.',
            ],
            [
              '`STATUS_PAGE_VAPID_PRIVATE_KEY`',
              'Empty',
              'Signs the push JWT. A credential. Notifications are disabled unless both key halves are set; never generate per process, because replicas would disagree and a restart would invalidate every subscription.',
            ],
            [
              '`STATUS_PAGE_VAPID_SUBJECT`',
              'Empty',
              '`mailto:` or `https:` operator contact for the VAPID `sub` claim. Some push services reject a missing or malformed value.',
            ],
            [
              '`STATUS_PAGE_PUSH_ENDPOINT_ALLOWLIST`',
              'Google, Mozilla, Microsoft, Apple push hosts',
              'Comma-separated host suffixes a stored push endpoint may use. This is an [SSRF control](/docs/security/#ssrf-network-policy); `*` disables it for a self-hosted push service.',
            ],
            [
              '`STATUS_PAGE_PUSH_MAX_SUBSCRIPTIONS_PER_PAGE`',
              '`10000`',
              'Cap on stored subscriptions per status page. Invalid or nonpositive input falls back to the default.',
            ],
            [
              '`STATUS_PAGE_TRUSTED_PROXY`',
              '`false`; deprecated',
              'Superseded by `TRUSTED_PROXY_CIDRS`, which the status-page reads too (with `SECURITY_HSTS_MAX_AGE`). `true` still trusts `X-Forwarded-For` from every peer for push rate limiting, but only while `TRUSTED_PROXY_CIDRS` is unset, and the service logs a deprecation warning.',
            ],
            [
              '`APP_BASE_URL`',
              'Empty',
              'Operator UI origin. Every page’s [CSP](/docs/status-pages/#content-security-policy) `frame-ancestors` admits it so the template editor can preview pages in an iframe. Compose passes it through from the shell (empty by default); Helm renders the top-level `appBaseURL`. Unset, the draft preview route may be framed by any site, so the template editor works from whatever host serves the UI; set it to restrict the preview to that origin.',
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
              '`API_PROXY_TRUST_FORWARDED_FOR`',
              '`false`',
              'The `/api` proxy drops a client-sent `X-Forwarded-For` / `X-Real-IP` unless this is `true`; set it only when a proxy you run in front of the UI sets that header, and then list the frontend in the API’s `TRUSTED_PROXY_CIDRS` ([trusted proxies](/docs/security/#audit-proxy-trust)). Runtime; not wired by Compose or Helm (use `frontend.extraEnv`).',
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
      title: 'Collector agent settings',
      blocks: [
        {
          type: 'paragraph',
          text:
            'The host agent is `probara-collector`, an OpenTelemetry Collector distribution started as `probara-collector --config <path>`. Its generated configuration contains no secrets; credentials are supplied through the environment (systemd `EnvironmentFile`, launchd runner script, or the Windows service registry `Environment` value).',
        },
        {
          type: 'table',
          columns: ['Environment variable', 'Default', 'Description'],
          rows: [
            ['`PROBARA_API_KEY`', 'None; required', 'Tenant write-scope API key sent as the `Authorization: Bearer` header.'],
            ['`PROBARA_AGENT_ID`', 'None; required', 'Agent monitor identifier sent as the `X-Probara-Agent-Id` header.'],
          ],
        },
        {
          type: 'callout',
          tone: 'info',
          title: 'Everything else lives in the collector config',
          text:
            'API origin, export interval, scrapers, and processors are all part of the generated collector YAML (`GET /api/v1/monitors/{id}/agent/config.yaml`), which references credentials only via `${env:…}` expansion. The legacy `probara-agent` CLI flags — including `-allow-remote-disable` — are retired with that binary; see the [migration guide](/docs/agents/#migrating-legacy).',
        },
      ],
    },
    {
      id: 'discovery-collector',
      title: 'Discovery collector settings',
      blocks: [
        {
          type: 'paragraph',
          text:
            'The [Kubernetes discovery](/docs/discovery/) collector (`probara-discovery-collector`) runs in the cluster from the manifest the setup form generates, which sets `PROBARA_URL` and `PROBARA_DISCOVERY_ID` and mounts the token Secret at the default `PROBARA_DISCOVERY_TOKEN_FILE` path; you only need these when writing your own manifest. It reaches the Kubernetes API through the in-cluster `KUBERNETES_SERVICE_HOST`/`KUBERNETES_SERVICE_PORT` and the mounted service-account CA and token.',
        },
        {
          type: 'table',
          columns: ['Environment variable', 'Default', 'Description'],
          rows: [
            ['`PROBARA_URL`', 'None; required', 'HTTPS origin of the Probara API, with no path, query or credentials; anything else fails startup.'],
            ['`PROBARA_DISCOVERY_ID`', 'None; required', 'The discovery integration UUID.'],
            ['`PROBARA_DISCOVERY_TOKEN_FILE`', '`/var/run/probara/token`', 'File holding the integration-scoped collector token (the manifest mounts its Secret there).'],
            ['`SSL_CERT_FILE`', 'Unset: the image\'s public CA bundle', 'PEM bundle the collector trusts for the Probara origin; needed only when that origin uses a certificate from an internal CA. The Kubernetes API connection always uses the service-account CA instead.'],
          ],
        },
        {
          type: 'paragraph',
          text:
            'The collector reads its configuration (active and pending namespaces) at the start of every cycle and waits five minutes between cycles, or the `Retry-After` delay (1 second to 1 hour) when the API answers 409, 429 or 503; the API sends a 10-second `Retry-After` with a 409 when a namespace was removed during the scan, and none for a stale revision. A refused request is logged with the HTTP status and up to 512 bytes of the response body, which carries the API\'s reason (for example `rate_limited` or `inventory_limit`). Pending namespaces are scanned after the active ones and stop 30 seconds before the two-minute cycle deadline; each one skipped is logged as `Pending namespace <ns> skipped (<reason>): <hint>` (see [Change namespaces](/docs/discovery/#namespaces)).',
        },
        {
          type: 'paragraph',
          text:
            'Probara origin behind an internal CA: store the CA certificate in a ConfigMap in the collector\'s namespace (the integration\'s home namespace, the first one entered at setup), mount it, and point `SSL_CERT_FILE` at it. A bundle holding only that CA is enough, since the Probara origin is the only peer verified against it. The generated manifest does not include this, so reapply the patch after applying a regenerated manifest (reinstall rotates the token).',
        },
        {
          type: 'code',
          language: 'bash',
          code:
            "kubectl -n <home-namespace> create configmap probara-ca --from-file=ca.crt=internal-ca.pem\nkubectl -n <home-namespace> patch deployment probara-discovery-<integration-prefix> -p '{\"spec\":{\"template\":{\"spec\":{\n  \"volumes\":[{\"name\":\"probara-ca\",\"configMap\":{\"name\":\"probara-ca\"}}],\n  \"containers\":[{\"name\":\"collector\",\n    \"env\":[{\"name\":\"SSL_CERT_FILE\",\"value\":\"/etc/probara-ca/ca.crt\"}],\n    \"volumeMounts\":[{\"name\":\"probara-ca\",\"mountPath\":\"/etc/probara-ca\",\"readOnly\":true}]}]}}}}'",
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
            ['`OCB_VERSION`', '`v0.159.0`', '`scripts/build-collector.sh`; pinned OpenTelemetry Collector Builder version, must match `collector/manifest.yaml`.'],
            ['`BUILD_DIR`', '`./static/collector`', 'Output directory for downloadable collector binaries and `checksums.txt`.'],
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
            ['`LAYA_GIT_URL`, `HF_ENDPOINT`, `BASE_MODEL_REPO`, `BASE_MODEL_REVISION`', '`https://github.com/NandhaKishorM/laya.git`, `https://huggingface.co`, empty, empty (= `base-lock.json`)', 'Build args of `ai-service/Dockerfile` naming where the pinned laya commit and base model are fetched from, for a mirror; the pinned commit and every locked sha256 are still checked. CI reads the first two from repository variables of the same name, and `release.yml` checks those sources still serve the pins before tagging.'],
            ['`API`', '`http://localhost:8080`', '`scripts/verify-auth.sh`.'],
            ['`ADMIN_USER`', '`admin`', 'Authentication verification helper.'],
            ['`ADMIN_PASS`', '`change-me`', 'Authentication verification helper only; never a production default.'],
            ['`VERIFY_SSO`', '`0`', 'Enable SSO checks in the auth verification helper.'],
            ['`API_URL`', '`http://localhost:8080`', 'Dependency-graph seed helper.'],
            ['`TENANT_ID`', 'Auto-select first tenant when supported', 'Seed/test helpers.'],
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
            ['Local AI', '`local-ai` service (loopback port 8090) wired to API and worker, `LOCAL_AI_DISABLED` and `AI_TRIAGE_ENABLED` also to the alerter, `AI_TRIAGE_ENABLED` also to the API; fixed public development token unless `LOCAL_AI_TOKEN` is set in the shell or `.env`', '`localAI` values: bundled Deployment, Service and NetworkPolicy, or `externalUrl`; token in `local_ai_token`; API and platform worker only (the alerter gets only `LOCAL_AI_DISABLED` and `AI_TRIAGE_ENABLED`; `AI_TRIAGE_ENABLED` also reaches the API)'],
            [
              'SMTP',
              'Not wired',
              '`smtp` values block wired to API, worker, and alerter',
            ],
            ['Notification dispatch mode', 'Not wired', 'Not wired'],
            ['Scheduler result ingest', 'Code defaults only', 'Code defaults only'],
            ['Scheduler purge and mesh', 'Code defaults only', 'Code defaults only'],
            ['Worker result stream and AI/notification queues', 'Code defaults only', 'Code defaults only'],
            ['Status cache, proxy, preview, timeout, update subject', 'Preview only; most defaults', 'Not wired'],
            ['Trusted proxies, HSTS, API rate limit and CORS allowlist', 'Passed through from the shell to API and status-page (`APP_BASE_URL` to status-page too)', 'Top-level `trustedProxyCIDRs` and `security` block rendered into API and status-page; `appBaseURL` into status-page as well'],
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
