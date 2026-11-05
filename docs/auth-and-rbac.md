# Authentication, RBAC, Audit & SSO

How identity and authorization work as of the enterprise-auth rework
(migrations 000073–000076).

## Identity model

Three credential types, resolved by `api/internal/middleware/auth.go`:

- **Admin users** (`admin_users`) — humans. JWT access cookie (15 min) +
  rotating opaque refresh session, both bound to one sign-in (session
  family, see [Sessions](#sessions)); password login sits behind the
  [lockout](#login-lockout) and, when enrolled, a TOTP
  [second factor](#two-factor). Two platform roles:
  - `superadmin` — full platform access: any tenant (via `X-Tenant-ID`),
    user management, tenant list, workspace create/rename/delete. All
    pre-rework admins were backfilled to superadmin.
  - `member` — access only through `tenant_memberships` rows. A
    tenant-scoped request requires a membership in that tenant (403
    otherwise); `GET /v1/tenants` returns only the member's tenants.
- **API keys** (`api_keys`) — machines. Pinned to one tenant, carry a
  `scope` (`read` | `write`, legacy keys backfilled to `write`) and an
  optional `expires_at`. Expired keys fail validation in SQL.
- **MCP OAuth access tokens** (`pbm_at_…`, `mcp_oauth_*` tables, migration
  000096) — an AI agent acting as the user who consented
  (`api/internal/services/mcpoauth`). Authenticated through the same admin
  path as a cookie session (roles from the DB, disabled users rejected),
  with the grant's default tenant when no `X-Tenant-ID` is sent and the
  grant's `scope` added on top. `ctxpkg.CanWrite` checks a scope **before**
  any role, so a read grant narrows even a superadmin. Accepted only on
  `/api/v1/mcp` and on requests it dispatches (context `via=mcp`, which no
  client can set) — the token's audience is the MCP resource, not the REST
  API. See [MCP](#mcp).

Outside this middleware, **discovery collector tokens** authenticate only the
in-cluster Kubernetes collector, on `/api/v1/discovery-collectors/{id}/config`
and `/snapshot` for their own integration (`handlers/discovery`,
`collectorToken`). One per integration, 32 random bytes, stored as a SHA-256
hash in `discovery_integrations.token_hash`, shown only by the create and
`rotate-token` responses (both write-gated). They carry no tenant role or
scope and are never accepted on tenant routes, so they cannot read coverage or
create monitors. On exposure: rotate (or delete the integration) and re-apply
the manifest Secret.

Tenant roles (fixed, on `tenant_memberships.role`):

| Role   | Can                                                      |
|--------|----------------------------------------------------------|
| admin  | everything in the tenant incl. API keys, settings, audit  |
| editor | create/modify monitors, alerts, incidents, status pages…  |
| viewer | read-only (plus compute-only POSTs like `/monitors/test`) |

Roles are resolved from the DB on every request — never stored in JWT
claims — so downgrades and disables apply immediately (disabled users are
rejected even with a live access token). The access JWT carries only the
admin ID and the session family (`sid`).

## Sessions

Migration 000104. `admin_session_families` holds one row per sign-in
(password or OIDC); `admin_sessions.family_id` ties every refresh token to
its sign-in, and `RotateSession` keeps the family. The access JWT names the
family in its `sid` claim (`shared/auth.CreateAdminSessionToken`).

- **`admin_session_families.revoked_at` is the one authority** for whether a
  sign-in is alive. The refresh lookup (`GetSessionByTokenHash`) joins it,
  so the 60-second reuse grace of a rotated token never outlives a
  revocation, and `AuthMiddleware` checks it on every cookie request
  (`middleware/session_family.go`, `adminauth.SessionFamilyActive`), as do
  `/auth/me` and `/auth/logout`. Revoking the one row therefore ends the
  refresh chain and every access JWT of the sign-in at once, with no race
  against a concurrent rotation.
- Revocation paths: logout (the token's family), `/account/sessions/*`
  (self: one, others, all), `DELETE /users/{id}/sessions` (superadmin, MCP
  `revoke_user_sessions`), a password reset or invite redeemed (all), a
  self-service password change (all but the current), and a superadmin setting a user's password
  (`adminauth.RevokeUserSessions`, which also records
  `password_changed_at` and clears the lockout).
- An access JWT without `sid` (minted before 000104) is accepted until it
  expires; it cannot be revoked early.
- `/account/*` takes a signed-in browser session only (no API key, no MCP
  token), sits outside the write gate (a viewer can end their own sessions)
  and outside generic audit; it records `auth.session_revoke` /
  `auth.sessions_revoke`. Routes: `api/internal/api/routes_identity.go`.
- API keys and MCP OAuth grants are separate credentials; signing out
  never touches them.

## Account lifecycle

Invites, password reset and email change (`adminauth/lifecycle.go`,
`adminauth/tokens.go`, mail in `services/accountmail`, routes in
`api/internal/api/routes_identity.go`).

- **Local account** = `password_hash` set and `external_subject` NULL.
  Reset, self password change, self email change and TOTP are local-only:
  for an IdP-linked account the IdP owns the credentials and the email is
  the link key, so changing it here is refused (409 `not_local_account`).
- **One-time tokens** (`admin_user_tokens`, migration 000117): 256-bit,
  stored as SHA-256, consumed by one `UPDATE … WHERE used_at IS NULL`
  (single use under concurrency), and issuing another of the same purpose
  supersedes the user's open ones.

  | Purpose | TTL | Issued by | Redeemed by |
  |---|---|---|---|
  | `invite` | 7 days | `POST /users/{id}/invite` (superadmin) | `POST /auth/invite/accept` (sets the password; `/auth/invite/inspect` reads it) |
  | `password_reset` | 1 hour | `POST /auth/password/forgot`, `POST /users/{id}/password-reset` | `POST /auth/password/reset` |
  | `email_change` | 24 hours | `POST /account/email` (current password) | `POST /auth/email/confirm` |

  Redeeming a password token sets the password, records
  `password_changed_at`, clears the lockout, supersedes the other open
  invite/reset tokens and revokes every session family. A self-service
  password change (`PATCH /account/password`) keeps only the current
  session. TOTP survives a reset.
- **Links** are `APP_BASE_URL` + `/accept-invite`, `/reset-password` or
  `/confirm-email` with the token in the **fragment** (`#token=…`): never
  sent to a server, so never in proxy/access logs. Tokens reach the API in
  request bodies; no lifecycle route has a token in its path.
- **Anti-enumeration**: `/auth/password/forgot` answers 202 immediately in
  every case and does the lookup and send in a bounded background pool, so
  neither status nor timing reveals an account; it matches a username
  exactly or an email case-insensitively, mails at most once a minute per
  account, and is charged per address by the anonymous rate limiter. All
  public link routes refuse with one 400 `invalid_token`.
- **Without SMTP or `APP_BASE_URL`**: `GET /auth/capabilities` reports
  `password_reset: false` (the login page hides "Forgot password"),
  `/account/email` answers 503, and the superadmin invite/reset routes
  return the link (`path`, plus `url` with `APP_BASE_URL`) for manual
  delivery. With mail on, a delivered password link is never returned.
- Invite method: `password` (set-password link) or `sso` (a "sign in with
  SSO" notice, no token). Default `sso` for a linked account, or one with
  no password while OIDC is enabled.
- Audit: `auth.invite`, `auth.invite_accept`,
  `auth.password_reset_request` (only when a link was issued),
  `auth.password_reset_issue`, `auth.password_reset`,
  `auth.password_change`, `auth.email_change_request`, `auth.email_change`.

## Two-factor

TOTP (`adminauth/totp.go`, stdlib RFC 6238: SHA-1, 6 digits, 30 s, ±1
step) for local accounts; migration 000118 adds `totp_secret_enc`
(PROBARA_SECRETS_KEY encryptor), `totp_enabled_at` (NULL = pending
enrollment), `totp_last_step` and `admin_user_recovery_codes` (10
SHA-256-hashed single-use codes).

- **Login is two steps.** `Authenticate` returns `*SecondFactorRequired`
  (an error, so a caller unaware of 2FA fails closed) and the handler
  answers `{mfa_required, mfa_token}` with no cookie. The token is
  `pmfa1~payload~mac` (HMAC key derived from `ADMIN_JWT_SECRET`, 5 min,
  never a JWT) and binds a fingerprint of the password hash and sealed
  secret. `POST /auth/login/mfa` completes the sign-in.
- **Replay**: a code is accepted only for a step after `totp_last_step`,
  advanced by a conditional `UPDATE`, so one code works once fleet-wide.
- **Lockout**: the MFA step reserves an attempt like a password does;
  a correct password at step one only releases its own reservation
  (`releaseReservationQuery`), never resets the streak, so wrong codes
  accumulate to the same lock. Self-service routes that check password +
  code reserve once for both (`VerifyCredentials`).
- Self-service: `/account/2fa` (status), `/setup` (password → secret and
  `otpauth://` URI; the UI draws the QR in the browser, `web/lib/qr.ts`),
  `/enable` (code → recovery codes, shown once), `/disable` and
  `/recovery-codes` (password + code).
- Recovery: superadmin `POST /users/{id}/reset-2fa` (MCP `reset_user_2fa`)
  or `go run ./cmd/admin -reset-2fa <username>`.
- Audit: `auth.login` (`mfa` detail on success, `factor: second` on
  failure), `auth.mfa_enable`, `auth.mfa_disable`,
  `auth.mfa_recovery_codes`, `auth.mfa_reset`.

## Login lockout

Migration 000103 (`admin_users.failed_login_count`, `last_failed_login_at`,
`locked_until`, `password_changed_at`) and 000106 (`login_failure_streaks`);
policy constants in `api/internal/services/adminauth/lockout.go`. All state
is in Postgres, so it holds across API replicas and restarts.

- `Authenticate` **reserves** the attempt (one `UPDATE … WHERE not locked
  RETURNING`) before comparing the password, so concurrent guesses cannot
  overshoot; a correct password clears the reservation. Every 10th failure
  in a streak locks for 15 min × 2^(k−1), capped at 4 h; a streak is
  forgotten after 24 h without failures. A locked account is refused without
  a password comparison.
- Anti-enumeration: wrong and unknown credentials share one 401; every
  lockout shares one 429 + `Retry-After`. Usernames with no password account
  (unknown, SSO-only, disabled) keep their streak in `login_failure_streaks`,
  keyed by SHA-256 of the exact (case-sensitive) username and counted by the
  same SQL (`lockoutAssignments`), plus a dummy bcrypt compare, so they lock
  on the same attempt as a real account on every replica. Streaks idle past
  24 h are pruned opportunistically on failed logins (`PruneGhostStreaks`).
- **No per-address stage.** With `TRUSTED_PROXY_CIDRS` unset every user
  behind the ingress or the UI proxy (or one NAT) shares a client IP, so an
  IP block would let anyone deny sign-in to all of them. Request-volume
  limits per client belong to the general API rate limiter.
- Unlock: superadmin `POST /users/{id}/unlock` (MCP `unlock_user`), the
  `cmd/admin` break-glass, a superadmin setting the password, or a
  password reset/invite link.
- Second-factor failures and wrong current passwords on `/account/*`
  count on the same streak (see [Two-factor](#two-factor)).
- Audit: `auth.login` (failure; `denied` with `reason` `locked`),
  `auth.lockout` (the failure that locked), `auth.unlock`.
- OIDC login is unaffected.

## Enforcement layers

Middleware chain on the authenticated subrouter (`server.go`):

```
AuthMiddleware → AuditMutations → RequireWrite → [route guards] → handler
```

- `RequireWrite` (`middleware/readonly.go`): non-GET requests need write
  capability (superadmin, tenant role admin/editor, or write-scope key).
  A small allowlist exempts compute-only POSTs (`/monitors/test`,
  `import/preview`, `dependency-suggestions`, `/mesh/probe`,
  `/ai-settings/test`). **Add new test/preview endpoints to that list** or
  viewers will get 403s.
- Credential-bearing reads have explicit write-capability checks:
  `/locations/{id}/deploy` and `/monitors/{id}/push/info` require a
  superadmin, tenant admin/editor, or write-scope API key. Monitor get/list
  and group-member responses omit push tokens for read-only identities;
  monitor config secrets remain masked for every identity.
- `/monitors/test` remains available to viewers for caller-supplied configs,
  but using saved secrets (`monitor_id` with a secret-bearing monitor type:
  any type in `shared/secrets` `MonitorSecretFields`/`MonitorSecretMapFields`,
  which includes http, grpc and the synthetic types) requires write
  capability. The saved and requested monitor types must
  match. Test requests cannot supply ciphertext envelopes directly: use
  write-only placeholders for existing credentials.
- `RequireTenantAdmin`: API key create/revoke, tenant settings mutation,
  audit log. Superadmin or tenant-role admin; API keys never pass. Its
  predicate is `ctxpkg.IsTenantAdmin`, which also gates SIP test-call
  monitors (`method: invite`, real calls) where no route guard can: create
  and every update (pause included) in the monitors service, so import
  inherits it, and `/monitors/{id}/run` and `/monitors/test` in their
  handlers (`monitorservice.RequireCallPermission`). Editors and API keys of
  any scope get 403.
- `RequireSuperadmin`: `/users/*` (including `/users/{id}/unlock` and
  `/users/{id}/sessions`), `POST /tenants`, `PATCH`/`DELETE /tenants/{id}`.

Audit runs *outside* the write gate so denied attempts are recorded with
`outcome=denied`.

## Deleted workspaces

Workspace delete (`DELETE /tenants/{id}`, `confirm_name` must repeat the
name) is a soft delete (migration 000105, `shared/tenancy`). One transaction
stamps `tenants.deleted_at`, tombstones the workspace's monitors and
locations, revokes its API keys and MCP grants, deletes its memberships and
SSO group mappings, and invalidates its discovery collector tokens. From then
on `AuthMiddleware` answers 403 to any session request scoped to it,
superadmins included (`rejectDeletedTenant`, `tenancy.IsDeleted`), every
tenant list omits it, and the status-page service stops serving its pages.
API key validation and the discovery collector token lookups add
`tenancy.LiveTenantSQL` to their own query, so a key or integration that a
request already past auth writes just after the delete commits is refused at
read time too; the purger's next run also revokes such late credentials and
tombstones late monitors (`deletedTenantCutoffs`).
The scheduler purger removes the data in bounded batches and then the row
(see `docs/architecture.md`); the API never deletes a tenant row. The
default workspace (`tenancy.DefaultTenantID`), the
`OIDC_JIT_DEFAULT_TENANT_ID` workspace and the last live workspace cannot be
deleted. Names are unique among live workspaces only, so a deleted name is
free at once. Create, rename and delete record `tenant.create`,
`tenant.rename` and `tenant.delete` as platform-level audit events with a
name snapshot.

## MCP

`/api/v1/mcp` (package `api/internal/mcp`) serves every catalog tool by
rebuilding the tool's REST request and dispatching it **through the root
router in process**, with the caller's bearer credential and a fresh
context. Every layer above therefore applies unchanged to agents; nothing in
the MCP package decides access. Rules that keep it so:

- Bearer only: `Protect` strips the `Cookie` header, so a browser page can
  never drive MCP with an operator's session.
- `tools/list` is filtered per caller (read-only → no write tools, API key →
  no tenant-admin/superadmin tools) from `IsReadOnlyRequest` and the route
  gates `chi.Walk` reports — UX only; the inner dispatch enforces.
- Every `/api/v1` route is a tool or listed in `notExposed` with a reason;
  `TestMCPCatalogMatchesRouter` (and startup) fail otherwise.
- Writes are audited under the real route with the user/key as actor and
  `details.via = "mcp"`.
- OAuth consent (`/oauth/authorize/decision`) and connection management
  (`/oauth/grants`) take a signed-in **user session only** (no API key, no
  MCP token), sit outside the write gate (a viewer may connect a read-only
  agent) and record `mcp_connection.authorize` / `.revoke` themselves.
- Agent install scripts embed the caller's bearer only when it is an API key
  (`embeddableAPIKey`), never an MCP token.

## Audit log

`audit_log` table; captured two ways: the `AuditMutations` middleware
derives `resource.verb` actions for every mutation, and explicit events
cover auth lifecycle (`auth.login`/`auth.logout`/`auth.oidc_login` incl.
failures, `auth.lockout`/`auth.unlock`, `auth.session_revoke`/
`auth.sessions_revoke`, the [account lifecycle](#account-lifecycle) and
[two-factor](#two-factor) events) and user/key management (those subtrees are skipped
by the generic layer). Workspace management records its own success events
(`tenant.create`/`.rename`/`.delete`, platform-level); the generic layer
still records its refusals and failures. Writes go through an async batching recorder — never
blocks or fails a request; drops rows quietly during the pre-migration
deploy window.

`GET /v1/audit-log` (+`/actions`) is tenant-admin gated; superadmins also
see platform-level events (`tenant_id IS NULL`). UI at `/audit`.

Retention: `AUDIT_RETENTION_DAYS` (default 365, `0` = keep forever),
enforced by an hourly pruner in the API process. Deliberately separate
from `tenants.data_retention_days` (telemetry vs compliance).

## OIDC SSO

Platform-level (one IdP per install), OIDC only. Enable with:

```
OIDC_ENABLED=true
OIDC_ISSUER_URL=https://idp.example.com
OIDC_CLIENT_ID=…
OIDC_CLIENT_SECRET=…
# optional:
OIDC_REDIRECT_URL=…        # default PUBLIC_BASE_URL + /api/v1/auth/oidc/callback
OIDC_SCOPES="openid profile email"   # add "groups" for group→role mappings
OIDC_GROUPS_CLAIM=groups   # ID-token claim read by group→role mappings
OIDC_PROVIDER_LABEL="Okta" # login button text
OIDC_JIT_PROVISION=true
OIDC_JIT_DEFAULT_ROLE=viewer
OIDC_JIT_DEFAULT_TENANT_ID=00000000-0000-0000-0000-000000000001
```

Helm: `auth.oidc.*` + `secrets.oidcClientSecret`. The IdP must allow the
redirect URL on the **web origin** (requests flow through the Next.js
proxy).

Flow: authorization code + PKCE; state/nonce/verifier ride in an
HMAC-signed stateless cookie (keyed with `ADMIN_JWT_SECRET`). Provider
discovery is lazy — the API boots while the IdP is down.

Identity mapping on callback:
1. `(issuer, subject)` match → login.
2. Verified-email match on a local user without an external identity →
   the IdP identity is linked to that account (this is how **invited**
   SSO-only users work: create the user with an email and no password).
3. JIT provisioning (if enabled) → new `member` with the default
   membership. Unverified or already-bound emails are refused
   (`sso_error=not_provisioned`) rather than creating a duplicate.

**Bootstrap × SSO:** on an install with zero active admins, the first
OIDC login JIT-provisions a *superadmin* (mirroring the password
bootstrap guard). Whichever path runs first claims superadmin. The
DB-side `cmd/admin` tool (`ADMIN_USERNAME`/`ADMIN_PASSWORD`) always upserts
a superadmin — no memberships, same as the password bootstrap — and on an
existing username resets the password, re-enables and promotes it (the
break-glass path).

### Group→role mappings

Superadmins map IdP groups to roles in `oidc_group_mappings` (Settings →
OIDC group mappings, or `/api/v1/oidc-group-mappings`). A row targets a
tenant with a tenant role, or the platform (`tenant_id NULL`, role
`superadmin`). Groups come from the ID-token claim named by
`OIDC_GROUPS_CLAIM` (no userinfo fallback — the IdP must embed the claim,
which usually means requesting the `groups` scope via `OIDC_SCOPES`).

Semantics (`api/internal/services/oidcauth/groupsync.go`):

- **Zero rows = feature off** — JIT defaults apply, roles stay manual.
  Deleting all rows is the runtime kill switch.
- Any rows → the IdP is the source of truth for SSO users: platform role
  and the *full* membership set are re-derived on **every login**
  (manual edits to SSO users last until their next sign-in). Password
  accounts are never touched.
- Highest role wins per tenant (`admin > editor > viewer`); group names
  match case-sensitively on the **raw claim value** (Azure AD sends group
  object IDs there, not names). Mappings carry an optional cosmetic
  `label` so GUID rows stay readable in the settings table.
- No matched groups → member with zero memberships (deliberately no
  JIT-default fallback — that would re-grant revoked access).
- The sync never demotes the **last active superadmin** (skipped +
  flagged `skipped_last_superadmin_demotion` in the audit event); local
  password login is unaffected, so mapping misconfig can't hard-lock the
  install.
- Sync runs in one transaction (user row locked `FOR UPDATE`); a sync
  failure aborts the login (`sso_error=internal`) rather than admitting
  stale roles. Applied changes emit an `auth.oidc_role_sync` audit event
  with the membership diff, `matched_groups`, and `received_groups`
  (everything the token presented — check here on name mismatches).
- Every successful SSO login upserts the token's groups into
  `oidc_seen_groups` (best-effort, never blocks login, works with zero
  mappings). The mapping editor uses it for autocomplete and
  click-to-prefill chips of unmapped groups — OIDC has no
  group-enumeration API, so this is the discovery mechanism.
- JIT provisioning with mappings present uses the mapped roles instead
  of `OIDC_JIT_DEFAULT_ROLE`/`_TENANT_ID` (bootstrap parity still wins).

Local dev IdP: `docker compose --profile sso up dex`
(`admin@example.com` / `password`; config in `infra/dex/config.yaml`).
staticPasswords users carry no groups — use the "Mock (groups test)"
connector on dex's login screen (fixed identity with groups
`["authors"]`) and `OIDC_SCOPES="openid profile email groups"` to
exercise mappings.

## Deploy ordering

The helm migrations job is post-upgrade, so new binaries briefly run on
the old schema. All auth queries feature-detect (SQLSTATE 42P01/42703 →
legacy behavior: everyone superadmin, keys full-access, no lockout, no
session families, no 2FA; account links answer 503) and the audit writer drops rows — the API never
crash-loops pre-migration. In the other direction, an old binary inserting
`admin_sessions` without `family_id` after 000104 gets a family from the
`admin_sessions_default_family` trigger. For
zero-surprise rollouts run the migration job manually first (see
`docs/correctness-notes.md` "Deploy ordering").

## Verification

`scripts/verify-auth.sh` runs the full matrix against a local stack
(30 checks): role × method enforcement, membership filtering, scoped and
expiring keys, audit capture, and (with `VERIFY_SSO=1` + the dex profile)
the full OIDC login flow.
