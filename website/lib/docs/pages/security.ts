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
              'Can create/update ordinary tenant resources, but not tenant-admin-only actions (which include managing, running or testing SIP test-call monitors).',
            ],
            [
              'Platform `member` + tenant `viewer`',
              'Selected tenant',
              'Read-only except explicitly allowlisted compute-only POST actions such as monitor test/preview and mesh probe.',
            ],
            [
              'API key, `write` scope',
              'Exactly the key’s tenant',
              'Can write ordinary resources but never passes tenant-admin checks, so it cannot manage, run or test a SIP test-call monitor.',
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
      id: 'login-lockout-sessions',
      title: 'Login lockout and session revocation',
      blocks: [
        {
          type: 'paragraph',
          text:
            'Password login is protected in the database, so the limits hold across API replicas. Ten failed passwords in a row lock the account for 15 minutes; while it is locked even the correct password is refused without being checked. Each further ten failures locks it again for twice as long, up to 4 hours. A successful login resets the count, and a failure streak is forgotten after 24 hours without failures. Attempts are counted before the password is compared, so parallel guesses cannot exceed the limit.',
        },
        {
          type: 'list',
          items: [
            'A locked account, a throttled address and an unknown username all get the same `429` with `Retry-After`; wrong and unknown credentials share one `401`. Unknown and SSO-only usernames are throttled like real accounts and cost the same bcrypt comparison, so the responses do not reveal which accounts exist.',
            'Each API replica also blocks a client address for 15 minutes after 50 failed logins (resolved through [`TRUSTED_PROXY_CIDRS`](#audit-proxy-trust)). This layer is per replica.',
            'Superadministrators see the lock on the user page and can lift it with **Unlock** (`POST /api/v1/users/{id}/unlock`, MCP `unlock_user`). [`go run ./cmd/admin`](/docs/operations/#database-migrations) also clears it, and so does a superadministrator setting a new password.',
            'Audit events: `auth.login` failures and denials (with the reason), `auth.lockout` when a lock starts, `auth.unlock`.',
            'SSO (OIDC) sign-in is not affected; the identity provider owns its own protection.',
          ],
        },
        {
          type: 'paragraph',
          text:
            'Every sign-in is a session family: refresh-token rotation stays inside it, and the access token names it in its `sid` claim. Signing a session out revokes the family, which ends its refresh token and its access token on their next request. Users manage their own sessions on the **Account** page (sign out one browser, every other one, or everywhere); superadministrators can sign a user out everywhere from the user page (`DELETE /api/v1/users/{id}/sessions`, MCP `revoke_user_sessions`). Logout, and a superadministrator setting a password, end sessions the same way. API keys and MCP connections are separate credentials and are not affected.',
        },
        {
          type: 'callout',
          tone: 'info',
          title: 'Lockout is a denial-of-service lever',
          text:
            'Anyone who knows a username can keep that account locked by failing on purpose. Prefer non-obvious administrator usernames, keep at least one break-glass path (`cmd/admin` with database access), and watch for `auth.lockout` events in the audit log.',
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
              'Use generated unique passwords; keep bcrypt cost within your login latency budget. Repeated failures [lock the account](#login-lockout-sessions).',
            ],
            [
              'API key',
              'bcrypt hash plus a full SHA-256 lookup value; revoked and expired keys are rejected',
              'The plaintext is available at creation time only. Store it in a secret manager and rotate per client.',
            ],
            [
              'Access token',
              'JWT signed by `ADMIN_JWT_SECRET`, default 15-minute TTL. Carries the user ID and the sign-in (`sid`), never roles; the API checks the sign-in on every request',
              'A JWT secret change invalidates active access tokens across all replicas. Signing a session out rejects its access token on the next request, not at expiry.',
            ],
            [
              'Refresh token',
              'SHA-256 hash stored in `admin_sessions`; rotated on use within its sign-in (session family), with a 60-second reuse window so concurrent refreshes (parallel requests, multiple tabs) do not invalidate the session',
              'Default lifetime is 30 days. The reuse window also means a stolen refresh token can be replayed for up to 60 seconds after rotation, unless the session is signed out: that rejects every token of the sign-in at once. Protect database backups because they contain session metadata and hashes.',
            ],
            [
              'MCP OAuth tokens',
              'Opaque `pbm_at_` (1 hour) and `pbm_rt_` (30 days) values, SHA-256 hashed in `mcp_oauth_tokens`; refresh tokens rotate on every use with no reuse window',
              'Presenting a rotated refresh token or replaying an authorization code revokes the whole connection. Access tokens act as the consenting user, are accepted only on the MCP endpoint and requests it dispatches, and their read/write scope can only narrow the user\'s role. See [MCP authentication](/docs/mcp/#authentication).',
            ],
            [
              'Discovery collector token',
              '32 random bytes; only its SHA-256 hash is stored, in `discovery_integrations.token_hash`. The plaintext is shown only in the response that creates the integration or rotates its token',
              'Valid only on `/api/v1/discovery-collectors/{id}/*` for its own integration (config and snapshot upload), never on tenant APIs, so it cannot read coverage or create monitors. It lives in the Kubernetes Secret of the [generated manifest](/docs/discovery/#setup); delete the downloaded file after applying it. The access manifest offered after adding namespaces holds only Roles and RoleBindings, never the token. Rotating (or reinstalling) invalidates the old token at once.',
            ],
          ],
        },
        {
          type: 'paragraph',
          text:
            'Session cookies are HttpOnly and SameSite=Lax. Their Secure attribute is controlled by `ADMIN_COOKIE_SECURE`, which defaults to false. Production HTTPS deployments must set it to true and preserve one stable JWT secret across API replicas.',
        },
        {
          type: 'paragraph',
          text:
            'Cross-origin browser access is an allowlist: only origins in `API_ALLOWED_ORIGINS` get `Access-Control-Allow-Origin` (with credentials) and preflight answers; every other `Origin` gets no CORS headers, so the browser withholds the response. The default, empty, means same-origin only, which is all the operator UI needs because it calls the API through its own `/api` proxy. The public OAuth/MCP discovery and token routes keep a wildcard policy without credentials.',
        },
      ],
    },
    {
      id: 'edge-controls',
      title: 'Security headers and rate limits',
      blocks: [
        {
          type: 'table',
          columns: ['Surface', 'Headers'],
          rows: [
            [
              'API',
              '`X-Content-Type-Options: nosniff`, `Referrer-Policy: no-referrer`, `X-Frame-Options: DENY` and `Content-Security-Policy: default-src \'none\'; frame-ancestors \'none\'` on every response.',
            ],
            [
              'Operator UI',
              '`nosniff`, `X-Frame-Options: DENY`, `Referrer-Policy: no-referrer` and a `Permissions-Policy` that denies camera, microphone and geolocation. A nonce-based Content-Security-Policy (`\'strict-dynamic\'`, `frame-ancestors \'none\'`, `frame-src` limited to the status-page origin) ships **report-only** for this release: violations appear in the browser console but nothing is blocked. Set `WEB_CSP_ENFORCE=true` to enforce it.',
            ],
            [
              'Status pages',
              '`nosniff` and `Referrer-Policy: no-referrer` on every response; each public page adds its own `Content-Security-Policy` with a per-response script nonce, chosen by the page’s [CSP mode](/docs/status-pages/#content-security-policy), and a `frame-ancestors` list of the page’s origin, the operator UI and the page’s allowed embed origins.',
            ],
          ],
        },
        {
          type: 'paragraph',
          text:
            'HSTS (`Strict-Transport-Security: max-age=…; includeSubDomains`) is off until `SECURITY_HSTS_MAX_AGE` is positive, and even then is sent only on HTTPS requests: TLS terminated by the service, or `X-Forwarded-Proto: https` from a peer in `TRUSTED_PROXY_CIDRS`. A forwarding header from any other peer never counts. Compose defaults to off; Helm turns it on (one year) when the chart’s ingress has `tls` configured.',
        },
        {
          type: 'list',
          items: [
            'The API has a general rate limit: a token bucket of `API_RATE_LIMIT_PER_MINUTE` (default 600) with `API_RATE_LIMIT_BURST` (default 120). Authenticated requests are keyed by credential — API key, MCP grant, or session user — so users behind one NAT or proxy do not share a bucket. Requests without a credential are keyed by client address, resolved through the [trusted proxy list](#audit-proxy-trust).',
            'A request that presents a credential and gets 401 also spends from an authentication-failure bucket for its address. Once that bucket is empty, credentialed requests from the address are refused before authentication runs, so guessing API keys cannot buy unlimited database lookups.',
            'Over the limit the API answers `429 Too Many Requests` with `Retry-After` in seconds. `0` disables the limiter.',
            'Exempt: health and metrics probes, `/static`, push heartbeats (`/api/v1/push`), OTLP export (limited per monitor by `OTLP_MONITOR_RATE_PER_MIN`), discovery collectors, and the alert stream.',
            'Buckets live in memory on each API replica, so the effective cluster-wide ceiling is the limit times the replica count, and a restart resets them. Behind a proxy that is not in `TRUSTED_PROXY_CIDRS`, every anonymous visitor shares the proxy’s bucket — list your ingress there.',
            'The limiter bounds request volume per credential; it is not an account lockout for password guessing.',
          ],
        },
        {
          type: 'callout',
          tone: 'info',
          title: 'Keep ingress limits too',
          text:
            'The application limits are per replica and in memory. Keep request-size limits, connection limits, and a coarse per-address rate limit at the ingress or reverse proxy as the first line of defense.',
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
            'Prefer [OIDC group mappings](/docs/administration/#oidc-group-mappings) over broad JIT defaults when the IdP manages access: roles are re-derived from IdP groups on every login, so removing a user from a group revokes their access at their next sign-in (active sessions are re-checked per request, so demotions bite immediately after the sync).',
            'Group-mapping misconfiguration cannot fully lock you out: the sync never demotes the last active superadmin, local password login is unaffected, and deleting all mapping rows instantly restores manual role management.',
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
              'Discovery collector token',
              'API database (hash only) and the collector Secret in one cluster',
              'Rotate with `POST /api/v1/discovery/integrations/{id}/rotate-token` (or Reinstall in the UI), then apply the new manifest; the old token stops working immediately.',
            ],
            [
              'SMTP password / webhook HMAC secret',
              'Alerter or notification workers',
              'Channel delivery can fail during uncoordinated rotation.',
            ],
            [
              'Retired AI provider key (rollback only)',
              'None: no current service reads it',
              'Ciphertext left in the legacy `ai_settings` provider columns by pre-local-AI releases, kept only so a rollback still works (migration 000092). Keep the key version that encrypted it until a rollback to a pre-local-AI release is ruled out; then clear the column (`UPDATE ai_settings SET api_key_encrypted = \'\'`) instead of rotating it.',
            ],
            [
              'Monitor request credentials',
              'API (encrypt, mask), scheduler (location re-encryption), worker',
              'Stored per monitor: database passwords, connection strings and client keys, SIP and Prometheus credentials, HTTP `basic_auth_password` and `secret_headers`, gRPC `secret_metadata`, synthetic `secret_variables`, and WebSocket headers ([coverage](/docs/monitors/#secrets)). Rotating one is an ordinary monitor edit; plaintext HTTP `headers` are not secret, and credential-named ones are moved to `secret_headers` on save.',
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
            'Helm stores release values in-cluster; avoid putting secrets into command history and protect the namespace/release metadata. Every chart credential can instead be [read from a Secret you supply](/docs/deployment/#external-secrets), which keeps it out of the release values entirely.',
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
go run ./cmd/admin/reencrypt_monitor_configs

# Once, after upgrading: move credentials saved in plaintext (HTTP headers,
# gRPC metadata, literal synthetic API step headers, credential-named
# synthetic variables such as password) into encrypted secrets.
go run ./cmd/admin/migrate_sensitive_headers          # dry run
go run ./cmd/admin/migrate_sensitive_headers -apply`,
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
            'Inventory ciphertext envelope versions across monitor, alert-channel and location data, plus any retired AI provider key (rollback only).',
            'Remove an old key only after every relevant envelope is migrated and a restore test succeeds without it.',
          ],
        },
        {
          type: 'callout',
          tone: 'warning',
          title: 'Current tooling does not rotate every ciphertext family',
          text:
            '`reencrypt_monitor_configs` handles plaintext and older monitor envelopes. `encrypt_existing_channels` skips channels that are already encrypted, even with an older key, and there is no general location/channel rotation command. Retain historical keys until you have separately verified and migrated all envelope families. Retired AI provider ciphertext is never rotated: keep its key or clear the column.',
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
            '`HTTP_BLOCK_PRIVATE_IPS=true` is the recommended setting; the code default is `false`, and neither the Helm chart nor Compose sets it, so set it on every worker that runs inside a network it should not reach. Despite its historical name, the guard is shared by HTTP, browser, ping, TCP, gRPC, PostgreSQL, MySQL, MongoDB, Redis, broker, SIP, and other worker dial paths (ping vets the addresses it resolves before sending any echo).',
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
              'Keeps a SIP target host that is `localhost` or a loopback IP (the monitor host or an SRV target) from being rewritten to `host.docker.internal`. Only the Compose worker turns the rewrite on, for development (`make start-all-local` runs its worker on the host and leaves it off); the rewritten host is still dialed through the network policy.',
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
            "A [SIP test call with `call.media`](/docs/monitors/#network-protocols) sends audio to the address the far end's SDP answer names, which the remote server chose rather than the operator, so it is vetted more strictly than a target before a single packet is sent: the port must be 1024-65535 (keeping the stream off DNS, NTP, SNMP and syslog); unspecified addresses and `0.0.0.0/8`, multicast and `240.0.0.0/4`, link-local addresses including cloud metadata, and the broadcast or network address of any of the worker's own IPv4 subnets (`broadcast`, which would otherwise reach every host on that segment) are refused whatever the policy or `HTTP_ALLOWED_CIDRS` say; the signalling peer's own address is allowed; loopback only when the peer is loopback; then the worker's policy applies; and, even with `HTTP_BLOCK_PRIVATE_IPS=false`, a private or reserved address named by a public peer is refused (`private_behind_public`) unless `HTTP_ALLOWED_CIDRS` lists it, while a private peer naming a private address (an on-premises estate) passes. A NAT64 (`64:ff9b::/96`, `64:ff9b:1::/48`) or 6to4 (`2002::/16`) address is judged on its embedded IPv4 address too, so an IPv6-only worker cannot reach `10.0.0.1` through `64:ff9b::a00:1`; Teredo and documentation prefixes count as reserved. A hostname in the answer is refused, never resolved, so the far end cannot steer the worker's DNS lookups.",
            "Media leaves from the call's bound, unconnected RTP and RTCP sockets, which surface no ICMP errors, so the stream cannot probe which ports are open, and which have broadcast turned off (`SO_BROADCAST`), so the kernel refuses any broadcast destination the vetting did not catch, such as a subnet the worker is not on but routes to. RTCP goes only to the media destination's IP, at the answer's `a=rtcp` port or the RTP port + 1, vetted by the same rules; an `a=rtcp` naming any other address gets no RTCP, so the far end cannot name a second host. The far end's RTCP is read only from the addresses audio is accepted from. Latching is restricted: the probe moves its audio at most once, to another port of the address the answer named, or, when that address was refused, to the signalling peer's IP; the new target is vetted again, and the destination never moves to any other address. Packets from any other source are counted, never analysed. Refusals are reported as coarse reason codes, and no media address ever appears in an error message or warning, which reach notifications; addresses appear only in the check's `metrics_data`, which the operator UI reads and status pages never show.",
            "What a hostile far end can do with this is bounded: at most about 100 KB of fixed, probe-generated content (silence, or the test tone) per call, plus at most 3 small RTCP packets, toward one public address it names, never into the worker's private network, to metadata, to a well-known port, or to a second host. A tenant admin who points a monitor at a SIP server of their own controls both the trigger and the address, so they could aim probe audio at a third party: about 80 kbit/s per call, at most `WORKER_CONCURRENCY` scheduled calls plus 16 `Test` calls per worker at once, from monitors limited to one call a minute per location and holds of at most 10 s; only a signed-in tenant admin can create or run a test call, and every change and test is in the audit log. A tenant admin who points SIP at a private peer (allowed with `HTTP_BLOCK_PRIVATE_IPS=false`) can have that peer name any private media address, which is no wider than the SIP egress to that network they already have.",
            "A SIP monitor's `nameserver` is dialed under the same policy as its targets. Without one, its RFC 3263 NAPTR query goes to the worker's own `/etc/resolv.conf` nameservers, the resolver every hostname lookup already uses: the policy lets exactly those addresses through.",
            'Browser validation rejects a hostname if any returned address is blocked, reducing mixed public/private DNS selection risk.',
            'Use a [dedicated location worker](/docs/locations/#target-policy) for internal monitoring and grant that location only the internal CIDRs it owns.',
            'Do not globally disable blocking to monitor one private target.',
            "[Kubernetes discovery](/docs/discovery/#limits) marks a suggestion internal from its name alone (Service addresses, IP literals, single-label names and `.svc`, `.cluster.local`, `.local`, `.internal` hosts) and requires a private location only for those. That flag is a routing hint, not an SSRF control: a public-looking Ingress host that resolves to a private address is classed external and can be approved onto the shared fleet. Keep `HTTP_BLOCK_PRIVATE_IPS=true` on every fleet that runs discovered checks, with `HTTP_ALLOWED_CIDRS` granting a private location only the cluster ranges it should reach.",
            'Treat `tls_skip_verify` and custom CA settings as scoped exceptions; prefer correct certificate chains and server names.',
            'Run workers without cloud instance metadata access and with a minimally privileged service account.',
          ],
        },
        {
          type: 'callout',
          tone: 'warning',
          title: 'Local AI evidence boundary',
          text:
            'AI calls go to an installation-owned CPU service (`ai-service/`) that only the API and the database-connected worker are configured to call; private-location workers never receive its URL or token. The service holds only its bearer token, never database, broker or platform credentials, and needs no outbound network: the image carries the model and runs with model-hub access disabled. In Helm it also gets no service-account token, runs non-root on a read-only root filesystem, and its NetworkPolicy denies all egress. Workspace users cannot supply inference destinations or credentials. Evidence excludes configuration secrets, request/response bodies, screenshots and arbitrary text. [AI training labels](#notifications-integrations-ai) store and export that same evidence and nothing more.',
        },
        {
          type: 'paragraph',
          text:
            'Notification channels (generic webhook, Slack, Discord, Teams, Mattermost, Google Chat, Matrix, PagerDuty, Opsgenie, Telegram, Twilio, ntfy, Gotify, Pushover) have their own policy, because their destinations are tenant-supplied and the requests leave the API, alerter and worker unattended. It uses the same address table and resolve-then-validate dialer as the monitor guard, but it is on by default and configured separately, so enabling internal monitoring never opens internal notification targets.',
        },
        {
          type: 'table',
          columns: ['Control', 'Default', 'Effect'],
          rows: [
            [
              '`NOTIFICATION_BLOCK_PRIVATE_IPS`',
              '`true`',
              'Refuse loopback, private, link-local (including cloud metadata), CGNAT, multicast, documentation and reserved destinations for every channel. A refusal is a permanent delivery failure, not retried.',
            ],
            [
              '`NOTIFICATION_ALLOWED_CIDRS`',
              'Empty',
              'Ranges that stay reachable while blocking is on — an internal webhook receiver or chat server. Invalid CIDR fails startup.',
            ],
          ],
        },
        {
          type: 'list',
          items: [
            'Set both on the API (channel test), alerter (synchronous delivery) and worker (asynchronous delivery); a service that never receives the policy fails closed.',
            'Redirects are never followed, so an allowed host cannot bounce a request to a blocked one.',
            'Transport errors are logged without the request URL, because webhook paths and Telegram bot tokens are credentials. Gotify and Matrix tokens travel in request headers, never in the URL.',
            'With an `HTTPS_PROXY` configured, the policy vets the proxy address (allow-list it) and destination control becomes the proxy\'s job.',
          ],
        },
        {
          type: 'paragraph',
          text:
            'Status page browser notifications add a third egress path with its own guard. The subscribe endpoint is the only unauthenticated write in the status-page service, and the endpoint it stores is a URL the platform later POSTs to, unattended, from inside the cluster — so an unguarded version would let any visitor turn the subscription table into a stored-SSRF and outbound spam primitive.',
        },
        {
          type: 'list',
          items: [
            'Stored endpoints must be `https` and must match `STATUS_PAGE_PUSH_ENDPOINT_ALLOWLIST` (default: the Google, Mozilla, Microsoft, and Apple push services). Matching is on a dot boundary, so `fcm.googleapis.com.attacker.example` does not match `fcm.googleapis.com`.',
            'Setting the allowlist to `*` disables the check for a self-hosted push service and re-opens this surface; restrict egress by network policy if you do.',
            'Request bodies are capped, subscriptions are rate limited per client IP, and each page has a subscription cap so the table cannot grow without bound.',
            'The rate-limit key is the client address resolved through the [trusted proxy list](#audit-proxy-trust) (`TRUSTED_PROXY_CIDRS`), so a visitor cannot rotate `X-Forwarded-For` to get a fresh bucket. The older `STATUS_PAGE_TRUSTED_PROXY=true` still works while `TRUSTED_PROXY_CIDRS` is unset, but it trusts every peer and logs a deprecation warning.',
            'Subscriptions store only the opaque browser endpoint and its public keys — no visitor identity, no email address, and no page-visit history.',
          ],
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
            'Use `tls://` or `wss://` for every public broker endpoint. Deploy generation rejects plaintext schemes and URLs that already contain userinfo. The websocket listener declares no authorization of its own, so websocket clients go through the same callout as TCP clients (an anonymous or wrong-credential websocket client is refused).',
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
          title: 'The development Compose NATS is not a private-location broker',
          text:
            'The root `docker-compose.yml` broker listens without authentication or TLS. The [release Compose stack](/docs/deployment/#release-compose) turns the auth callout on and publishes only its websocket listener, on loopback, for a TLS proxy to front. The embedded Helm broker is ClusterIP-only until you enable `nats.websocket.ingress` (which the chart refuses without `nats.auth.enabled`) or add another secure exposure. Setting `PUBLIC_NATS_URL` alone does not create that exposure.',
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
              term: 'Local AI token',
              description:
                '`LOCAL_AI_TOKEN` is installation-owned and shared only by the API, platform worker and CPU service, which compares it in constant time. Workspaces cannot set an inference destination or credential. Plain Compose falls back to a development token that is public in the repository, acceptable only because the port is bound to loopback. When Helm manages it and `secrets.localAiToken` is empty, the token is derived from `secrets.adminJwtSecret`, so it is exactly as secret as that value; set it explicitly or from an existing Secret for production.',
            },
            {
              term: 'AI training labels',
              description:
                'A label is copied server-side from the decision snapshot the worker stored when the analysis or alert triage completed (dependency pairs: evidence recomputed at labelling time); client-supplied evidence is rejected. It holds the evidence counters and booleans, catalogue keys, opaque decision IDs, the monitor and incident IDs the UI names decisions with, and the labelling user ID. It never holds names or free text. Labels are kept until the workspace is deleted: `data_retention_days` does not prune them, and they survive monitor purges and incident deletion.',
            },
            {
              term: 'Suspected-cause follow-ups',
              description:
                'When a workspace opts in, the alerter sends the [alert triage](/docs/alerting/#ai-triage) result to the notification providers already paged for the alert: the catalogue key, title and suggested first checks, the confidence level and the triage ID, alongside the usual alert fields (webhooks receive them as `alert.suspected_cause`). No evidence, model output text or summary leaves the installation this way, and PagerDuty and Opsgenie never receive it. The alerter only reads the catalogue; it never calls local AI.',
            },
            {
              term: 'Label export',
              description:
                'Export rows carry evidence, catalogue answers and provenance, and never monitor, incident, analysis, decision, user or tenant identifiers or names. Row and group IDs are HMACs keyed by a per-workspace random salt that is never exported, so they stay stable across exports without revealing the subject. Treat the files as workspace operational data.',
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
            'Review alert and RCA payloads before sending them to third-party notification providers; error bodies, URLs, headers, and incident context can be sensitive.',
            'Keep the local AI service reachable only from the API and platform worker. The chart’s NetworkPolicy does this only on a CNI that enforces NetworkPolicy; elsewhere the token is the only barrier. Share label export files only with the people who retrain the model.',
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
          type: 'paragraph',
          text:
            'The IP in audit records and admin sessions, and the status page push rate-limit key, come from one resolver. Forwarding headers are client-settable, so they count only when the TCP peer is listed in `TRUSTED_PROXY_CIDRS` (comma-separated CIDRs or bare IPs, read by the API and status-page services).',
        },
        {
          type: 'list',
          items: [
            'Unset (the default), no proxy is trusted: the recorded address is the TCP peer, and `X-Forwarded-For` / `X-Real-IP` are ignored. Behind a proxy this records the proxy, not the visitor, so set the list in any deployment that has one.',
            'From a trusted peer, `X-Forwarded-For` is read right to left. Each proxy appends the address it received the connection from, so the first entry outside the list is the client, and anything a client prepended to the left of it is ignored.',
            '`X-Real-IP` is used only when a trusted peer sent no `X-Forwarded-For`. If every hop is trusted, the leftmost entry is used.',
            'Your proxies must append to `X-Forwarded-For` (nginx `$proxy_add_x_forwarded_for`, the ingress-nginx and Traefik defaults) rather than pass a client value through unchanged.',
            'List only proxies you operate. A range that also contains clients, such as a whole VPC that users connect from, lets those clients forge their address. In Kubernetes this is usually the pod CIDR of the ingress controller; with Helm set `trustedProxyCIDRs`.',
            'The operator UI’s own `/api` proxy cannot append to `X-Forwarded-For`, so it drops `X-Forwarded-For` and `X-Real-IP` before calling the API. UI traffic that reaches the API through it is recorded with the frontend’s address. If a proxy you run appends the header in front of the UI, set `API_PROXY_TRUST_FORWARDED_FOR=true` on the frontend and list both that proxy and the frontend in `TRUSTED_PROXY_CIDRS`. With the bundled Helm ingress, `/api` goes straight to the API, so this does not apply.',
          ],
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
            'Compose uses development database credentials and unauthenticated NATS. Helm defaults to unpinned image tags (the chart’s `appVersion`), simple single-instance embedded stateful services, writable root filesystems, and no network policies. Harden or replace these defaults before production.',
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
            'Confirm notification egress cannot reach internal metadata/control-plane addresses.',
            'Test location disable/revocation and ensure one location cannot consume another location’s jobs.',
            'Validate audit capture, drop warnings, retention, export restrictions, and external log durability.',
            'Check the [security headers](#edge-controls) on API, UI, and status-page responses through the real ingress, including HSTS on HTTPS and `429` with `Retry-After` from the API limiter.',
            'Restore a database backup with the complete keyring and verify encrypted channels, monitors, and locations.',
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
              'Reset the password (which signs the user out everywhere) or use **Sign out everywhere** on the user page, inspect audit activity, and consider JWT rotation if token-signing material was exposed.',
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
              'Discovery collector token/manifest',
              'Rotate the token (or delete the integration), apply the new manifest so the cluster Secret holds the new token, and review the integration\'s inventory, and any suggestion approved since the exposure, for resources that do not exist in the cluster: a token holder can submit invented Services and Ingresses within the configured namespaces. A token holder can only replace that integration\'s inventory (at most one processed snapshot a minute, refused ones included, and 10,000 stored resources); it cannot create or change monitors.',
            ],
            [
              'NATS platform credential or issuer seed',
              'Rotate broker and workload credentials; for issuer compromise, coordinate a new account key across NATS and API and revoke old trust.',
            ],
            [
              'OIDC/SMTP/local AI/webhook secret',
              'Rotate at the provider, update all consumers, test the integration, and review provider-side activity/logs.',
            ],
          ],
        },
      ],
    },
  ],
};
