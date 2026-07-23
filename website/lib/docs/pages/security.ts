import type { DocPage } from '../types';

export const SECURITY_PAGE: DocPage = {
  slug: 'security',
  group: 'Build & operate',
  title: 'Security',
  description:
    'Harden Probara authentication, tenant access, secrets, outbound monitoring, private locations, NATS, OIDC, notifications, status pages, audit evidence, and production infrastructure.',
  eyebrow: 'Security guide',
  readingTime: '21 min read',
  keywords: [
    'security',
    'SSRF',
    'secrets',
    'OIDC',
    'RBAC',
    'tenant isolation',
    'NATS security',
    'audit',
  ],
  sections: [
    {
      id: 'trust-boundaries',
      title: 'Trust boundaries',
      blocks: [
        {
          type: 'definitions',
          items: [
            {
              term: 'Public edge',
              description:
                'Frontend, API routes intentionally published through the frontend proxy/ingress, agent ingest, and public status pages.',
            },
            {
              term: 'Control plane',
              description:
                'API, scheduler, alerter, PostgreSQL, and platform NATS credentials. This tier can access every tenant’s shared application data.',
            },
            {
              term: 'Execution plane',
              description:
                'Workers execute user-configured network checks and synthetic browser code. Isolate them because their purpose is outbound reachability.',
            },
            {
              term: 'Private location',
              description:
                'A remote worker fleet with a location-scoped NATS identity and no PostgreSQL access.',
            },
            {
              term: 'Presentation plane',
              description:
                'Public status renderer and browser artifact access. Public status content is intentionally anonymous; edit/preview paths are not.',
            },
          ],
        },
        {
          type: 'callout',
          tone: 'warning',
          title: 'Application tenancy is not database row-level security',
          text:
            'Tenant IDs and middleware/repository filters enforce isolation in the application. The shared PostgreSQL service role can read all rows, and the migrations do not establish PostgreSQL RLS policies. Protect DB credentials as platform-wide access and test every new query for tenant scoping.',
        },
      ],
    },
    {
      id: 'roles-scopes-tenancy',
      title: 'Roles, scopes, and tenant isolation',
      blocks: [
        {
          type: 'table',
          columns: ['Identity', 'Scope', 'Write behavior'],
          rows: [
            [
              'Platform `superadmin`',
              'Cross-tenant platform administration',
              'Can perform superadmin and tenant-admin operations.',
            ],
            [
              'Platform `member` + tenant `admin`',
              'Selected tenant',
              'Can write and perform tenant-admin operations.',
            ],
            [
              'Platform `member` + tenant `editor`',
              'Selected tenant',
              'Can create/update ordinary tenant resources, but not tenant-admin-only actions.',
            ],
            [
              'Platform `member` + tenant `viewer`',
              'Selected tenant',
              'Read-only except explicitly allowlisted compute-only POST actions such as monitor test/preview and mesh probe.',
            ],
            [
              'API key, `write` scope',
              'Exactly the key’s tenant',
              'Can write ordinary resources but never passes tenant-admin middleware.',
            ],
            [
              'API key, `read` scope',
              'Exactly the key’s tenant',
              'Read-only plus the same compute-only allowlist.',
            ],
          ],
        },
        {
          type: 'list',
          items: [
            'Do not accept a tenant ID from a request without reconciling it to the authenticated identity and selected membership.',
            'API-key tenant identity is resolved from the stored key, not from a client-chosen tenant header.',
            'Review side-effecting “test,” “preview,” or “probe” endpoints carefully before adding them to the read-only POST allowlist.',
            'Use separate [API keys](/docs/administration/#api-keys) per integration, give them an expiry, select `read` unless mutation is necessary, and revoke them when ownership changes.',
          ],
        },
      ],
    },
    {
      id: 'passwords-api-keys-sessions',
      title: 'Passwords, API keys, and browser sessions',
      blocks: [
        {
          type: 'table',
          columns: ['Credential', 'Storage / validation', 'Operator guidance'],
          rows: [
            [
              'Administrator password',
              'bcrypt with configurable cost, default 12',
              'Use generated unique passwords; keep bcrypt cost within your login latency budget.',
            ],
            [
              'API key',
              'bcrypt hash plus a full SHA-256 lookup value; revoked and expired keys are rejected',
              'The plaintext is available at creation time only. Store it in a secret manager and rotate per client.',
            ],
            [
              'Access token',
              'JWT signed by `ADMIN_JWT_SECRET`, default 15-minute TTL',
              'A JWT secret change invalidates active access tokens across all replicas.',
            ],
            [
              'Refresh token',
              'SHA-256 hash stored in `admin_sessions`; rotated and revocable',
              'Default lifetime is 30 days. Protect database backups because they contain session metadata and hashes.',
            ],
          ],
        },
        {
          type: 'paragraph',
          text:
            'Session cookies are HttpOnly and SameSite=Lax. Their Secure attribute is controlled by `ADMIN_COOKIE_SECURE`, which defaults to false. Production HTTPS deployments must set it to true and preserve one stable JWT secret across API replicas.',
        },
        {
          type: 'callout',
          tone: 'warning',
          title: 'CORS currently reflects arbitrary Origin values',
          text:
            'Authenticated API and alert SSE paths echo a supplied `Origin` and set `Access-Control-Allow-Credentials: true`; there is no configured origin allowlist. SameSite=Lax provides some browser protection, but keep API traffic same-origin behind the frontend/reverse proxy or add explicit trusted-origin enforcement.',
        },
        {
          type: 'callout',
          tone: 'warning',
          title: 'Edge controls are still required',
          text:
            'The application does not provide a general login/API rate limiter or a complete production security-header policy. Apply rate limits, request-size limits, HSTS, CSP where compatible, frame protection, content-type protection, and trusted proxy/header rules at the ingress or reverse proxy.',
        },
      ],
    },
    {
      id: 'oidc-security',
      title: 'OIDC security',
      blocks: [
        {
          type: 'list',
          items: [
            'Use an exact HTTPS issuer and callback. The default callback is `<PUBLIC_BASE_URL>/api/v1/auth/oidc/callback`.',
            'Register only the needed scopes: default `openid profile email`.',
            'Store `OIDC_CLIENT_SECRET` outside source control and rotate it using the IdP’s overlapping-secret procedure when possible.',
            'Use JIT role `viewer` unless a stronger business requirement exists. Validate `OIDC_JIT_DEFAULT_TENANT_ID` because the loader does not validate UUID syntax.',
            'Disable JIT provisioning when accounts must be approved in advance; pre-created users are matched using verified identity information.',
            'Keep `PUBLIC_BASE_URL` stable and canonical to avoid callback confusion behind proxies.',
            'Review IdP claim and email-verification behavior during every provider change.',
          ],
        },
        {
          type: 'callout',
          tone: 'warning',
          title: 'Development Dex credentials are not production credentials',
          text:
            'The optional Compose Dex profile includes local example identities and passwords. Do not expose it or carry its values into a real IdP configuration.',
        },
      ],
    },
    {
      id: 'secret-inventory',
      title: 'Secret inventory and ownership',
      blocks: [
        {
          type: 'table',
          columns: ['Secret', 'Consumers', 'Rotation impact'],
          rows: [
            [
              '`ADMIN_JWT_SECRET`',
              'Every API replica',
              'Changing invalidates access tokens/OIDC state. Deploy consistently across replicas.',
            ],
            [
              '`PROBARA_SECRETS_KEY*`',
              'API, scheduler, worker, alerter, maintenance CLIs',
              'Historical versions are needed to open existing ciphertext.',
            ],
            [
              '`OIDC_CLIENT_SECRET`',
              'API',
              'Coordinate with IdP client rotation.',
            ],
            [
              '`STATUS_PAGE_PREVIEW_SECRET`',
              'API and status-page service',
              'Changing invalidates outstanding one-hour preview tokens.',
            ],
            [
              '`NATS_LOCATION_AUTH_ISSUER_SEED`',
              'API only',
              'NATS receives the public account key, not the seed. Rotation requires coordinated broker/API changes.',
            ],
            [
              'NATS platform password',
              'Internal services',
              'Rotate across broker and every workload without leaving anonymous access.',
            ],
            [
              'Location credential',
              'API database and one location fleet',
              'Regenerate deployment info and replace every worker replica for that location.',
            ],
            [
              'SMTP password / webhook HMAC secret',
              'Alerter or notification workers',
              'Channel delivery can fail during uncoordinated rotation.',
            ],
            [
              'Tenant LLM API key',
              'API and DB-connected AI workers',
              'Stored encrypted and masked from clients; rotate with provider and test before removing old key.',
            ],
            [
              'PostgreSQL URL and credentials',
              'Platform services',
              'Database role currently spans tenant data; rotate as a platform credential.',
            ],
          ],
        },
        {
          type: 'list',
          items: [
            'Never commit `.env`, Helm production values, generated location snippets, backup files, or decrypted configuration.',
            'Kubernetes Secrets are base64 transport objects, not encrypted storage by themselves. Enable etcd encryption and use workload identity/external secret management where available.',
            'Helm stores release values in-cluster; avoid putting secrets into command history and protect the namespace/release metadata.',
            'The local launcher chmods the development JWT file to 0600, but the development encryption-key file relies on the current umask. Verify and restrict its permissions.',
            'Use separate secrets for preview signing, JWT signing, encryption, webhooks, NATS issuer, and location credentials; do not reuse one master string.',
          ],
        },
      ],
    },
    {
      id: 'encryption-at-rest',
      title: 'Encryption at rest and key rotation',
      blocks: [
        {
          type: 'paragraph',
          text:
            'When a valid `PROBARA_SECRETS_KEY` keyring is configured, sensitive configuration is stored in self-describing AES-256-GCM JSON envelopes containing algorithm, key version, nonce, and ciphertext. The base key and every [rotation key](/docs/configuration/#encryption-keys) must decode from base64 to exactly 32 bytes.',
        },
        {
          type: 'code',
          language: 'bash',
          title: 'Generate and introduce a rotation key',
          code: `# Keep V1 configured while introducing V2.
export PROBARA_SECRETS_KEY='existing-base64-key'
export PROBARA_SECRETS_KEY_V2="$(openssl rand -base64 32)"
export POSTGRES_URL='postgres://...'

# Preview and then re-encrypt monitor secret fields with the highest key version.
go run ./cmd/admin/reencrypt_monitor_configs -dry-run
go run ./cmd/admin/reencrypt_monitor_configs`,
        },
        {
          type: 'code',
          language: 'bash',
          title: 'Encrypt legacy plaintext alert channels',
          code: `go run ./cmd/admin/encrypt_existing_channels -dry-run
go run ./cmd/admin/encrypt_existing_channels`,
        },
        {
          type: 'list',
          ordered: true,
          items: [
            'Back up PostgreSQL and all currently configured key versions.',
            'Add the next `PROBARA_SECRETS_KEY_V<n>` to every data-handling service before writing with it.',
            'Restart/roll out services and verify each replica reports the expected keyring.',
            'Dry-run and execute monitor re-encryption; encrypt any legacy plaintext channels.',
            'Inventory ciphertext envelope versions across monitor, alert-channel, location, and AI/settings data.',
            'Remove an old key only after every relevant envelope is migrated and a restore test succeeds without it.',
          ],
        },
        {
          type: 'callout',
          tone: 'warning',
          title: 'Current tooling does not rotate every ciphertext family',
          text:
            '`reencrypt_monitor_configs` handles plaintext and older monitor envelopes. `encrypt_existing_channels` skips channels that are already encrypted, even with an older key, and there is no general location/AI/channel rotation command. Retain historical keys until you have separately verified and migrated all envelope families.',
        },
        {
          type: 'callout',
          tone: 'warning',
          title: 'Missing encryption can silently create plaintext',
          text:
            'Direct runtimes can use a NoOp encryptor when the base key is absent, leaving new values plaintext. The NoOp path refuses to decrypt a real envelope, so losing a key causes explicit failures. Set encryption before production data is created.',
        },
      ],
    },
    {
      id: 'ssrf-network-policy',
      title: 'SSRF and worker network policy',
      blocks: [
        {
          type: 'paragraph',
          text:
            '`HTTP_BLOCK_PRIVATE_IPS=true` is the secure default. Despite its historical name, the guard is shared by HTTP, browser, TCP, gRPC, PostgreSQL, MySQL, MongoDB, Redis, broker, SIP, and other worker dial paths.',
        },
        {
          type: 'table',
          columns: ['Control', 'Recommended setting', 'Effect'],
          rows: [
            [
              '`HTTP_BLOCK_PRIVATE_IPS`',
              '`true`',
              'Reject loopback, RFC1918, carrier-grade NAT, link-local, multicast, unspecified, documentation, benchmark, transition, and reserved IPv4/IPv6 destinations.',
            ],
            [
              '`HTTP_ALLOWED_CIDRS`',
              'Only explicit trusted ranges',
              'Overrides the block policy for matching destinations. Invalid CIDR fails worker startup.',
            ],
            [
              '`SIP_LOCALHOST_AS_HOST_GATEWAY`',
              '`false` outside local development',
              'Avoids rewriting localhost toward the Docker host.',
            ],
            [
              'Kubernetes/network firewall egress',
              'Allow DNS, required public targets, NATS, and approved private networks',
              'Provides defense in depth if an application checker or future integration bypasses the guard.',
            ],
          ],
        },
        {
          type: 'list',
          items: [
            'Hostname dials resolve candidate IPs and validate them before connecting.',
            'Browser validation rejects a hostname if any returned address is blocked, reducing mixed public/private DNS selection risk.',
            'Use a [dedicated location worker](/docs/locations/#target-policy) for internal monitoring and grant that location only the internal CIDRs it owns.',
            'Do not globally disable blocking to monitor one private target.',
            'Treat `tls_skip_verify` and custom CA settings as scoped exceptions; prefer correct certificate chains and server names.',
            'Run workers without cloud instance metadata access and with a minimally privileged service account.',
          ],
        },
        {
          type: 'callout',
          tone: 'warning',
          title: 'Notification and LLM egress are separate',
          text:
            'The monitor dial guard does not automatically protect alerter/notification webhooks or arbitrary LLM provider base URLs. Restrict those services with network policy, proxy allowlists, DNS policy, or provider allowlists so tenant-controlled destinations cannot reach sensitive internal services.',
        },
      ],
    },
    {
      id: 'private-locations-nats',
      title: 'Private locations and NATS hardening',
      blocks: [
        {
          type: 'paragraph',
          text:
            '[Private locations](/docs/locations/#credentials-and-secrets) authenticate to NATS with location UUID as username and a 32-byte random URL-safe credential as password. The API authorization callout validates only active, non-deleted locations and issues a one-hour NATS user claim scoped to that location.',
        },
        {
          type: 'list',
          items: [
            'Use `tls://` or `wss://` for every public broker endpoint. Deploy generation rejects plaintext schemes and URLs that already contain userinfo.',
            'Keep the account issuer seed only in API. Configure only its public account key in NATS.',
            'Use a separate platform NATS user for trusted internal services. Never give it to a location worker.',
            'Location claims restrict result publication, heartbeat publication, consumer pull/ACK operations, inbox responses, and test-job subscription to that location.',
            'Monitor and revoke disabled/deleted location credentials. Replace credentials if a generated deploy URL is exposed in logs, shell history, manifests, or tickets.',
            'Remote workers must not receive PostgreSQL credentials or the platform-wide encryption key. Monitor configuration secrets are re-encrypted with a key derived from the location credential.',
            'Expose mesh echo endpoints only across intended private networks; they identify location connectivity and should not publish `/metrics` externally.',
          ],
        },
        {
          type: 'callout',
          tone: 'warning',
          title: 'Local Compose NATS is not a private-location broker',
          text:
            'It listens without authentication or TLS. The embedded Helm NATS service is ClusterIP-only until you add secure external exposure. Setting `PUBLIC_NATS_URL` alone does not create that exposure.',
        },
      ],
    },
    {
      id: 'notifications-integrations-ai',
      title: 'Notifications, webhooks, SMTP, and AI',
      blocks: [
        {
          type: 'definitions',
          items: [
            {
              term: 'Webhook signing',
              description:
                'When a webhook secret is configured, Probara sends `X-Probara-Signature: sha256=<HMAC>` so receivers can authenticate the raw body.',
            },
            {
              term: 'SMTP TLS',
              description:
                '`SMTP_USE_TLS=true` performs direct implicit TLS. It does not issue STARTTLS, so verify protocol and port with the provider.',
            },
            {
              term: 'Tenant AI key',
              description:
                'Encrypted at rest when platform secret encryption is configured and never returned to clients; only a “has key” indicator is exposed.',
            },
          ],
        },
        {
          type: 'list',
          items: [
            'Verify webhook signatures with constant-time comparison, reject stale/replayed events according to your receiver policy, and rotate per channel.',
            'Do not place secrets in webhook URLs when headers or signing are available; URLs appear in proxy and DNS logs.',
            'Restrict webhook destinations with egress policy because the worker monitor CIDR guard does not cover the notification plugin.',
            'Use a dedicated SMTP account with send-only permissions and a controlled From domain.',
            'Review alert and RCA payloads before sending them to third-party notification or LLM providers; error bodies, URLs, headers, and incident context can be sensitive.',
            'Allowlist AI provider origins and use a model/account with retention and data-processing terms appropriate for your environment.',
            'Keep asynchronous dispatch stream/consumer permissions separate from check execution permissions.',
          ],
        },
      ],
    },
    {
      id: 'status-pages-artifacts',
      title: 'Status pages, previews, and browser artifacts',
      blocks: [
        {
          type: 'list',
          items: [
            'Published [status pages](/docs/status-pages/#page-model) are intentionally public. Do not include private monitor URLs, internal hostnames, customer names, or operational notes unless disclosure is intended.',
            'Set one strong shared `STATUS_PAGE_PREVIEW_SECRET` on API and status service. Without it, preview-token enforcement is disabled.',
            'Preview tokens are HMAC-SHA256 values with a one-hour expiry. Treat preview URLs as temporary bearer links.',
            'The status service’s optional API proxy is intentionally restricted, but should still be exposed only through the expected status editing/preview flow.',
            'Synthetic browser screenshots can contain credentials, cookies, personal data, or application content. Store them encrypted, authorize retrieval, and expire them. Apply the same controls if trace or HAR capture is implemented later.',
            'Never make `SYNTHETIC_BROWSER_ARTIFACTS_DIR` a public static directory.',
          ],
        },
      ],
    },
    {
      id: 'audit-proxy-trust',
      title: 'Audit evidence and trusted proxies',
      blocks: [
        {
          type: 'paragraph',
          text:
            '[Audit records](/docs/administration/#audit) capture time, tenant, actor type/ID/label, action, resource, outcome, HTTP status, IP, user agent, and structured details. Writes are asynchronous in batches so audit storage does not block product requests.',
        },
        {
          type: 'list',
          items: [
            'The recorder buffer holds 1,024 events, flushes every two seconds or 100 records, and drops rather than blocking when full. Watch for “Audit buffer full; dropping events” warnings.',
            'A failed audit insert does not fail the original API request. Forward application logs and audit records to a protected external system when compliance requires stronger durability.',
            '`AUDIT_RETENTION_DAYS` defaults to 365; `0` retains forever. This is separate from tenant telemetry retention.',
            'Restrict audit search/export to appropriate administrative roles and treat IP/user-agent/details as potentially personal or sensitive data.',
            'Back up audit data and preserve time synchronization across services.',
          ],
        },
        {
          type: 'callout',
          tone: 'warning',
          title: 'X-Forwarded-For is trusted without a proxy allowlist',
          text:
            'Audit and session code use the first `X-Forwarded-For` value when present. Ensure the public edge strips client-supplied forwarding headers and writes the canonical client address; never expose API directly behind an untrusted pass-through proxy.',
        },
      ],
    },
    {
      id: 'infrastructure-hardening',
      title: 'Database, broker, containers, and ingress',
      blocks: [
        {
          type: 'table',
          columns: ['Layer', 'Minimum production controls'],
          rows: [
            [
              'PostgreSQL',
              'TLS verification, private networking, least-privileged non-superuser service role, encrypted storage/backups, monitored replication/PITR, credential rotation, and restore drills.',
            ],
            [
              'NATS',
              'TLS/WSS, platform authentication, location authorization callout, account/subject permissions, JetStream persistence/replication, monitoring endpoint isolation, and credential rotation.',
            ],
            [
              'Kubernetes',
              'Restricted namespace RBAC, Pod Security admission, non-root pods, dropped capabilities, seccomp, network policies, secret encryption, image signing/scanning, and immutable tags/digests.',
            ],
            [
              'Ingress',
              'HTTPS-only, HSTS, trusted proxy headers, body/time limits, rate limiting, origin policy, SSE buffering disabled where required, and separate routing for public status pages.',
            ],
            [
              'Workers',
              'Dedicated node pool or strong workload isolation, minimal service account, read-only filesystem where compatible, restricted egress, no metadata access, and bounded CPU/memory/concurrency.',
            ],
          ],
        },
        {
          type: 'callout',
          tone: 'warning',
          title: 'Bundled defaults favor evaluation',
          text:
            'Compose uses development database credentials and unauthenticated NATS. Helm defaults to `latest` images, simple single-instance embedded stateful services, writable root filesystems, and no network policies. Harden or replace these defaults before production.',
        },
      ],
    },
    {
      id: 'security-verification',
      title: 'Security verification checklist',
      blocks: [
        {
          type: 'list',
          items: [
            'Confirm all public URLs use HTTPS and every remote NATS URL uses TLS/WSS with certificate validation.',
            'Verify `ADMIN_COOKIE_SECURE=true`, stable random JWT/preview/encryption keys, and exact OIDC callback values.',
            'Enumerate API keys by owner, tenant, scope, expiry, and last use; revoke unknown or overprivileged keys.',
            'Test viewer/editor/admin/superadmin and read/write API-key boundaries across representative endpoints.',
            'Attempt loopback, metadata, RFC1918, IPv6 local, mixed-DNS, redirect, browser, database, and broker SSRF cases from each worker class.',
            'Inspect effective Kubernetes environment and NATS permissions rather than relying on values files.',
            'Verify scheduler has every encryption key and can execute encrypted monitors.',
            'Confirm webhook and LLM egress cannot reach internal metadata/control-plane addresses.',
            'Test location disable/revocation and ensure one location cannot consume another location’s jobs.',
            'Validate audit capture, drop warnings, retention, export restrictions, and external log durability.',
            'Restore a database backup with the complete keyring and verify encrypted channels, monitors, locations, and AI settings.',
            'Scan images/dependencies and run Go race tests, frontend production builds, Helm lint, and integration tests before release.',
          ],
        },
      ],
    },
    {
      id: 'credential-incident',
      title: 'Credential-exposure response',
      blocks: [
        {
          type: 'table',
          columns: ['Exposed material', 'Immediate response'],
          rows: [
            [
              'API key',
              'Revoke the key, create a scoped replacement, inspect audit/last-use records, and update only its owning integration.',
            ],
            [
              'Administrator password/session',
              'Reset password, revoke sessions, inspect audit activity, and consider JWT rotation if token-signing material was exposed.',
            ],
            [
              'JWT secret',
              'Replace on every API replica simultaneously, invalidate all sessions, and investigate forged-token exposure window.',
            ],
            [
              'Encryption key',
              'Restrict database/backup access, introduce a new version, migrate covered ciphertext, retain the compromised key only as long as recovery requires, and assess data disclosure.',
            ],
            [
              'Location credential/deploy URL',
              'Regenerate/revoke the location credential, restart all location workers, and inspect NATS authorization/subject activity.',
            ],
            [
              'NATS platform credential or issuer seed',
              'Rotate broker and workload credentials; for issuer compromise, coordinate a new account key across NATS and API and revoke old trust.',
            ],
            [
              'OIDC/SMTP/LLM/webhook secret',
              'Rotate at the provider, update all consumers, test the integration, and review provider-side activity/logs.',
            ],
          ],
        },
      ],
    },
  ],
};
