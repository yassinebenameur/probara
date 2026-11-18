# Authentication, RBAC, Audit & SSO

How identity and authorization work as of the enterprise-auth rework
(migrations 000073–000076).

## Identity model

Three credential types, resolved by `api/internal/middleware/auth.go`:

- **Admin users** (`admin_users`) — humans. JWT access cookie (token
  15 min; the cookie itself lasts the refresh lifetime so the expired token
  rides along with a refresh) + rotating opaque refresh token, grouped into a session (see
  [Sign-in lockout and sessions](#sign-in-lockout-and-sessions)). Two
  platform roles:
  - `superadmin` — full platform access: any tenant (via `X-Tenant-ID`),
    user management, tenant list. All pre-rework admins were backfilled to
    superadmin.
  - `member` — access only through `tenant_memberships` rows. A
    tenant-scoped request requires a membership in that tenant (otherwise
    `404 workspace_not_found`, the same answer as for a deleted or unknown
    workspace, so a member learns nothing about workspaces they cannot
    open); `GET /v1/tenants` returns only the member's tenants.
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
rejected even with a live access token).

## Sign-in lockout and sessions

Migrations 000117 (lockout) and 000118 (session families).

**Lockout** (`api/internal/services/adminauth/lockout.go`, password sign-in
only): 10 consecutive failures lock the account for 15 minutes, fixed; a
failure more than 24 h after the previous one (`last_failed_login_at`)
starts a new streak. The streak (`failed_login_count`) and the lock
(`locked_until`) live on `admin_users` and are timed by the database clock
(`NOW()`; tests move it through `Service.clockOffset` only), so replicas
agree and restarts keep them. `Authenticate`
reserves the attempt before comparing the password (one atomic `UPDATE`
that only matches an unlocked password account), so parallel guesses cannot
overshoot: exactly 10 are compared, the 10th locks and restarts the streak.
A correct password clears it; a locked row matches nothing, so attempts
during a lock neither count nor extend it, and a locked account's correct
password is refused. Unknown, SSO-only and disabled usernames are refused
without any write. Every refusal gets the same `401` and body and costs one
bcrypt comparison (a dummy hash when there is none to compare), so the lock
never confirms an account exists; the audit log tells them apart
(`auth.login` failure with `reason: locked`, `auth.lockout`).
Failed second factors count toward the same lock through
`RecordFailedAttempt`, and on a two-factor account a correct password only
takes back its own count; the streak clears when the second step passes
(see [Account links and two-factor](#account-links-and-two-factor)). Superadmin unlock: `POST /users/{id}/unlock` (`user.unlock`, MCP
`unlock_user`); `cmd/admin` also clears a lock. What bounds "lock every
account": the lock is time-boxed and never extended, it ends no sessions,
superadmins can unlock, locks are audited, and the API rate limiter charges
failed sign-ins to the address's failure budget (attributable addresses
only).

**Sessions** (`adminauth/sessions.go`): one `admin_session_families` row
per sign-in (password or OIDC); every refresh token (`admin_sessions`, one
row per rotation) carries its `family_id`, and the access JWT carries the
family id as `sid`. The family's `revoked_at` is the single authority:

- `AuthMiddleware` checks it for every cookie-authenticated request
  (`middleware/session.go` → `adminauth.SessionActive`, one primary-key
  lookup, no cache, so a revocation is immediate on every replica);
  `/auth/me` checks it too. Tokens without a `sid` (minted before 000118)
  are honoured until they expire.
- `RefreshSession` rotates inside the family under the family row lock. A
  token rotated less than 60 s ago (concurrent tabs) earns an access token
  only -- no new refresh token, so the family never forks into two chains;
  presented later it is reuse: the family is revoked (`refresh_reuse`, audit
  `auth.session_reuse`).
- Long-lived responses re-check: the auth middleware leaves
  `middleware.StillAuthorized` / `CredentialExpiry` in the context
  (`credential_recheck.go`: session, user enabled, tenant membership, MCP
  token, API key and its workspace); the alert SSE stream calls it every heartbeat and ends at
  the access token's expiry with a `reauth` event the web client reconnects
  on.
- `SessionPruner` (API process, hourly, batches of 1000) deletes refresh
  tokens expired longer than the refresh lifetime + 1 day ago and families
  revoked or expired that long ago; reuse detection only needs rotated
  tokens until they expire.
- Last use, address (`shared/clientip`) and user agent move on sign-in and
  refresh only, never per request.
- The rate limiter's verified-credential cache never authenticates: it only
  lifts the address pre-check, and the refresh handler / middleware still
  check the family, so a revoked session's cookie is refused on first use
  and then forgotten by that replica. A refresh cookie the replica issued is
  kept for the access-token lifetime plus 10 minutes (the browser presents it
  only once its access token has expired). A refresh that also carries an
  access cookie the API signed (live, or expired within the refresh
  lifetime) skips the pre-check on any replica, charged to that user's
  refresh bucket, and its `401` is never turned into a `429`; the cookies'
  common session is the handler's lookup to settle. The web client treats a
  `429` from refresh as "try again later": it retries per `Retry-After` a
  few times, then fails that call as `rate_limited` and keeps the session.
- A request that authenticates and is then refused by `AuthMiddleware` (a
  malformed tenant identifier, `404 workspace_not_found`) is charged to the
  credential's own bucket (`ratelimit_refusal.go`), so looping random
  `X-Tenant-ID` values is rate limited like any other request.

Routes: `/account/sessions` (list, `DELETE /{id}`, `POST /revoke-others`) —
cookie session only (a `sid` is required; API keys and MCP tokens get 403),
outside the write gate, audited as `auth.session_revoke` /
`auth.sessions_revoke_others`; `/users/{id}/sessions` (superadmin, MCP
tools `list_user_sessions`, `revoke_user_session`, `revoke_user_sessions`,
audited `user.session_revoke` / `user.sessions_revoke`).

What ends sessions: sign-out (that one); a user's own revoke (one / all
others); superadmin revoke (one / all; on your own account all others);
superadmin password set (all; your own password: all others —
`adminusers.UpdateUser` keeps the caller's `sid`); `cmd/admin` (all); user
deletion (cascade); refresh reuse (that one); a completed password reset or
invite (all); the user's own password change, turning two-factor off, or a
recovery code used (all others). Every password set (reset, invite, own
change, superadmin set, `cmd/admin`) also revokes the user's MCP OAuth
grants and unexchanged codes (`adminauth.RevokeMCPAccess`); workspace API
keys are not user credentials and stay. Not: lockout or unlock (a
guesser must not be able to sign people out), role and membership changes
(roles are re-read per request anyway). Revoking an SSO session does not
sign out of the IdP.

Migration 000118 turns every live refresh token into its own family (id =
token row id), so signed-in users stay signed in; rotated/expired rows keep
`family_id NULL` and are never accepted. A trigger gives a family to rows an
older binary inserts during a rolling deploy. An old binary also honours a
revoked family's token, so revocations made mid-rollout may not hold:
finish the rollout first. Drop the trigger in a later release.

## Account links and two-factor

Migrations 000124 (account tokens) and 000125 (TOTP, recovery codes, the
session's `second_factor`). Local (password, no `external_subject`)
accounts only: SSO accounts get no reset, email change, password or second
factor here.

**Tokens** (`adminauth/tokens.go`, `admin_account_tokens`): invite (7 days),
password reset (1 h), email change (24 h) and the second sign-in step's
challenge (5 min) are 256 random bits stored as SHA-256, consumed by one
conditional `UPDATE` (single use), looked up by purpose (an invite never
resets a password). Server state rather than `shared/actionlink` signed
tokens, because single use and revocation need it anyway, and links must
work without `PROBARA_SECRETS_KEY`. Issuing a link revokes the user's open
links of the same purpose (challenges are not superseded: each is one
sign-in). `RetireAccountTokens` revokes every open link and challenge; every
credential reset calls it: completed reset or invite, own password change,
superadmin password set (`adminusers` `RevokeSessionsByAdminID`), `cmd/admin`
(both modes); a superadmin two-factor reset retires the email change and
challenges. Link pages carry the token in the URL fragment and send it in
`X-Action-Token` (`handlers/auth/links.go`), never in a path. The session
pruner deletes tokens expired longer than the session retention.

**Flows**: forgot password always answers `202` with one body and does the
lookup and mail in the background (bounded workers), mailing only an
enabled local account with an address, locked included, at most
`PasswordResetMailsPerHour` (3) self-service resets per account per hour.
Completing a reset or invite sets the password, revokes all sessions and
MCP grants, retires every other token, and clears the lockout unless
two-factor is on (an ordinary lock and the second factor survive a reset; a
held lock is lifted). An address change (confirmation or superadmin edit)
retires open invite and reset links (`RetireMailedLinksTx`). Passwords are
12 characters to 72 bytes (`auth.ValidatePassword`; `HashPassword` returns
`ErrPasswordTooLong`, which handlers answer as a validation error). Email change: current password
under the lockout, confirmation to the new address, notice to the old one,
applied on confirmation (uniqueness checked there). Superadmin invite /
reset link: mailed with `SMTP_*` + `APP_BASE_URL` on the API
(`api/internal/services/accountmail`), otherwise returned once in the
response (`link`), never logged or audited. Every account-page check of the
current password reserves an attempt (`VerifyCurrentPassword` /
`VerifyCurrentCredentials`); a right one only takes back its own count.

**Two-factor** (`adminauth/twofactor.go`, `totp.go`): RFC 6238 on the
standard library (SHA-1, 6 digits, 30 s, ±1 step), `totp_last_step` refuses
replays, 10 recovery codes stored hashed and burned on use. The secret is
sealed with the `PROBARA_SECRETS_KEY` encryptor (`ConfigureEncryption` gets
it only when a key is configured; without one enrolment is
`ErrTwoFactorNeedsKey`) and only an envelope is ever opened. Sign-in:
`Authenticate` returns `*SecondFactorRequired` (a challenge, no cookie) for a
correct password, after releasing that attempt's reservation unless it just
locked the account (then the ordinary refusal); `CompleteSecondFactor`
reserves an attempt through `RecordFailedAttempt` before comparing the code,
and under the user row lock checks the code, uses the challenge and clears
the streak. Locked, disabled or wrong code: one `401` body. A lock completed
by a wrong code is a second-factor lockout (`recordSecondFactorLockout`,
audit `auth.second_factor_lockout`, mail notice, shown on the user's page);
the third within 24 h holds the account (`lockout_held`, `locked_until` far
ahead) until a superadmin unlock, `cmd/admin`, or a completed reset. An
internal error in the step (an unreadable secret: `ErrSecretUnreadable`,
logged once) gives the attempt back. A recovery code signs out every other
session. The rate limiter
treats `POST /auth/login/mfa` as a sign-in. Disable needs password and code
and revokes other sessions; superadmin `POST /users/{id}/reset-2fa`;
`cmd/admin -reset-2fa <username>` (tolerates a database without 000124/125).
Notices (enabled, disabled, reset, recovery code used) are mailed in the
background when account mail is configured.

## Enforcement layers

Middleware chain on the authenticated subrouter (`server.go`):

```
AuthMiddleware → AuditMutations → RequireWrite → [route guards] → handler
```

- `RequireWrite` (`middleware/readonly.go`): non-GET requests need write
  capability (superadmin, tenant role admin/editor, or write-scope key).
  A small allowlist exempts compute-only POSTs (`/monitors/test`,
  `import/preview`, `dependency-suggestions`, `/mesh/probe`,
  `/ai-settings/test`, `/monitors/apply/plan`). **Add new test/preview endpoints to that list** or
  viewers will get 403s.
- Credential-bearing reads have explicit write-capability checks:
  `/locations/{id}/deploy` and `/monitors/{id}/push/info` require a
  superadmin, tenant admin/editor, or write-scope API key. Monitor get/list
  and group-member responses omit push tokens for read-only identities;
  monitor config secrets remain masked for every identity.
- `/monitors/test` remains available to viewers for caller-supplied configs,
  but using saved secrets (`monitor_id` with a secret-bearing monitor type,
  which includes http, grpc, synthetic_api and synthetic_browser, whether or
  not that monitor holds a secret) requires write capability. The saved and requested monitor types must
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
- `RequireSuperadmin`: `/users/*`, and workspace management (`POST
  /tenants`, `PATCH`/`DELETE /tenants/{id}`, `GET /tenants/overview`).

### Cross-site request forgery

`SameSite=Lax` does not separate the operator UI from a status page on the
same site (another port of the same host, a sibling subdomain), and a
workspace editor can publish a status-page template whose script runs there.
So `AuthMiddleware` refuses a request its **session cookie** authenticates
with an unsafe method (not GET/HEAD/OPTIONS) unless it comes from a trusted
origin (`middleware/origin.go`, `OriginPolicy`), with `403
cross_origin_refused`, before any lookup; sign-in and its second step,
refresh and sign-out (anonymous, but they set or clear the cookie) and the
mailed-link POSTs (forgot, reset, invite accept, email confirm) take the
same origin and JSON-body rule (`RequireTrustedOrigin` in
`routes_public.go`), and OAuth consent re-checks it. `/account/*` runs behind
`AuthMiddleware`, so it is covered there. Trusted: `APP_BASE_URL`, `PUBLIC_BASE_URL`, `API_ALLOWED_ORIGINS`, and the
request's own origin (`Host`, or `X-Forwarded-Host` from a
`TRUSTED_PROXY_CIDRS` peer only; scheme compared when the request is known to
be HTTPS). Judged on `Origin`, else `Sec-Fetch-Site` (`same-origin` only),
else the `Referer`'s origin. `Sec-Fetch-Site: same-origin` never overrides
an `Origin`: it compares the page with the URL fetched, which may be a proxy
to the API on another origin (the status page's `/_sp_api`). None of the
three means a non-browser client (`curl` with a cookie jar), which is
allowed -- and it is what the UI's `/api` proxy sends for its own pages: it
compares the page's origin with the `Host` it was reached with (one value;
`X-Forwarded-Host` instead, alone, only with
`API_PROXY_TRUST_FORWARDED_HOST=true`) and, when they match, drops Origin,
Referer and Sec-Fetch-Site (`web/lib/api-proxy.ts`). A page on another origin
cannot match: the browser sets Host from the URL. The proxy never reads or
passes on a client `X-Forwarded-Host` otherwise, since anything forwarding
client headers to it would let a script choose one. So the UI works through its proxy on
any host or port with no origin configuration; the bundled ingress sends
`/api` to the API with the browser's Host, which is the API's own origin. The
403 names the refused origin and `APP_BASE_URL`. A cookie-authenticated unsafe request with a body must also be
`application/json` (`multipart/form-data` on `MultipartUploadPaths`, the
import preview), else `415 unsupported_media_type`, so a preflight-free
`text/plain` request cannot carry JSON. API keys and MCP tokens are not
checked. A status page served from the UI's own origin cannot be told apart
from the UI; give status pages their own host.

## Workspaces

Superadmins create, rename and delete workspaces
(`api/internal/services/tenants/workspaces.go`). Names are trimmed, 1–100
characters without control characters, and unique among live workspaces
(partial unique index `tenants_name_live_key`). A new workspace gets no
membership: superadmins act everywhere. Delete needs `confirm_name` equal to
the name and refuses the default workspace (`tenancy.DefaultTenantID`), the
OIDC JIT tenant while JIT provisioning is on, and the last live workspace
(a transaction advisory lock serializes deletes). Each change is audited
explicitly (`tenant.create`/`.rename`/`.delete`, platform-level, with the
name).

Delete is a soft delete. One transaction stamps `tenants.deleted_at` and
`deleted_by`, removes the workspace's memberships and SSO group mappings and
revokes its MCP grants and API keys. From that commit:

- `identityQuery` reports whether a live workspace has the requested ID, and
  `AuthMiddleware` answers `404 workspace_not_found` to any session or
  MCP-token request scoped to a deleted, purged or never-created one,
  superadmins included (and to a member's request for a workspace they do
  not belong to). It is the one query that reads `deleted_at` tolerantly
  (`to_jsonb`): on a schema without the column it must not fall into the
  missing-schema fallback, which treats every admin as a superadmin.
- `auth.ValidateAPIKey`, `mcpoauth.LookupAccessToken`, the push-token lookup,
  the discovery collector lookups, `locations.AuthenticateWorker` (NATS
  callout) and the ingest location credential lookup add
  `tenancy.LiveTenantSQL` to their query, so a credential written by a
  request racing the delete is refused too.
- Tenant lists (`GET /tenants`, memberships on users and `/auth/me`, MCP
  connections) omit it; acknowledge links and report-unsubscribe links of it
  read as invalid.

Memberships (user create and update) and SSO group mappings name live
workspaces only: a deleted or unknown one is refused with `422` "workspace
not found".

The web client treats `workspace_not_found` as "the selected workspace is
gone": it drops that selection (only if it is still the one the failing
request carried, so concurrent failures change nothing more) and the
switcher reloads its list once (`lib/api/core.ts`, `TENANTS_CHANGED_EVENT`).
It never sends `X-Tenant-ID` on platform routes (`isPlatformPath`:
`/v1/tenants*`, `/v1/account*`, `/v1/oauth/grants*`, `/v1/users*`,
`/v1/oidc-group-mappings*`, `/v1/openapi.json`), so the reload cannot fail
the same way, and they need no workspace. A member left with no workspace
keeps the account and its account page (sessions, password, email,
two-factor); the client answers their tenant-scoped requests with a "not a
member of any workspace" error instead of sending them.

The scheduler purges the data afterwards; see `docs/architecture.md`.

Audit runs *outside* the write gate so denied attempts are recorded with
`outcome=denied`.

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
failures, `auth.lockout`, `auth.session_reuse`, `auth.session_revoke`,
`auth.sessions_revoke_others`, `auth.password_reset_request`,
`auth.password_reset`, `auth.invite_accept`, `auth.email_change_request`,
`auth.email_change`, `auth.password_change`, `auth.two_factor_enable`,
`auth.two_factor_disable`, `auth.recovery_codes_regenerate`) and user/key
management (incl. `user.unlock`, `user.session_revoke`, `user.sessions_revoke`,
`user.invite`, `user.password_reset_link`, `user.two_factor_reset`) (those subtrees are skipped by the
generic layer). Writes go through an async batching recorder — never
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
bootstrap guard). Whichever path runs first claims superadmin.
With `BOOTSTRAP_SETUP_CODE` set this parity is off: every JIT user, the
first included, gets the ordinary JIT role (default membership or mapped
roles), and password bootstrap stays open until an active *superadmin*
exists (not just any active user), so the first superadmin always comes
from the code-gated form (`oidcauth.Service.ConfigureSetupCode`,
`adminusers.Service.ConfigureSetupCode`). With or without a code, both
paths close for good once any superadmin has existed
(`admin_bootstrap_closed`, set by a trigger on `admin_users`), so an install
whose users were all disabled or removed out of band is not reopened to
whoever holds the code or signs in first. There is no SSO-only mode that
disables the password form, so such an install cannot be left without a
way to create an admin; `cmd/admin` remains the break-glass path. The
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

The helm migrations job is a pre-upgrade hook and Compose services wait for
the `migrations` service, so new binaries start on the migrated schema; a
deployment that skips that step (`migrations.enabled=false`, a rendered
chart applied without its hooks) can still run them briefly on the old
schema. All auth queries feature-detect (SQLSTATE 42P01/42703 →
legacy behavior: everyone superadmin, keys full-access) and the audit
writer drops rows — the API never crash-loops pre-migration. The exception
is workspace soft delete (000116): API-key validation, tenant lists and
every background loop name `tenants.deleted_at`, so that migration must run
before the new binaries serve (the release's upgrade notes say so);
`identityQuery` alone reads it tolerantly, so a missing column can never
elevate a member. Without the hook, run the migrations first (see
`docs/correctness-notes.md` "Deploy ordering").

## Verification

`scripts/verify-auth.sh` runs the full matrix against a local stack
(30 checks): role × method enforcement, membership filtering, scoped and
expiring keys, audit capture, and (with `VERIFY_SSO=1` + the dex profile)
the full OIDC login flow.
