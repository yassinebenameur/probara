# Authentication, RBAC, Audit & SSO

How identity and authorization work as of the enterprise-auth rework
(migrations 000073–000076).

## Identity model

Two credential types, resolved by `api/internal/middleware/auth.go`:

- **Admin users** (`admin_users`) — humans. JWT access cookie (15 min) +
  rotating opaque refresh session. Two platform roles:
  - `superadmin` — full platform access: any tenant (via `X-Tenant-ID`),
    user management, tenant list. All pre-rework admins were backfilled to
    superadmin.
  - `member` — access only through `tenant_memberships` rows. A
    tenant-scoped request requires a membership in that tenant (403
    otherwise); `GET /v1/tenants` returns only the member's tenants.
- **API keys** (`api_keys`) — machines. Pinned to one tenant, carry a
  `scope` (`read` | `write`, legacy keys backfilled to `write`) and an
  optional `expires_at`. Expired keys fail validation in SQL.

Tenant roles (fixed, on `tenant_memberships.role`):

| Role   | Can                                                      |
|--------|----------------------------------------------------------|
| admin  | everything in the tenant incl. API keys, settings, audit  |
| editor | create/modify monitors, alerts, incidents, status pages…  |
| viewer | read-only (plus compute-only POSTs like `/monitors/test`) |

Roles are resolved from the DB on every request — never stored in JWT
claims — so downgrades and disables apply immediately (disabled users are
rejected even with a live access token).

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
  but using saved secrets (`monitor_id` with a secret-bearing monitor type)
  requires write capability. The saved and requested monitor types must
  match. Test requests cannot supply ciphertext envelopes directly: use
  write-only placeholders for existing credentials.
- `RequireTenantAdmin`: API key create/revoke, tenant settings mutation,
  audit log. Superadmin or tenant-role admin; API keys never pass.
- `RequireSuperadmin`: `/users/*`.

Audit runs *outside* the write gate so denied attempts are recorded with
`outcome=denied`.

## Audit log

`audit_log` table; captured two ways: the `AuditMutations` middleware
derives `resource.verb` actions for every mutation, and explicit events
cover auth lifecycle (`auth.login`/`auth.logout`/`auth.oidc_login` incl.
failures) and user/key management (those subtrees are skipped by the
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
legacy behavior: everyone superadmin, keys full-access) and the audit
writer drops rows — the API never crash-loops pre-migration. For
zero-surprise rollouts run the migration job manually first (see
`docs/correctness-notes.md` "Deploy ordering").

## Verification

`scripts/verify-auth.sh` runs the full matrix against a local stack
(30 checks): role × method enforcement, membership filtering, scoped and
expiring keys, audit capture, and (with `VERIFY_SSO=1` + the dex profile)
the full OIDC login flow.
