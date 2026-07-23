import type { DocPage } from '../types';

export const DEPLOYMENT_PAGE: DocPage = {
  slug: 'deployment',
  group: 'Build & operate',
  title: 'Deployment',
  description:
    'Run Probara with local processes, Docker Compose, or Kubernetes and Helm, with explicit guidance for databases, NATS, ingress, private locations, migrations, scaling, and current packaging limitations.',
  eyebrow: 'Deployment guide',
  readingTime: '22 min read',
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
              'PostgreSQL and NATS in Docker; locally built Go services and Next.js UI on the host.',
            ],
            [
              '`make start-all`',
              'Testing backend container images locally',
              'PostgreSQL, NATS, and Go services in Compose; Next.js UI still runs on the host.',
            ],
            [
              '`docker compose up -d` / `make up`',
              'Raw backend Compose lifecycle',
              'Compose services only. There is no frontend service in the current root Compose file.',
            ],
            [
              'Helm chart',
              'Kubernetes staging and production',
              'Frontend, services, migration hook, and optionally embedded PostgreSQL/NATS in the cluster.',
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
            'Go 1.23 for the main monorepo. The local launcher compares the installed major/minor version with the root `go.mod` toolchain.',
            'Node.js LTS and npm for the frontend. `scripts/start-ui.sh` can use nvm when it is available.',
            'Helm 3 and `kubectl` for Kubernetes deployment.',
            'A PostgreSQL 16-compatible database and NATS 2.10 with JetStream for production when not using embedded chart dependencies.',
          ],
        },
        {
          type: 'paragraph',
          text:
            'The standalone agent is a separate Go module that currently declares Go 1.21. Root `make test` does not cover it.',
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
            'The Compose model hardcodes an insecure `ADMIN_JWT_SECRET` default and never references `PUBLIC_BASE_URL`; the only interpolated Compose variables (`STATUS_PAGE_PREVIEW_SECRET`, `OIDC_*`, `AUDIT_RETENTION_DAYS`) all carry `:-` defaults. Populate `.env` anyway so the platform runs on real values instead of placeholders.',
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
            'The target starts PostgreSQL and NATS, bootstraps local database access, validates Go, runs migrations, builds downloadable agent binaries, builds and launches each Go service, and starts Next.js on `0.0.0.0:3000`.',
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
          ],
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
            'Scheduler and worker use stream `check-jobs` and subject `check.job`; API falls back to `CHECK_JOBS` and `check.jobs`. Scheduled jobs and on-demand jobs therefore do not share one subject contract.',
            'The root `.env` file is not passed as a service `env_file`; only variables explicitly declared in Compose reach containers.',
            'Encryption, AI, SMTP, asynchronous notifications, public NATS authorization, scheduler mesh/purge, and many result-ingest settings are not wired.',
            'The commented private-location worker example omits required `LOCATION_CREDENTIAL` and should not be enabled as written.',
            'The API and worker correctly share the `synthetic_artifacts` volume used for failure screenshots. Trace and HAR capture are configured by the schema but are not implemented by the current worker.',
          ],
        },
        {
          type: 'callout',
          tone: 'warning',
          title: 'Do not hide these gaps with documentation-only values',
          text:
            'Adding an entry to `.env` does not repair the Compose wiring. Until the Compose file is updated, customize the service environment explicitly and make the queue subjects identical before relying on on-demand checks.',
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
          title: 'Do not deploy the default latest tags to production',
          text:
            'Pin immutable backend, frontend, and migration image tags. Retain JWT and encryption keys across every upgrade; changing them invalidates sessions or makes stored ciphertext unreadable.',
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
            ['`image.tag`', '`latest`', 'Backend image tag. Pin for production.'],
            ['`image.pullSecrets`', '`[]`', 'Backend registry pull secrets.'],
            ['`postgresql.enabled`', '`true`', 'Deploy embedded PostgreSQL StatefulSet.'],
            ['`postgresql.externalUrl`', 'Empty', 'Used when embedded PostgreSQL is disabled.'],
            ['`postgresql.image`', '`postgres:16-alpine`', 'Embedded database image.'],
            ['`postgresql.auth.username`', '`probara`', 'Embedded database user.'],
            ['`postgresql.auth.password`', '`probara-secret`', 'Embedded password; replace it.'],
            ['`postgresql.auth.database`', '`probara`', 'Embedded database name.'],
            ['`postgresql.persistence.enabled`', '`true`', 'Use a PVC for database data.'],
            ['`postgresql.persistence.size`', '`10Gi`', 'Database PVC request.'],
            ['`postgresql.persistence.storageClass`', 'Empty', 'Cluster default storage class.'],
            ['`postgresql.resources`', '100m/256Mi request; 500m/512Mi limit', 'Embedded database resources.'],
            ['`nats.enabled`', '`true`', 'Deploy embedded NATS StatefulSet.'],
            ['`nats.externalUrl`', 'Empty', 'Used when embedded NATS is disabled.'],
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
              '`auth.auditRetentionDays`',
              '`365`',
              'Audit retention days; `0` preserves records indefinitely.',
            ],
            [
              '`auth.oidc.*`',
              'Disabled; blank issuer/client; label `SSO`; JIT viewer/default tenant',
              'Platform OIDC settings. Secret is `secrets.oidcClientSecret`.',
            ],
            [
              '`secrets.adminJwtSecret`',
              'Insecure placeholder default',
              'Ships as `change-me-in-production-jwt-secret-minimum-32-chars`; no template validation rejects it, so replace it with at least 32 random characters.',
            ],
            [
              '`secrets.initialApiKey`',
              'Insecure placeholder',
              'Stored and printed by notes but not consumed by any bootstrap path. It does not create an API key.',
            ],
            [
              '`secrets.probaraSecretsKey`',
              'Empty',
              'Base64 32-byte encryption key. Current chart omits it from scheduler.',
            ],
            [
              '`secrets.oidcClientSecret`',
              'Empty',
              'Required when OIDC is enabled.',
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
              'Alert evaluation workload; advanced alert/SMTP settings are not chart values.',
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
              'Dedicated frontend repository, latest tag, IfNotPresent, no secrets',
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
            'Protect values files and rendered manifests because external URLs may contain credentials. The chart does not accept an existing Secret reference for these URLs.',
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
              'Scheduler receives no encryption key',
              'It cannot decrypt already encrypted monitor/location configuration.',
            ],
            [
              'API subject differs from scheduler/worker',
              'On-demand checks can publish to an unconsumed subject.',
            ],
            [
              'No shared browser-artifact storage',
              'API replicas cannot reliably serve artifacts produced on worker filesystems.',
            ],
            [
              'No `extraEnv`',
              'Many supported runtime variables cannot be configured without changing templates.',
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
            'Pin immutable backend, frontend, agent, migration, PostgreSQL, and NATS versions.',
            'Use managed/HA PostgreSQL and NATS or define tested backup and restore objectives for embedded state.',
            'Configure HTTPS, `ADMIN_COOKIE_SECURE=true`, a correct `PUBLIC_BASE_URL`, and trusted reverse-proxy headers.',
            'Use stable random JWT, encryption, OIDC, preview, SMTP, webhook, database, and NATS secrets from a secret manager.',
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
