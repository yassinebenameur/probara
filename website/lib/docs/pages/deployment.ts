import type { DocPage } from '../types';

export const DEPLOYMENT_PAGE: DocPage = {
  slug: 'deployment',
  group: 'Build & operate',
  title: 'Deployment',
  description:
    'Run Probara with local processes, Docker Compose, or Kubernetes and Helm, with explicit guidance for databases, NATS, ingress, private locations, migrations, scaling, and current packaging limitations.',
  eyebrow: 'Deployment guide',
  readingTime: '25 min read',
  keywords: [
    'deployment',
    'Docker Compose',
    'Kubernetes',
    'Helm',
    'PostgreSQL',
    'NATS',
    'ingress',
    'private locations',
  ],
  sections: [
    {
      id: 'choose-a-workflow',
      title: 'Choose a deployment workflow',
      blocks: [
        {
          type: 'table',
          columns: ['Workflow', 'Best for', 'What runs where'],
          rows: [
            [
              '`make start-all-local`',
              'Feature development and debugging',
              'PostgreSQL, NATS and the local AI container in Docker; locally built Go services and Next.js UI on the host.',
            ],
            [
              '`make start-all`',
              'Testing backend container images locally',
              'PostgreSQL, NATS, local AI, and Go services in Compose; Next.js UI still runs on the host.',
            ],
            [
              '`docker compose up -d` / `make up`',
              'Raw backend Compose lifecycle',
              'Compose services only. There is no frontend service in the current root Compose file.',
            ],
            [
              'Helm chart',
              'Kubernetes staging and production',
              'Frontend, services, local AI inference, migration hook, and optionally embedded PostgreSQL/NATS in the cluster.',
            ],
          ],
        },
        {
          type: 'callout',
          tone: 'info',
          title: 'Use the matching stop command',
          text:
            'Stop local-process stacks with `make stop-all-local` and Docker-backed stacks with `make stop-all`. The restart targets call the matching stop/start pair.',
        },
      ],
    },
    {
      id: 'prerequisites',
      title: 'Prerequisites',
      blocks: [
        {
          type: 'list',
          items: [
            'Docker with Compose v2 for PostgreSQL, NATS, migrations, and container workflows.',
            'GNU Make and a Bash-compatible environment for repository scripts. On Windows, use WSL or another environment that can execute the Makefile’s Bash commands.',
            'Git LFS to build the local AI image from source: run `git lfs install` and `git lfs pull --include \'ai-service/artifacts/*/head.safetensors\'` once after cloning. Not needed when a release image is pulled or with `LOCAL_AI_DISABLED=true`.',
            'Go 1.26.5 for the main monorepo. The local launcher compares the installed major/minor version with the root `go.mod` directive.',
            'Node.js LTS and npm for the frontend. `scripts/start-ui.sh` can use nvm when it is available.',
            'Helm 3 and `kubectl` for Kubernetes deployment.',
            'A PostgreSQL 16-compatible database and NATS 2.10 with JetStream for production when not using embedded chart dependencies.',
          ],
        },
        {
          type: 'paragraph',
          text:
            'The host agent (`probara-collector`) is not a separate Go module: `scripts/build-collector.sh` generates and cross-compiles a minimal OpenTelemetry Collector distribution from `collector/manifest.yaml` with a pinned builder version. Root `make test` covers the whole repository.',
        },
      ],
    },
    {
      id: 'prepare-secrets',
      title: 'Prepare required values',
      blocks: [
        {
          type: 'code',
          language: 'bash',
          title: 'Create local environment values',
          code: `cp .env.example .env

# Generate stable secrets; keep them across restarts and upgrades.
openssl rand -hex 32       # ADMIN_JWT_SECRET
openssl rand -base64 32    # PROBARA_SECRETS_KEY`,
        },
        {
          type: 'list',
          items: [
            'Set `ADMIN_JWT_SECRET` to a random value of at least 32 characters. Validation only rejects an empty value, so shipped placeholder defaults must be replaced deliberately.',
            'Set `PUBLIC_BASE_URL` to the [externally reachable API origin](/docs/configuration/#public-urls-and-private-location-auth). For local use, `http://localhost:8080` is appropriate.',
            'Set `PROBARA_SECRETS_KEY` before storing monitor credentials, notification secrets, location credentials, or tenant AI keys; see [encryption keys](/docs/configuration/#encryption-keys) for format and rotation.',
            'Do not set `PUBLIC_NATS_URL` until NATS authentication and an externally reachable TLS/WSS endpoint are ready.',
          ],
        },
        {
          type: 'callout',
          tone: 'warning',
          title: 'Compose ships an insecure hardcoded secret',
          text:
            'The Compose model hardcodes an insecure `ADMIN_JWT_SECRET` default and never references `PUBLIC_BASE_URL`; the interpolated Compose variables (`STATUS_PAGE_PREVIEW_SECRET`, `OIDC_*`, `AUDIT_RETENTION_DAYS`, `PROBARA_SECRETS_KEY`, `LOCAL_AI_*`) are optional, and `LOCAL_AI_TOKEN` falls back to a public development token. Populate `.env` anyway so the platform runs on real values instead of placeholders.',
        },
      ],
    },
    {
      id: 'local-process-stack',
      title: 'Local-process stack',
      blocks: [
        {
          type: 'code',
          language: 'bash',
          title: 'Start and stop',
          code: `make start-all-local

# Logs
tail -f /tmp/probara-*.log
tail -f /tmp/probara-ui.log

# Matching lifecycle
make restart-all-local
make stop-all-local`,
        },
        {
          type: 'paragraph',
          text:
            'The target starts PostgreSQL and NATS, bootstraps local database access, validates Go, runs migrations, builds downloadable collector binaries, builds and launches each Go service, starts the `local-ai` container in the background (the first image build takes several minutes; progress in `/tmp/probara-local-start.log`), and starts Next.js on `0.0.0.0:3000`. Until the container is up, or if it fails to start, API and worker report the model unavailable; the platform is never blocked. With no local image and the head weights still a Git LFS pointer, it only tries a pull. A local build is reused as is, so rebuild it with `docker compose build local-ai` after an `ai-service/` change. `LOCAL_AI_DISABLED=true` skips the container and turns the feature off.',
        },
        {
          type: 'definitions',
          items: [
            {
              term: '`.dev-secrets.key`',
              description:
                'Gitignored, persistent local encryption key created by the launcher.',
            },
            {
              term: '`.dev-local-ai-token`',
              description:
                'Gitignored per-checkout local AI bearer token, created on first start when `LOCAL_AI_TOKEN` is not exported, and passed to the container, API and worker.',
            },
            {
              term: '`/tmp/probara-*.pid` and `/tmp/probara-*.log`',
              description:
                'Local service process tracking and logs used by the start/stop scripts.',
            },
          ],
        },
        {
          type: 'callout',
          tone: 'warning',
          title: 'An older helper is incomplete',
          text:
            '`scripts/run-api.sh` does not supply the now-required `ADMIN_JWT_SECRET`. Prefer the Make workflow or export all required variables yourself.',
        },
      ],
    },
    {
      id: 'docker-compose',
      title: 'Docker Compose stack',
      blocks: [
        {
          type: 'code',
          language: 'bash',
          title: 'Docker-backed backend with local UI',
          code: `make start-all
make ps
make logs
make healthcheck
make stop-all`,
        },
        {
          type: 'code',
          language: 'bash',
          title: 'Raw Compose operations',
          code: `docker compose up -d
docker compose ps
docker compose logs -f
docker compose down`,
        },
        {
          type: 'table',
          columns: ['Component', 'Host port', 'Container behavior'],
          rows: [
            ['Frontend', '`3000`', 'Started on the host by Make; not a Compose service.'],
            ['API HTTP, health, readiness, metrics', '`8080`', 'All API routes, including `/metrics`, use the HTTP listener.'],
            [
              'API declared metrics port',
              '`9090`',
              'Mapped by Compose, but the API currently does not start a separate listener there.',
            ],
            ['Scheduler metrics/health', '`9091`', 'The scheduler’s real HTTP listener.'],
            [
              'Scheduler declared HTTP port',
              '`8081`',
              'Mapped by Compose, but scheduler has no listener on `HTTP_PORT`.',
            ],
            ['Worker metrics/health/mesh', '`9092`', 'Metrics listener and mesh echo.'],
            [
              'Worker dedicated mesh HTTP',
              'Not published (`8083` inside container)',
              'Starts because HTTP and metrics ports differ; Compose exposes only metrics port.',
            ],
            ['Status pages', '`8082`', 'Public rendered pages and status HTTP routes.'],
            ['Status metrics/health', '`9093`', 'Separate operational listener.'],
            ['Alerter metrics/health', '`9094`', 'Only published alerter listener.'],
            ['PostgreSQL', '`5432`', 'Development database, user/database/password `probara`.'],
            ['NATS', '`4222`', 'Client connection; no local authentication or TLS.'],
            ['NATS monitor', '`8222`', 'NATS HTTP monitoring endpoint.'],
            ['Local AI inference', '`8090` on `127.0.0.1` only', 'CPU inference for API and worker; `/healthz`, authenticated `/readyz`, `/metrics`.'],
          ],
        },
        {
          type: 'paragraph',
          text:
            'Local AI is on by default. Compose pulls `LOCAL_AI_IMAGE` and builds it from `ai-service/` only when the pull fails; that build fails on purpose unless the committed artifact is release-approved, and a failed build aborts a whole `docker compose up`. The Make targets (`up`, `build`, `rebuild`, `scale-workers`, `start-all`, `start-all-local`) therefore start the platform first and `local-ai` separately, tolerating its failure. With plain Compose and no pullable approved image, name the services you want (`docker compose up -d postgres nats migrations api scheduler worker status-page alerter`) or set `LOCAL_AI_IMAGE` to an image you already have. `LOCAL_AI_DISABLED=true` turns the feature off and makes the Make targets skip `local-ai`, but plain `docker compose up` still pulls or builds the service.',
        },
        {
          type: 'callout',
          tone: 'warning',
          title: 'Compose is a development profile',
          text:
            'It exposes PostgreSQL and an unauthenticated plaintext NATS broker on host ports, uses development credentials, and omits most advanced environment variables. Do not publish this topology directly to the internet.',
        },
      ],
    },
    {
      id: 'compose-contract-gaps',
      title: 'Compose contract gaps',
      blocks: [
        {
          type: 'list',
          items: [
            'API, scheduler, and worker all use stream `check-jobs` and subject `check.job`, so scheduled and on-demand jobs share one subject contract. If you change these values, change them on all three services together.',
            'The root `.env` file is not passed as a service `env_file`; only variables explicitly declared in Compose reach containers. `PROBARA_SECRETS_KEY` is forwarded from the shell environment to the api, scheduler, worker, and alerter services.',
            'SMTP, asynchronous notifications, public NATS authorization, scheduler mesh/purge, and many result-ingest settings are not wired.',
            'Without an exported `LOCAL_AI_TOKEN` (shell or `.env`), `local-ai`, API and worker share a fixed development token that is public in the repository. That is tolerable only because port 8090 is bound to loopback; set your own (`openssl rand -hex 32`) on any shared host. `make start-all-local` generates a per-checkout token instead.',
            'The commented private-location worker example omits required `LOCATION_CREDENTIAL` and should not be enabled as written.',
            'The API and worker correctly share the `synthetic_artifacts` volume used for failure screenshots. Trace and HAR capture are configured by the schema but are not implemented by the current worker.',
          ],
        },
        {
          type: 'callout',
          tone: 'warning',
          title: 'Do not hide these gaps with documentation-only values',
          text:
            'Adding an entry to `.env` does not repair the Compose wiring for variables that are not forwarded. Customize the service environment explicitly for anything beyond the declared variables, and keep queue stream/subject values identical across the API, scheduler, and workers.',
        },
      ],
    },
    {
      id: 'helm-install',
      title: 'Install with Helm',
      blocks: [
        {
          type: 'code',
          language: 'yaml',
          title: 'Minimal values.prod.yaml',
          code: `image:
  tag: "pin-an-immutable-release"

secrets:
  adminJwtSecret: "replace-with-at-least-32-random-characters"
  probaraSecretsKey: "base64-encoded-32-byte-key"

api:
  publicBaseURL: "https://probara.example.com"

ingress:
  enabled: true
  className: nginx
  hosts:
    - host: probara.example.com
      paths:
        - path: /
          pathType: Prefix
  tls:
    - secretName: probara-tls
      hosts:
        - probara.example.com`,
        },
        {
          type: 'code',
          language: 'bash',
          title: 'Install or upgrade',
          code: `helm lint ./helm/monitoring-platform \\
  --set secrets.adminJwtSecret="$(openssl rand -hex 32)" \\
  --set api.publicBaseURL="https://probara.example.com"

helm upgrade --install probara ./helm/monitoring-platform \\
  --namespace probara \\
  --create-namespace \\
  --values values.prod.yaml

kubectl -n probara get pods,svc,ingress
kubectl -n probara get jobs`,
        },
        {
          type: 'callout',
          tone: 'warning',
          title: 'Pin image tags in production',
          text:
            'The default image tag follows the chart’s `appVersion`, so a chart upgrade also moves every image. Pin immutable backend, frontend, migration and local AI image tags so images change only when you decide. Retain JWT and encryption keys across every upgrade; changing them invalidates sessions or makes stored ciphertext unreadable.',
        },
      ],
    },
    {
      id: 'helm-values-global-infrastructure',
      title: 'Helm values: images and infrastructure',
      blocks: [
        {
          type: 'table',
          columns: ['Value', 'Default', 'Meaning'],
          rows: [
            [
              '`global.imageRegistry`',
              'Empty',
              'Declared but currently not used when rendering image names.',
            ],
            [
              '`global.imagePullPolicy`',
              '`IfNotPresent`',
              'Global backend pull policy; also wins over the frontend-specific pull policy in current templates.',
            ],
            ['`image.repository`', '`ghcr.io/yassinebenameur/probara`', 'Backend image base repository.'],
            ['`image.tag`', 'Empty: the chart’s `appVersion`', 'Tag for every Probara image (backend services, migrations, frontend and local AI, unless their own `tag` is set). Each release pushes all images under its version, so the chart’s own version always resolves; `latest` exists only for stable releases, never on the prerelease line. Pin for production.'],
            ['`image.pullSecrets`', '`[]`', 'Backend registry pull secrets.'],
            ['`postgresql.enabled`', '`true`', 'Deploy embedded PostgreSQL StatefulSet.'],
            ['`postgresql.externalUrl`', 'Empty', 'Used when embedded PostgreSQL is disabled. Required in that mode unless an existing Secret supplies the DSN — an empty external URL now fails the render instead of starting pods with a blank DSN.'],
            [
              '`postgresql.existingSecret`, `.existingSecretKey`',
              'Empty, `postgres_url`',
              'Read the full DSN from a Secret you supply instead of `externalUrl` or the embedded credentials. Works in either mode and takes precedence over both.',
            ],
            ['`postgresql.image`', '`postgres:16-alpine`', 'Embedded database image.'],
            ['`postgresql.auth.username`', '`probara`', 'Embedded database user.'],
            ['`postgresql.auth.password`', '`probara-secret`', 'Embedded password; replace it.'],
            ['`postgresql.auth.database`', '`probara`', 'Embedded database name.'],
            [
              '`postgresql.auth.existingSecret`, `.existingSecretUserKey`, `.existingSecretPasswordKey`',
              'Empty, `postgresql_username`, `postgresql_password`',
              'Embedded-server credentials from a Secret you supply. Requires the DSN to be externalized too — via `postgresql.existingSecret` or the chart-wide `secrets.existingSecret` — because Helm cannot compose a DSN out of values it is not allowed to read. The render fails if neither is set.',
            ],
            ['`postgresql.persistence.enabled`', '`true`', 'Use a PVC for database data.'],
            ['`postgresql.persistence.size`', '`10Gi`', 'Database PVC request.'],
            ['`postgresql.persistence.storageClass`', 'Empty', 'Cluster default storage class.'],
            ['`postgresql.resources`', '100m/256Mi request; 500m/512Mi limit', 'Embedded database resources.'],
            ['`nats.enabled`', '`true`', 'Deploy embedded NATS StatefulSet.'],
            ['`nats.externalUrl`', 'Empty', 'Used when embedded NATS is disabled.'],
            [
              '`nats.existingSecret`, `.existingSecretKey`',
              'Empty, `nats_url`',
              'Read the full broker URL — credentials included — from a Secret you supply. Required alongside `nats.auth.platformPasswordExistingSecret` on the embedded broker (or covered by the chart-wide `secrets.existingSecret`), since the chart otherwise has to compose the URL around a password it cannot read.',
            ],
            [
              '`nats.auth.platformPasswordExistingSecret`, `.platformPasswordExistingSecretKey`',
              'Empty, `nats_platform_password`',
              'Platform-user password for the embedded broker. It reaches nats-server as a `--pass` argument, substituted by Kubernetes from a Secret-backed environment variable, so no password is written into the ConfigMap in either mode. It deliberately does not travel through `nats.conf` — nats-server re-parses an expanded `$VAR` as configuration, which breaks purely numeric or space-bearing passwords.',
            ],
            [
              '`nats.auth.locationIssuerSeedExistingSecret`, `.locationIssuerSeedExistingSecretKey`',
              'Empty, `nats_location_auth_issuer_seed`',
              'Auth-callout account seed for the API. The matching public key stays a plain ConfigMap value.',
            ],
            ['`nats.image`', '`nats:2.10-alpine`', 'Embedded broker image.'],
            ['`nats.persistence.enabled`', '`true`', 'Persist JetStream state.'],
            ['`nats.persistence.size`', '`1Gi`', 'NATS PVC request.'],
            ['`nats.persistence.storageClass`', 'Empty', 'Cluster default storage class.'],
            ['`nats.resources`', '50m/64Mi request; 200m/256Mi limit', 'Embedded NATS resources.'],
          ],
        },
      ],
    },
    {
      id: 'helm-values-auth-services',
      title: 'Helm values: auth and service workloads',
      blocks: [
        {
          type: 'table',
          columns: ['Value family', 'Defaults', 'Meaning / caveat'],
          rows: [
            [
              '`extraEnv`, `<service>.extraEnv`',
              '`[]`',
              'Standard EnvVar entries appended to workloads: top-level `extraEnv` reaches every workload (including the migrations job); per-service lists (`api`, `scheduler`, `worker`, `alerter`, `statusPage`, `frontend`, `migrations`) reach one. `worker.extraEnv` also applies to location workers, and each `worker.locations` entry may carry its own `extraEnv`. Use this for any supported runtime variable without a dedicated chart value (rotation keys, SSRF policy). Top-level `extraEnv` never reaches the local AI pod, which holds no platform credentials; it has its own `localAI.extraEnv`. Notification egress (`NOTIFICATION_BLOCK_PRIVATE_IPS`, `NOTIFICATION_ALLOWED_CIDRS`) blocks private destinations by default; to let channels reach an internal receiver, set the allow-list through top-level `extraEnv` so the API, worker and alerter all get it.',
            ],
            [
              '`smtp.host`, `.port`, `.useTLS`, `.from`, `.username`, `.password`, `.existingSecret`, `.existingSecretKey`',
              '`""`, `465`, `true`, `""`, `""`, `""`, `""`, `password`',
              'Mail transport for email alert channels, rendered into the API, worker, and alerter together — the API needs it for the channel test endpoint, not only the alerter. Empty `host` disables email delivery. `useTLS` selects implicit TLS (pair with port 465), not STARTTLS. Prefer `existingSecret` over an inline `password`.',
            ],
            [
              '`auth.auditRetentionDays`',
              '`365`',
              'Audit retention days; `0` preserves records indefinitely.',
            ],
            [
              '`auth.oidc.*`',
              'Disabled; blank issuer/client; scopes `openid profile email`; groups claim `groups`; label `SSO`; JIT viewer/default tenant',
              'Platform OIDC settings. Secret is `secrets.oidcClientSecret`. Add `groups` to `auth.oidc.scopes` when using OIDC group mappings.',
            ],
            [
              '`secrets.existingSecret`',
              'Empty',
              'Chart-wide fallback Secret backing every credential without a per-field reference. When set the chart renders no Secret of its own. See “Secrets from an external secret manager” below for the key names.',
            ],
            [
              '`secrets.adminJwtSecret`',
              'Insecure placeholder default',
              'Ships as `change-me-in-production-jwt-secret-minimum-32-chars`; no template validation rejects it, so replace it with at least 32 random characters, or point `secrets.adminJwtSecretExistingSecret` at a Secret.',
            ],
            [
              '`secrets.initialApiKey`',
              'Insecure placeholder',
              'Stored and printed by notes but not consumed by any bootstrap path. It does not create an API key.',
            ],
            [
              '`secrets.probaraSecretsKey`',
              'Empty',
              'Base64 32-byte encryption key, provided to the API, scheduler, worker, and alerter. Supplied by either the literal or an existing Secret; with neither, the key is absent and at-rest encryption stays off.',
            ],
            [
              '`secrets.oidcClientSecret`',
              'Empty',
              'Required when OIDC is enabled — the render now fails if neither it nor `secrets.oidcClientSecretExistingSecret` is set, instead of leaving the API pod unable to start on a missing Secret key.',
            ],
            [
              '`secrets.<field>ExistingSecret`, `.<field>ExistingSecretKey`',
              'Empty; canonical key name',
              'Per-field overrides for `adminJwtSecret`, `probaraSecretsKey`, and `oidcClientSecret`. They win over `secrets.existingSecret` and may point at different Secrets, so chart-managed, chart-wide, and per-field sources mix field by field.',
            ],
            [
              '`migrations.enabled`',
              '`true`',
              'Creates a post-install/post-upgrade migration hook Job.',
            ],
            [
              '`migrations.image.repository`, `.tag`, `.resources`',
              'Derived image; 50m/64Mi request, 200m/256Mi limit',
              'Override migration image independently when needed.',
            ],
            [
              '`api.enabled`, `.replicas`, `.httpPort`, `.metricsPort`, `.logLevel`',
              '`true`, `2`, `8080`, `9090`, `info`',
              'API workload. `publicBaseURL` is required when enabled.',
            ],
            [
              '`api.publicBaseURL`, `.publicNatsURL`',
              'Empty',
              'External API origin and optional external TLS/WSS NATS endpoint.',
            ],
            [
              '`scheduler.enabled`, `.replicas`, `.metricsPort`, `.logLevel`',
              '`true`, `1`, `9090`, `info`',
              'Scheduling and retention workload.',
            ],
            [
              '`scheduler.checkJobStream`, `.checkJobSubject`, `.intervalSeconds`',
              '`check-jobs`, `check.job`, `5`',
              'Must match worker and API. Current API subject wiring does not match.',
            ],
            [
              '`scheduler.retentionCleanup*`',
              '`true`, hour `2`, batch `5000`, max `200000`',
              'Scheduled telemetry retention limits.',
            ],
            [
              '`worker.enabled`, `.replicas`, `.metricsPort`, `.httpPort`, `.logLevel`',
              '`true`, `2`, `9090`, `8080`, `info`',
              'Default check-execution fleet and mesh echo port.',
            ],
            [
              '`worker.checkJobStream`, `.checkJobSubject`, `.consumerName`, `.concurrency`',
              '`check-jobs`, `check.job`, `worker`, `10`',
              'Queue contract and worker parallelism.',
            ],
            [
              '`worker.autoscaling.*`',
              'Disabled; 2–10 replicas; 80% CPU',
              'Creates an HPA for the default worker deployment.',
            ],
            [
              '`alerter.enabled`, `.replicas`, `.metricsPort`, `.logLevel`',
              '`true`, `2`, `9090`, `info`',
              'Alert evaluation workload. SMTP comes from the top-level `smtp` block; other advanced alert settings are not chart values.',
            ],
          ],
        },
        {
          type: 'paragraph',
          text:
            'Each service group also accepts a standard `resources.requests` and `resources.limits` object. Defaults are intentionally small and should be sized from observed check volume, job latency, database load, and notification throughput.',
        },
      ],
    },
    {
      id: 'helm-values-edge',
      title: 'Helm values: status, frontend, ingress, and pod policy',
      blocks: [
        {
          type: 'table',
          columns: ['Value family', 'Defaults', 'Meaning / caveat'],
          rows: [
            [
              '`statusPage.enabled`, `.replicas`, `.httpPort`, `.metricsPort`, `.logLevel`',
              '`true`, `2`, `8080`, `9090`, `info`',
              'Public status renderer.',
            ],
            [
              '`statusPage.baseUrl`',
              'Empty',
              'Passed as `STATUS_PAGE_BASE_URL`, which currently has no runtime use.',
            ],
            [
              '`statusPage.service.*`',
              'ClusterIP, port 8080, null NodePorts',
              'Public/metrics NodePorts may be fixed only when using NodePort.',
            ],
            [
              '`frontend.enabled`, `.replicas`, `.httpPort`',
              '`true`, `2`, `3000`',
              'Next.js deployment.',
            ],
            [
              '`frontend.apiUrl`, `.statusPageUrl`, `.apiProxyTarget`',
              '`/api`, empty, auto in-cluster API',
              'Public variables are build-time in Next.js; proxy target is runtime server-side.',
            ],
            [
              '`frontend.image.*`',
              'Dedicated frontend repository, empty tag (`image.tag`, then the chart’s `appVersion`), IfNotPresent, no secrets',
              'Pin tag. Global pull policy currently masks the frontend-specific pull policy.',
            ],
            [
              '`frontend.service.*`',
              'ClusterIP, port 3000',
              'Frontend Service settings.',
            ],
            [
              '`ingress.enabled`, `.className`, `.annotations`, `.hosts`, `.tls`',
              'Disabled; nginx; example frontend and status hosts',
              'The current routing template gives `/` to frontend and `/api` to API; see warning below.',
            ],
            [
              '`serviceAccount.create`, `.annotations`, `.name`',
              '`true`, `{}`, empty',
              'Workload service account selection.',
            ],
            [
              '`podAnnotations`',
              'Prometheus scrape on port 9090 at `/metrics`',
              'Applied to workloads; verify API metrics reachability because API serves metrics on its HTTP listener.',
            ],
            [
              '`podSecurityContext`',
              'fsGroup/runAsUser 1000; non-root',
              'Pod-level identity defaults.',
            ],
            [
              '`securityContext`',
              'No privilege escalation; drop all capabilities; writable root filesystem',
              'Container hardening defaults. Read-only root remains disabled.',
            ],
          ],
        },
        {
          type: 'callout',
          tone: 'warning',
          title: 'The example status host does not route / to the status service',
          text:
            'The ingress template always routes `/` to frontend and `/api` to API. Only additional paths other than `/` and `/api` are sent to status-page. A second host with path `/`, as shown in default values, therefore serves the frontend. Use a dedicated ingress/resource or repair the template for a status-site root host.',
        },
      ],
    },
    {
      id: 'helm-values-local-ai',
      title: 'Helm values: local AI',
      intro:
        'The chart deploys the bundled CPU inference service by default and configures only the API and the platform worker to call it. The alerter never calls it and receives only the `LOCAL_AI_DISABLED` and `AI_TRIAGE_ENABLED` kill switches; scheduler, status page and location workers never receive `LOCAL_AI_*`.',
      blocks: [
        {
          type: 'table',
          columns: ['Value', 'Default', 'Meaning'],
          rows: [
            ['`localAI.enabled`', '`true`', 'Deploy `<release>-local-ai` (Deployment, ClusterIP Service on 8090, NetworkPolicy). `false` without `externalUrl` renders `LOCAL_AI_DISABLED=true` on API, worker and alerter, which overrides every workspace setting (on the alerter it stops suspected-cause follow-ups).'],
            ['`localAI.externalUrl`', 'Empty', 'An operator-run service speaking the same `/v1/decisions` protocol. Requires `enabled=false` and an explicit token; must be `http(s)://host[:port]` with no path, query or credentials. Each rule fails the render.'],
            ['`localAI.image.repository`, `.tag`, `.pullPolicy`', 'Empty: `<image.repository>-local-ai`, `image.tag` (then the chart’s `appVersion`), `global.imagePullPolicy`', 'The image carries the model weights and runs offline. A private registry needs `image.pullSecrets`, which this pod also uses.'],
            ['`localAI.threads`', '`4`; 1–4', '`LOCAL_AI_THREADS`. Keep it at or below the CPU limit; the model is CPU-validated at 4.'],
            ['`localAI.queueCapacity`', '`4`; 1–32', '`LOCAL_AI_QUEUE_CAPACITY`: batches waiting behind the executing one before 429.'],
            ['`localAI.queueTimeoutSeconds`', '`60`; 1–300, below `timeoutSeconds`', '`LOCAL_AI_QUEUE_TIMEOUT_SECONDS`: how long a waiting batch waits for the executing one before 429. A full 50-decision batch takes about 15–20 s on 4 threads. The render fails unless it is lower than `localAI.timeoutSeconds`, so a queued caller gets a retryable 429 before its own client gives up.'],
            ['`localAI.maxInferenceSeconds`', '`120`; 1–600', '`LOCAL_AI_MAX_INFERENCE_SECONDS`: a batch running longer is wedged; `/healthz` answers 503 and the liveness probe restarts the pod.'],
            ['`localAI.timeoutSeconds`', '`90`; 1–300', '`LOCAL_AI_TIMEOUT_SECONDS` on API and worker.'],
            ['`localAI.triageEnabled`', '`true`', '`AI_TRIAGE_ENABLED` on the platform worker, the alerter and the API. `false` stops new [alert triages](/docs/alerting/#ai-triage) (queued ones drain as skipped) and every suspected-cause follow-up, whatever the workspace settings say, and SLA reports then list no alert as Triage pending.'],
            ['`localAI.resources`', '1 CPU/2Gi request; 4 CPU/4Gi limit', 'One process, one replica, `Recreate` strategy, so two copies of the model never run at once during a rollout.'],
            ['`localAI.podSecurityContext`', 'uid/gid 1000, non-root, `RuntimeDefault` seccomp', 'The container also runs with a read-only root filesystem, no privilege escalation, all capabilities dropped, no service-account token and a 64Mi `/tmp`.'],
            ['`localAI.podAnnotations`, `.nodeSelector`, `.tolerations`, `.affinity`', 'Empty', 'Not merged with the top-level values: the top-level scrape annotation names port 9090, while this pod serves `/metrics` on 8090.'],
            ['`localAI.networkPolicy.enabled`, `.extraIngress`', '`true`, `[]`', 'Ingress only from this release’s API and worker pods on 8090, and no egress at all, not even DNS. Add a Prometheus rule through `extraIngress`.'],
            ['`localAI.extraEnv`', '`[]`', 'The only extra environment the pod gets (the top-level `extraEnv` does not reach it). Every `LOCAL_AI_*` setting the service reads has its own value above.'],
            ['`secrets.localAiToken`, `.localAiTokenExistingSecret`, `.localAiTokenExistingSecretKey`', 'Empty, empty, `local_ai_token`', 'Bearer token shared by API, worker and the service; at least 16 characters.'],
          ],
        },
        {
          type: 'paragraph',
          text:
            'The token resolves like every other chart credential: `secrets.localAiTokenExistingSecret`, then the chart-wide `secrets.existingSecret`, then the chart-managed Secret. When chart-managed and left empty, the chart derives it from `secrets.adminJwtSecret` (a SHA-256 over a domain-separated copy) instead of generating a random value, so `helm template` and GitOps renderers produce the same Secret on every render. The derived token is as private as your JWT secret: with the shipped placeholder JWT secret it is a well-known value, and rotating `adminJwtSecret` rotates it, after which API, worker and `local-ai` need a restart together.',
        },
        {
          type: 'list',
          items: [
            'The render fails, asking for `secrets.localAiToken` or an existing Secret, when `localAI.externalUrl` is set (the operator-run service must hold the same token) or when `adminJwtSecret` is empty or supplied by an existing Secret (the chart cannot read it to derive from).',
            'API and worker reference the token as optional, so an existing Secret that predates local AI does not stop them on upgrade: they start and report local AI as not configured correctly. The local AI pod’s reference is required and it waits in `CreateContainerConfigError` until the key exists. The chart’s install/upgrade notes print this warning when `secrets.existingSecret` is set without a per-field token Secret.',
            'Readiness execs the image’s Python against the authenticated `/readyz`, because an `httpGet` probe cannot read the token from a Secret; like the service and the clients, it ignores whitespace around the token. The startup probe allows five minutes for the model to load. Liveness polls `/healthz` every 15 seconds and restarts the pod after three failures, which happens once a batch has run past `maxInferenceSeconds`: a hung inference cannot be cancelled in the process, and with one replica only a restart recovers.',
          ],
        },
        {
          type: 'callout',
          tone: 'warning',
          title: 'A NetworkPolicy only works where the CNI enforces it',
          text:
            'Calico, Cilium and similar CNIs enforce the rendered policy. On a CNI without NetworkPolicy support it is accepted and silently ignored, and the bearer token is then the only barrier between other pods and the service. Verify enforcement in your cluster; rendering the object proves nothing.',
        },
      ],
    },
    {
      id: 'external-secrets',
      title: 'Secrets from an external secret manager',
      blocks: [
        {
          type: 'paragraph',
          text:
            'Every credential the chart consumes can come from a Secret you supply instead of a literal in your values file. The chart takes no dependency on any particular tool: External Secrets Operator, the Vault agent injector, sealed-secrets, SOPS, and a hand-written `kubectl create secret` all end at the same place — a Secret in the release namespace — and the chart only ever names it.',
        },
        {
          type: 'paragraph',
          text:
            'Each field resolves its source in a fixed order: its own `<field>ExistingSecret`, then the chart-wide `secrets.existingSecret`, then the Secret the chart renders from your literal values. Sources mix field by field, so an install can keep the JWT chart-managed while the encryption key comes from Vault and the database DSN from a cloud secret store.',
        },
        {
          type: 'code',
          language: 'yaml',
          title: 'Chart-wide: one Secret behind everything',
          code: `secrets:
  # The chart then renders no Secret of its own.
  existingSecret: probara-platform-secrets`,
        },
        {
          type: 'code',
          language: 'yaml',
          title: 'Per field: different stores for different credentials',
          code: `postgresql:
  enabled: false
  existingSecret: rds-probara       # key: postgres_url (override with existingSecretKey)

nats:
  existingSecret: nats-platform     # key: nats_url

secrets:
  adminJwtSecret: "kept-in-the-values-file-for-this-install"
  probaraSecretsKeyExistingSecret: vault-probara
  probaraSecretsKeyExistingSecretKey: encryption_key
  oidcClientSecretExistingSecret: vault-probara

worker:
  locations:
    - name: eu-west
      locationId: "location-uuid-from-probara"
      existingSecret: probara-location-eu-west`,
        },
        {
          type: 'table',
          columns: ['Key', 'Needed when', 'Read by'],
          rows: [
            ['`postgres_url`', 'Always', 'API, scheduler, worker, alerter, status page, migrations job'],
            ['`nats_url`', 'Always', 'API, scheduler, worker, alerter, status page'],
            ['`admin_jwt_secret`', 'Always', 'API'],
            [
              '`probara_secrets_key`',
              'Always in chart-wide mode',
              'API, scheduler, worker, alerter. In chart-wide mode the chart assumes the Secret carries it rather than silently running with at-rest encryption off.',
            ],
            ['`postgresql_username`, `postgresql_password`', '`postgresql.enabled`', 'Embedded PostgreSQL StatefulSet'],
            ['`nats_platform_password`', '`nats.auth.enabled` on the embedded broker', 'Embedded NATS server, as a `--pass` argument substituted by Kubernetes'],
            ['`nats_location_auth_issuer_seed`', '`nats.auth.enabled`', 'API'],
            ['`oidc_client_secret`', '`auth.oidc.enabled`', 'API'],
            ['`local_ai_token`', '`localAI.enabled` or `localAI.externalUrl`', 'API and platform worker (optional reference), local AI pod (required)'],
          ],
        },
        {
          type: 'list',
          items: [
            'Per-field `<field>ExistingSecretKey` values default to the canonical key names above, so a Secret laid out with those names needs no key overrides.',
            'A key the chart asks for but the Secret does not carry leaves the pod in `CreateContainerConfigError`. That is deliberate: a missing encryption key should stop a rollout, not quietly disable encryption.',
            'Helm cannot read Secrets, so it cannot assemble a URL around a value it does not have: `postgresql.auth.existingSecret` needs the DSN externalized too, and `nats.auth.platformPasswordExistingSecret` needs the broker URL externalized. Either the matching per-field reference or the chart-wide `secrets.existingSecret` satisfies that; setting neither fails the render with that explanation.',
            'The Secret must exist before the pods start. With External Secrets Operator, that means the ExternalSecret must have synced — the migrations Job runs as a `post-install`/`post-upgrade` hook and needs `postgres_url` at that moment.',
            'Rotating a value in the Secret does not restart the workloads; the chart sets no checksum annotation. Restart the affected Deployments yourself after a rotation.',
            'Rotation keys (`PROBARA_SECRETS_KEY_V2` and higher) have no dedicated value and go through the top-level `extraEnv`, which accepts a `valueFrom.secretKeyRef` entry like any Kubernetes EnvVar.',
          ],
        },
      ],
    },
    {
      id: 'external-database-nats',
      title: 'External PostgreSQL and NATS',
      blocks: [
        {
          type: 'code',
          language: 'yaml',
          title: 'External infrastructure values',
          code: `postgresql:
  enabled: false
  externalUrl: "postgres://probara:REDACTED@postgres.example.internal:5432/probara?sslmode=require"

nats:
  enabled: false
  externalUrl: "tls://probara-platform:REDACTED@nats.example.internal:4222"

api:
  publicBaseURL: "https://probara.example.com"
  # Set only if private locations can reach this broker address.
  publicNatsURL: "tls://nats.example.com:4222"`,
        },
        {
          type: 'list',
          items: [
            'Use TLS certificate verification for both PostgreSQL and NATS. Do not copy the example with `sslmode=disable` into production.',
            'Provision JetStream storage for `CHECK_JOBS`, `CHECK_RESULTS`, alert, notification, and AI workloads.',
            'Keep the platform NATS credential separate from per-location credentials and restrict broker/network access.',
            'External URLs written as literals live in your values file. Point `postgresql.existingSecret` and `nats.existingSecret` at Secrets instead to keep them out of it — either way the chart reads both URLs from a Secret at runtime, never from a PodSpec.',
            'Test migrations against a backup before changing application versions.',
          ],
        },
      ],
    },
    {
      id: 'private-location-workers',
      title: 'Deploy private-location workers',
      blocks: [
        {
          type: 'paragraph',
          text:
            'A private-location worker needs only an authenticated NATS endpoint, its location UUID, and its location credential. It must not receive direct PostgreSQL access. Generate deploy information in the [Locations UI/API](/docs/locations/#create-and-deploy) after NATS authorization is configured.',
        },
        {
          type: 'code',
          language: 'yaml',
          title: 'In-cluster Helm location fleet',
          code: `worker:
  locations:
    - name: eu-west
      locationId: "location-uuid-from-probara"
      credential: "generated-location-credential"
      natsUrl: "tls://location-id:credential@nats.example.com:4222"
      # Or replace both with a Secret in the release namespace:
      #   existingSecret: probara-location-eu-west
      #   natsUrlKey: nats_url          # default
      #   credentialKey: credential     # default
      replicas: 1
      concurrency: "10"
      meshService:
        enabled: true
        type: LoadBalancer
        annotations:
          service.beta.kubernetes.io/aws-load-balancer-internal: "true"`,
        },
        {
          type: 'list',
          items: [
            'Use a DNS-safe unique `name`; each entry creates a separate Deployment.',
            'Set `existingSecret` on an entry to take `natsUrl` and `credential` from a Secret instead of the values file; the literals are then no longer required and no location credential appears in the PodSpec.',
            'The chart does not expose HTTP egress policy; provide `HTTP_BLOCK_PRIVATE_IPS` and `HTTP_ALLOWED_CIDRS` through extra environment configuration, and keep any allowlist limited to networks that location is explicitly trusted to monitor.',
            'Expose the mesh echo endpoint only on private inter-location networks.',
            'For locations outside the Kubernetes cluster, use the generated container/deployment snippet rather than granting database access.',
            'The embedded NATS Service is ClusterIP-only; an external location needs a separate TLS/WSS exposure path.',
          ],
        },
      ],
    },
    {
      id: 'scaling',
      title: 'Scaling and availability',
      blocks: [
        {
          type: 'table',
          columns: ['Component', 'Scaling guidance'],
          rows: [
            [
              'API',
              'Stateless around PostgreSQL/NATS and configured for two chart replicas. Preserve shared JWT/encryption/preview secrets across every replica.',
            ],
            [
              'Scheduler',
              'Chart defaults to one replica. Treat it as a singleton unless the scheduling/retention coordination semantics have been explicitly validated.',
            ],
            [
              'Default worker',
              'Scale manually or enable HPA. Queue consumer semantics distribute work across replicas.',
            ],
            [
              'Location worker',
              'Scale each location independently; every replica consumes only that location’s filtered job subject.',
            ],
            [
              'Alerter',
              'Chart defaults to two replicas, but validate duplicate-evaluation/notification behavior under your policies and NATS mode.',
            ],
            [
              'Status page and frontend',
              'Stateless application replicas; status cache invalidation benefits from NATS updates.',
            ],
            [
              'Local AI',
              'Exactly one replica with one inference process; a restart makes analyses and suggestions wait or fail until it is ready again. Size throughput with `localAI.threads`, `queueCapacity` and CPU limits, not replicas.',
            ],
            [
              'PostgreSQL and NATS',
              'Bundled dependencies are simple single StatefulSets, not production HA operators. Use managed or operator-backed systems for stronger recovery objectives.',
            ],
          ],
        },
        {
          type: 'code',
          language: 'bash',
          title: 'Local worker scaling',
          code: `make scale-workers N=3`,
        },
      ],
    },
    {
      id: 'migrations-upgrades',
      title: 'Migrations, upgrades, and rollback',
      blocks: [
        {
          type: 'list',
          ordered: true,
          items: [
            '[Back up PostgreSQL](/docs/operations/#backup-restore) and record the running application/chart/image versions.',
            'Render or lint the target chart with production values and inspect Secrets, Services, queue variables, and image tags.',
            'Run the [target migrations](/docs/operations/#database-migrations) against a disposable copy or staging database.',
            'Deploy the migration-compatible application version. The chart migration Job is a post-install/post-upgrade hook; API startup also runs shared migrations.',
            'Verify `/readyz`, result ingestion, scheduled and on-demand checks, alert delivery, status invalidation, and private locations.',
            'Roll back application images only when the schema remains backward-compatible. Database migrations are not automatically reversed by `helm rollback`.',
          ],
        },
        {
          type: 'callout',
          tone: 'warning',
          title: 'A Helm rollback is not a database rollback',
          text:
            'The migration hook changes shared PostgreSQL state. Keep backups and review migration compatibility before upgrading; never assume an application rollback will undo schema or data changes.',
        },
        {
          type: 'callout',
          tone: 'info',
          title: 'Upgrading from 1.0.x: AI is local and on by default',
          text:
            'The external LLM path is gone: `LLM_*` variables and workspace provider settings are no longer read (the old columns stay for rollback), and workspaces without an explicit AI setting are enabled. The chart now deploys `<release>-local-ai` (1 CPU/2 GiB request) and wires API and worker to it (the alerter gets only its kill switches); with `secrets.existingSecret`, add a `local_ai_token` key (see [local AI values](/docs/deployment/#helm-values-local-ai)). Deployments that do not use this chart need the `probara-local-ai` workload plus `LOCAL_AI_URL` and `LOCAL_AI_TOKEN` on API and worker, or `LOCAL_AI_DISABLED=true` on API, worker and alerter; otherwise analyses and alert triages fail with local AI reported unavailable, while alerting and paging are unaffected. Set `localAI.enabled=false` to opt out.',
        },
      ],
    },
    {
      id: 'known-chart-gaps',
      title: 'Current Helm packaging gaps',
      blocks: [
        {
          type: 'table',
          columns: ['Gap', 'Operational impact'],
          rows: [
            [
              'No shared browser-artifact storage',
              'API replicas cannot reliably serve artifacts produced on worker filesystems.',
            ],
            [
              '`initialApiKey` is unused',
              'Installation notes show a credential that authentication never recognizes.',
            ],
            [
              'Public Next variables are runtime-only',
              'Prebuilt frontend client links may retain build-time values.',
            ],
            [
              'API metrics Service points to an inactive separate port',
              'API actually serves `/metrics` on its HTTP listener.',
            ],
            [
              'Status-root ingress routes to frontend',
              'Default second-host example does not publish the status service at `/`.',
            ],
            [
              '`global.imageRegistry` and generic ConfigMap are unused',
              'Changing them does not alter workload behavior.',
            ],
            [
              'Embedded NATS is ClusterIP-only',
              'Private locations outside the cluster cannot connect without an additional secure exposure.',
            ],
            [
              'Local AI accuracy is measured on a controlled corpus only',
              'The shipped checkpoint passed its CPU release gates on synthetic scenarios whose labels come from the same rules that guard its answers, so no real-world accuracy is claimed. The `-local-ai` image is published only on a release tag, and only from a release-approved checkpoint. `docs/local-ai-validation.md` in the repository records the results and their limits.',
            ],
            [
              'Local AI is a single replica',
              'No HA for inference; analyses retry on the worker and then fail with a retry action while the pod restarts.',
            ],
            [
              'NetworkPolicy enforcement depends on the CNI',
              'Without an enforcing CNI the local AI service is reachable from any pod that holds the token.',
            ],
          ],
        },
        {
          type: 'callout',
          tone: 'info',
          title: 'Treat gaps as release criteria',
          text:
            'These are verified behaviors of the current dev state, not recommended architecture. Resolve the gaps relevant to your installation or maintain explicit downstream chart patches; do not assume undocumented values will be forwarded.',
        },
      ],
    },
    {
      id: 'production-checklist',
      title: 'Production checklist',
      blocks: [
        {
          type: 'list',
          items: [
            'Pin immutable backend, frontend, local AI, collector, migration, PostgreSQL, and NATS versions.',
            'Use managed/HA PostgreSQL and NATS or define tested backup and restore objectives for embedded state.',
            'Configure HTTPS, `ADMIN_COOKIE_SECURE=true`, a correct `PUBLIC_BASE_URL`, and trusted reverse-proxy headers.',
            'Use stable random JWT, encryption, local AI token, OIDC, preview, SMTP, webhook, database, and NATS secrets, sourced [from your secret manager](#external-secrets) rather than written into the values file.',
            'Confirm the cluster CNI enforces the [local AI NetworkPolicy](#helm-values-local-ai), or isolate the service another way.',
            'Align every NATS stream, subject, consumer, and status-update subject across services.',
            'Keep [private-destination blocking](/docs/security/#ssrf-network-policy) enabled and allow only narrowly scoped CIDRs.',
            'Share or externalize synthetic-browser artifact storage.',
            'Provide secure TLS/WSS NATS exposure before enabling remote private locations.',
            'Correct ingress routing and verify browser client URLs in the built frontend image.',
            'Scrape the listeners that actually serve `/metrics`; alert on readiness failures, result-ingest errors, queue backlog, and migration failure.',
            'Run the complete [verification suite](/docs/security/#security-verification) and a restore drill before launch.',
          ],
        },
      ],
    },
  ],
};
