import type { DocPage } from "../types";

export const ADMINISTRATION_PAGE: DocPage = {
  slug: "administration",
  group: "Use Probara",
  title: "Administration",
  description:
    "Manage users, roles, tenants, API keys, OIDC, audit history, AI settings, retention, and imports.",
  eyebrow: "Governance",
  readingTime: "32 min read",
  keywords: [
    "RBAC",
    "OIDC",
    "API keys",
    "audit log",
    "tenant settings",
  ],
  sections: [
    {
      id: "credential-model",
      title: "Credential model",
      blocks: [
        {
          type: "table",
          columns: ["Credential", "Lifetime and tenant behavior", "Use"],
          rows: [
            [
              "Administrator session",
              "Short-lived signed access cookie plus rotating opaque refresh token; tenant selected from memberships",
              "Interactive UI and administrative API",
            ],
            [
              "Tenant API key",
              "Revocable, optionally expiring, permanently pinned to one tenant",
              "Automation and integrations",
            ],
            [
              "Push token",
              "Monitor-specific capability URL",
              "Passive push heartbeat only",
            ],
            [
              "Location credential",
              "Private-location identity constrained by NATS authorization",
              "Remote worker queue access",
            ],
          ],
        },
        {
          type: "paragraph",
          text:
            "Administrator access tokens default to a short lifetime (15 minutes in the standard configuration), while refresh tokens default to 30 days and rotate on use. A rotated refresh token still earns an access token (never another refresh token) for 60 seconds, so concurrent refreshes from parallel requests or multiple tabs do not end the session; presented again after that window, it signs its session out. Each sign-in is one [session](#sign-in-lockout-sessions) across those rotations, checked on every request, so signing a session out takes effect at once. Disabled users are checked against database state on authenticated requests.",
        },
      ],
    },
    {
      id: "roles",
      title: "Platform and tenant roles",
      blocks: [
        {
          type: "table",
          columns: ["Role", "Scope", "Capabilities"],
          rows: [
            [
              "Platform `superadmin`",
              "Installation",
              "User administration, any tenant context, and all tenant operations",
            ],
            [
              "Platform `member`",
              "Installation",
              "Only tenants granted through membership",
            ],
            [
              "Tenant `admin`",
              "One tenant",
              "All tenant operations, including API keys, tenant settings, and audit access",
            ],
            [
              "Tenant `editor`",
              "One tenant",
              "Normal monitoring mutations but not API-key, tenant-setting, or audit administration",
            ],
            [
              "Tenant `viewer`",
              "One tenant",
              "Reads plus approved compute-only diagnostics",
            ],
          ],
        },
        {
          type: "paragraph",
          text:
            "Viewer and read-key compute-only POST routes include monitor test, import preview, dependency suggestions, mesh probe, and AI connection test. Persisted operations such as `Run now`, channel test, acknowledgement, or configuration updates require [write authorization](/docs/security/#roles-scopes-tenancy).",
        },
        {
          type: "paragraph",
          text:
            "AI training labels are writes, not diagnostics: confirming or correcting an [incident analysis](/docs/alerting/#ai-analysis) or an [alert triage](/docs/alerting/#ai-triage) and accepting or dismissing a [dependency suggestion](/docs/dependencies/#suggestions) need an editor, admin, superadmin, or write API key, and viewers and read keys get HTTP 403. Only tenant admins and superadmins, signed in, can [export labels](#ai-settings); every API key is refused, write scope included. The same holds for [SIP test-call monitors](/docs/monitors/#network-protocols), which place real calls: creating, changing, pausing, running, testing or importing one needs a signed-in tenant admin or superadmin, and editors and API keys get HTTP 403.",
        },
        {
          type: "callout",
          tone: "warning",
          title: "Write scope is not tenant admin",
          text:
            "A write API key can mutate normal tenant resources but does not inherit human-only tenant administration. Keep user, role, tenant-setting, key-management, and audit boundaries explicit in integrations.",
        },
      ],
    },
    {
      id: "bootstrap-users",
      title: "Bootstrap and manage users",
      blocks: [
        {
          type: "paragraph",
          text:
            "On an empty installation, the public bootstrap status and first-user endpoints allow exactly the initial account to be created. That first active account is a superadministrator. With `BOOTSTRAP_SETUP_CODE` set on the API, the first-user endpoint also requires that code (missing and wrong codes get the same 403), and the status endpoint reports that one is required, never the code itself. Bootstrap then stays open until an active superadministrator exists, not merely any user, because SSO sign-ins no longer create one. Either way it closes for good once any superadministrator has existed, even if every user is later disabled or removed outside the product. The last active superadministrator cannot be demoted or deleted, and concurrent demotions and deletions are serialized so they cannot remove the last two at once. OIDC just-in-time login can also initialize the first superadministrator on an empty installation, and an operator with database access can create or recover one at any time with [`go run ./cmd/admin`](/docs/operations/#database-migrations).",
        },
        {
          type: "table",
          columns: ["User variable", "Validation and behavior"],
          rows: [
            [
              "`username`",
              "3–64 characters; letters, numbers, `.`, `_`, and `-`",
            ],
            ["`email`", "Optional, used for OIDC linking, SSO-only users, and account emails (invites, reset links, security notices)"],
            ["`password`", "At least 12 characters and at most 72 bytes (the bcrypt limit) when local login is enabled"],
            [
              "`platform_role`",
              "`superadmin` or `member`",
            ],
            [
              "`memberships`",
              "Tenant IDs paired with `admin`, `editor`, or `viewer`; patch replacement is authoritative",
            ],
            [
              "`auth_method`",
              "Indicates local, OIDC/SSO-only, or linked authentication behavior",
            ],
          ],
        },
        {
          type: "paragraph",
          text:
            "A user with email and no password can be provisioned as SSO-only, or [invited](#invites-resets-two-factor) to set a password. Superadministrator-only user routes create, list, update, and delete accounts. Safeguards prevent deleting the current user or leaving the installation without an administrator.",
        },
        {
          type: "callout",
          tone: "warning",
          title: "Membership patches replace the list",
          text:
            "When updating memberships, send the complete intended set. Treating the field as an incremental add operation can accidentally remove tenant access.",
        },
      ],
    },
    {
      id: "sign-in-lockout-sessions",
      title: "Sign-in lockout and sessions",
      blocks: [
        {
          type: "paragraph",
          text:
            "Ten failed password sign-ins in a row lock that account's password sign-in for 15 minutes; a successful sign-in resets the count, a failure more than 24 hours after the previous one starts a new count, and the lock lifts on its own. The sign-in page answers a locked account exactly like a wrong password, so the user is not told they are locked beyond the rule the page states. Users shows a **Locked** badge, and the user's page shows when the lock ends, the current run of failures, and **Unlock sign-in** (`POST /api/v1/users/{id}/unlock`, MCP `unlock_user`), which lifts the lock and clears the count. Unlocking and locking are audited (`user.unlock`, `auth.lockout`). SSO sign-ins are never locked. What bounds abuse of the lock, and its anti-enumeration design, are in [Security](/docs/security/#sign-in-lockout-sessions).",
        },
        {
          type: "paragraph",
          text:
            "Every user manages their own sessions under **Account → Sessions** (the account menu in the sidebar): each sign-in, password or SSO, with its browser, client address, sign-in time and last activity, this device marked; sign out one, or everywhere else. A superadministrator sees and signs out any user's sessions on that user's page (`GET`/`DELETE /api/v1/users/{id}/sessions`, `DELETE /api/v1/users/{id}/sessions/{sessionId}`; MCP `list_user_sessions`, `revoke_user_session`, `revoke_user_sessions`). A signed-out session stops working on its next request. Setting a user's password signs them out everywhere (your own: everywhere else); deleting a user ends all their sessions; a lockout, an unlock and role or membership changes end none. Signing out an SSO session does not sign the user out of the identity provider. API keys and MCP connections are not sessions and are managed under Settings.",
        },
      ],
    },
    {
      id: "invites-resets-two-factor",
      title: "Invites, password resets and two-factor sign-in",
      blocks: [
        {
          type: "paragraph",
          text:
            "A superadministrator manages another user's access from the **Sign-in access** panel of the user's page. **Send invite** (`POST /api/v1/users/{id}/invite`, MCP `invite_user`) gives an account without a password a single-use link to set one, valid 7 days; sending another replaces it. For an account that signs in with single sign-on, or with `method: \"sso\"`, the invite only tells the user to sign in with SSO. With single sign-on configured, inviting an account that is not linked yet needs an explicit choice (the panel preselects SSO): a password invite gives the account a Probara password, after which the identity provider can never link it by email, so the user signs in without the provider’s multi-factor authentication. **Generate reset link** (`POST /api/v1/users/{id}/password-reset`, MCP `create_password_reset_link`) issues a single-use reset link valid 1 hour for a password account; completing it signs the user out of every browser and revokes their MCP connections (workspace API keys stay). **Reset two-factor** (`POST /api/v1/users/{id}/reset-2fa`, MCP `reset_user_two_factor`) turns a user's two-factor sign-in off after a lost device: they sign in with the password alone and can enrol again; their sessions stay signed in and a pending email change is cancelled. All three are audited (`user.invite`, `user.password_reset_link`, `user.two_factor_reset`).",
        },
        {
          type: "paragraph",
          text:
            "With email delivery (`SMTP_*` and `APP_BASE_URL` on the API) links are emailed to the account's address and never shown. Without it, or for an account with no address, the response (and the panel) shows the link once: copy it and send it over a channel you trust; it is not logged or stored in readable form. When `APP_BASE_URL` is unset the link is a path on the operator UI, which the panel completes with the address you are using.",
        },
        {
          type: "paragraph",
          text:
            "Users manage their own account under **Account**: **Password** (needs the current password; signs out every other browser), **Email** (a confirmation link goes to the new address and a notice to the current one; the change applies when the link is used; needs email delivery), and **Two-factor** (an authenticator app plus 10 single-use recovery codes, shown once; enrolment needs `PROBARA_SECRETS_KEY` on the API). The sign-in page links to **Forgot password**, which emails a reset link, or, without email delivery, tells the user to ask an administrator. None of this applies to SSO accounts, whose identity provider owns the password, email address and multi-factor authentication. Expiries, what each step signs out, and the anti-enumeration rules are in [Security](/docs/security/#account-links-two-factor).",
        },
        {
          type: "callout",
          tone: "info",
          title: "Lost authenticator on the last superadmin account",
          text:
            "Sign in with a recovery code (it signs out every other session). Without one and without another superadministrator, run `go run ./cmd/admin -reset-2fa <username>` with `POSTGRES_URL` set (it works on a database the migrations have not reached yet), then sign in with the password.",
        },
      ],
    },
    {
      id: "tenant-context",
      title: "Tenant context",
      blocks: [
        {
          type: "paragraph",
          text:
            "An administrator session can select a tenant using `X-Tenant-ID`; a `tenant_id` query fallback is supported by the authentication context where applicable. The server verifies membership unless the user is a superadministrator.",
        },
        {
          type: "paragraph",
          text:
            "API keys ignore arbitrary tenant selection and remain pinned to their creation tenant. The tenant-list endpoint is for administrator sessions and returns only allowed tenant context. Superadministrators create, rename and delete workspaces; see [Workspaces](#workspaces).",
        },
        {
          type: "callout",
          tone: "warning",
          title: "Never reuse a cross-tenant cache key",
          text:
            "External integrations should include tenant identity in caches, queues, and idempotency keys even when resource UUIDs appear globally unique.",
        },
      ],
    },
    {
      id: "workspaces",
      title: "Workspaces",
      blocks: [
        {
          type: "paragraph",
          text:
            "Only superadministrators manage workspaces: the **Workspaces** page (listed with each workspace's member and monitor counts), the **New workspace** entry at the end of the workspace switcher, `POST /api/v1/tenants`, `PATCH /api/v1/tenants/{id}`, `DELETE /api/v1/tenants/{id}` and `GET /api/v1/tenants/overview`, or the matching [MCP](/docs/mcp/) tools. Members and API keys are refused. Each change is audited as `tenant.create`, `tenant.rename` or `tenant.delete` with the name, as a platform event that stays visible after the workspace is gone.",
        },
        {
          type: "list",
          items: [
            "**Names** are 1–100 characters with no control characters, trimmed, and unique among live workspaces (exact match, so `Ops` and `ops` can coexist). A deleted workspace's name is free again at once.",
            "**A new workspace starts empty**: no monitors or channels, the shared worker fleet, default notification settings, workspace AI on, and no members. Superadministrators act in every workspace without a membership; add members on the [user](#bootstrap-users) pages. Creating one from the switcher or the Workspaces page switches to it.",
            "**Rename** changes only the name; IDs, keys and links keep working.",
          ],
        },
        {
          type: "paragraph",
          text:
            "**Delete** is permanent and has no undo window. The confirmation must repeat the workspace's name (`confirm_name` in the API). The default workspace cannot be deleted, nor, while SSO just-in-time provisioning is on, the workspace new SSO users join (`OIDC_JIT_DEFAULT_TENANT_ID`), nor the last remaining workspace.",
        },
        {
          type: "table",
          columns: ["When", "What happens"],
          rows: [
            [
              "At once",
              "The workspace disappears from every list and switcher, and its memberships, SSO group mappings and MCP connections are removed. Every credential scoped to it is refused: sessions and MCP tokens get `404 workspace_not_found` on its routes (superadministrators too, and still after the purge: the same answer as for an ID that never existed), its API keys (OTLP ingest included) get `401`, push URLs and discovery collector tokens `404`, its private-location workers are refused by the broker on their next connection and their results and heartbeats are dropped, and acknowledge and report-unsubscribe links read as invalid.",
            ],
            [
              "Within seconds",
              "Its monitors are no longer dispatched, results still in flight are discarded, and the absence watchdog, mesh probes and maintenance windows skip it. Its status pages stop being served once the status-page cache expires (`STATUS_PAGE_CACHE_TTL`, 10 seconds by default) and visitor push stops. The alerter neither evaluates nor notifies for it: open alerts are left as they are and are not resolved (so its channels hear nothing), and notifications still queued are dropped. Scheduled SLA reports and AI analyses stop.",
            ],
            [
              "In the background",
              "Within the first purge run (seconds after the delete, every `MONITOR_PURGE_INTERVAL_SECONDS`) the scheduler blanks its stored secrets: channel credentials, monitor credentials (secret fields, the plain `headers`, `metadata` and `variables` maps, and the password and credential parameters of monitor URLs) and push tokens, private-location worker credentials. It then erases the data in bounded batches, table by table (API keys, tokens and status pages first, then history, then configuration), and deletes the workspace row last. Every run handles at most `MONITOR_PURGE_MAX_ROWS_PER_RUN` rows in `MONITOR_PURGE_BATCH_SIZE` batches, so a large workspace takes several runs; progress shows in the scheduler log and the `workspace_purge_*` metrics ([Operations](/docs/operations/#retention)).",
            ],
          ],
        },
        {
          type: "callout",
          tone: "info",
          title: "What is kept",
          text:
            "Audit log entries about the workspace are kept and age out under `AUDIT_RETENTION_DAYS`. Status page slugs are unique across the installation, so a deleted workspace's slugs become free once the purge has removed its pages, usually within the first run. Users are platform accounts and are never deleted with a workspace.",
        },
        {
          type: "paragraph",
          text:
            "A browser whose selected workspace is deleted switches to another one the user can open on its next request. A member whose only workspace was deleted keeps the account and can still sign in, but sees “You are not a member of any workspace” until a superadministrator adds a membership. Memberships and SSO group mappings can only name a live workspace: a deleted one is refused as “workspace not found”.",
        },
      ],
    },
    {
      id: "api-keys",
      title: "API keys",
      blocks: [
        {
          type: "table",
          columns: ["Variable", "Meaning"],
          rows: [
            ["`name`", "Operator-facing key purpose"],
            [
              "`scope`",
              "`read` or `write`; defaults to write when omitted in the current API",
            ],
            ["`expires_at`", "Optional future expiration time"],
            [
              "`key`",
              "Full secret returned only in the creation response",
            ],
            ["`key_prefix`", "Non-secret SHA-256-derived identifier returned in list responses to correlate keys; not a fragment of the secret"],
            ["`last_used_at`", "Usage timestamp, updated with write throttling"],
            ["`created_by` / `created_at`", "Creation audit metadata"],
            ["`revoked`", "Whether the key can no longer authenticate"],
          ],
        },
        {
          type: "code",
          language: "bash",
          title: "Authenticate with a tenant API key",
          code:
            "curl -H 'Authorization: Bearer pk_<secret>' \\\n  https://monitoring.example.com/api/v1/monitors",
        },
        {
          type: "list",
          ordered: true,
          items: [
            "Create the key as a tenant administrator and choose the [minimum scope](/docs/api/#authentication).",
            "Copy the full key from the one-time creation response into a secret manager.",
            "Use `key_prefix` to identify the key record later; it is a derived identifier, not a recognizable fragment of the secret, and list responses never reveal the full key.",
            "Set an expiration and rotate before it, rather than keeping indefinite integration credentials.",
            "Revoke the key when a client is retired or the secret may have leaked.",
          ],
        },
        {
          type: "paragraph",
          text:
            "`last_used_at` is intentionally not updated on every request; writes are throttled to roughly five-minute granularity. Use it for coarse inventory, not per-request forensics.",
        },
      ],
    },
    {
      id: "oidc",
      title: "OpenID Connect",
      blocks: [
        {
          type: "paragraph",
          text:
            "Probara supports one [platform identity provider](/docs/configuration/#oidc) using the authorization-code flow with PKCE. Signed state cookies and nonce validation protect the browser redirect. Provider discovery is loaded lazily as the login flow needs it.",
        },
        {
          type: "list",
          items: [
            "OIDC identities are bound by the stable `(issuer, subject)` pair.",
            "A verified email can link to an explicitly SSO-only account.",
            "An unverified email is not trusted for account linking.",
            "A subject already bound to another account is refused.",
            "When just-in-time provisioning is enabled, new users receive the configured default platform/tenant membership rather than arbitrary claims-based privilege — unless OIDC group mappings exist, in which case mapped roles replace the JIT defaults.",
            "On an empty installation, the first successful JIT login becomes the initial superadministrator, unless `BOOTSTRAP_SETUP_CODE` is set. With a setup code, every JIT user, the first included, gets the ordinary JIT role (default membership, or the mapped roles when group mappings exist); create the first superadministrator through the first-administrator form with the code, then promote SSO users. The login page keeps the SSO button available during that setup so those members can sign in.",
          ],
        },
        {
          type: "callout",
          tone: "warning",
          title: "Verify redirect and public origins",
          text:
            "The provider callback must match the externally reachable API origin and proxy scheme. Misconfigured `PUBLIC_BASE_URL` or forwarded headers can break stateful login even when local login works.",
        },
      ],
    },
    {
      id: "oidc-group-mappings",
      title: "OIDC group mappings",
      blocks: [
        {
          type: "paragraph",
          text:
            "Superadmins can map identity-provider groups to roles under Settings → Access → OIDC group mappings (or via `/api/v1/oidc-group-mappings`). A mapping targets either one tenant with a role (`admin`, `editor`, `viewer`) or the platform (grants `superadmin`). Groups are read from the ID-token claim named by [`OIDC_GROUPS_CLAIM`](/docs/configuration/#oidc); request the `groups` scope via `OIDC_SCOPES` so the provider sends it.",
        },
        {
          type: "list",
          items: [
            "Zero mappings means the feature is off: JIT defaults apply and roles stay manually managed. Deleting all mappings restores that behavior immediately.",
            "With any mappings present, the identity provider is the source of truth for SSO users: platform role and tenant memberships are re-derived from the user's groups on every login, overwriting manual edits.",
            "A user in several groups mapping to the same tenant gets the highest role (`admin` > `editor` > `viewer`). Group names match case-sensitively.",
            "A user in no mapped groups syncs to a member with no tenant memberships — deliberately no fallback to the JIT default, which would silently re-grant revoked access.",
            "The sync never demotes the last active superadmin; the demotion is skipped and flagged in the audit log (`skipped_last_superadmin_demotion`).",
            "Password-based accounts are never touched by group sync.",
            "Applied changes are recorded as `auth.oidc_role_sync` audit events with the full membership diff, the matched groups, and every group the token presented (`received_groups`) — the place to look when a name doesn't match.",
            "Groups presented in verified ID tokens at successful logins are catalogued and offered as suggestions in the mapping editor (with click-to-prefill chips for groups that have no mapping yet). OIDC has no API to enumerate an IdP's groups, so the catalog only knows groups someone has already logged in with.",
            "Mappings accept an optional display label (click a row's group cell to edit). Matching always uses the raw claim value — essential with Azure AD, whose groups claim carries object IDs (GUIDs), not names: label the GUID rows so the table stays readable.",
          ],
        },
        {
          type: "callout",
          tone: "warning",
          title: "Groups must arrive in the ID token",
          text:
            "Probara reads groups from the ID token only — there is no userinfo-endpoint fallback yet. Configure the provider to embed the claim (Okta: a Groups claim filter on the authorization server; Azure AD: group claims in token configuration, noting that past ~200 groups Azure sends a reference instead of values). If mappings exist but the token carries no groups claim, users sync to no mapped roles and the API logs a warning.",
        },
      ],
    },
    {
      id: "tenant-settings",
      title: "Tenant settings",
      blocks: [
        {
          type: "table",
          columns: ["Setting", "Constraint and effect"],
          rows: [
            [
              "`data_retention_days`",
              "`0` keeps mesh history indefinitely; otherwise 30–3,650 days. Raw check results are always capped at 30 days",
            ],
            [
              "`dashboard_group_tags`",
              "Up to 50 non-empty unique tags, each up to 64 characters, used to curate service-summary groups",
            ],
          ],
        },
        {
          type: "paragraph",
          text:
            "[Retention](/docs/operations/#retention) applies to mesh history and raw agent metric samples. Raw check results are kept 30 days regardless of this setting, and uptime/latency history beyond that comes from rollups kept 400 days. Audit retention is configured separately at platform level. Dashboard group tags change presentation only; they do not create monitor groups or notification rollup.",
        },
      ],
    },
    {
      id: "ai-settings",
      title: "Workspace local AI settings",
      blocks: [
        { type: "table", columns: ["Field", "Meaning"], rows: [
          ["`enabled`", "Writable; enable dependency suggestions, asynchronous incident analysis and alert triage for the workspace"],
          ["`installation_enabled`", "Installation-level availability switch; overrides workspace enablement"],
          ["`available` / `model`", "Read-only local-service readiness and model identity. Each API process caches the result for about 30 seconds, failures included, so a just-fixed service can read as unavailable until the cache expires."],
          ["`message`", "One of four fixed readiness strings (disabled, not configured correctly, unreachable, not ready); details are only in the API logs"],
          ["`auto_triage_enabled`", "Writable, default `false`; [triage new alerts automatically](/docs/alerting/#ai-triage). Takes effect only while `enabled` is also on. With it off, alerts are not triaged at all, so [SLA report](/docs/slas/#suspected-causes) outages land in Not triaged"],
          ["`triage_delay_seconds`", "Writable, default `60`, 0–3600; how long an alert must stay open before it is triaged. The wait is never shorter than two check intervals of the alert's monitor (that part capped at one hour), so `0` still waits for them"],
          ["`triage_cooldown_seconds`", "Writable, default `900`, 0–86400; skip an alert's triage when its monitor already has a ready triage completed within this time. `0` turns the cooldown off"],
          ["`triage_max_per_hour`", "Writable, default `60`, 1–600; triages claimed per rolling hour in this workspace, skipped ones not counted"],
          ["`triage_notifications_enabled`", "Writable, default `false`; [send suspected causes](/docs/alerting/#ai-triage) once to each channel already paged for an alert (not PagerDuty or Opsgenie). Takes effect only while `enabled` and `auto_triage_enabled` are also on, and only for triages completed after it was turned on"],
        ] },
        { type: "paragraph", text: "Settings → AI controls the workspace switch and tests the installation-owned CPU service. Absent workspace settings default enabled, with auto-triage off and the triage defaults above; existing enable and disable switches are preserved. Settings updates are partial: any of the six writable fields, at least one, and an omitted field keeps its stored value. Out-of-range numbers and legacy provider fields return a validation error. Editors, admins, superadmins and write API keys can change these settings; viewers see them read-only. The **Automatic alert triage** block (**Triage new alerts automatically**, **Send suspected causes to alert channels**, **Wait before triage (s)**, **Cooldown per monitor (s)**, **Max triages per hour**) is locked while local AI is disabled for the installation or the workspace switch is off, and **Send suspected causes to alert channels** also while auto-triage is off. The API still accepts `auto_triage_enabled` and `triage_notifications_enabled` with AI or triage off; no alert is triaged, and no follow-up sent, until every switch they depend on is on. The connection test accepts an empty object, cannot accept destinations or credentials, always probes afresh and refreshes the readiness cache of the API replica that serves it." },
        { type: "paragraph", text: "Tenant admins also see **Training labels** under Settings → AI. **Export labels** downloads every label in the workspace as JSONL training rows (`POST /api/v1/ai-labels/export`, which also accepts a `since`/`until` window and subject types). A row carries evidence counters, catalogue answers and provenance, with no monitor names or IDs (see [AI training labels](/docs/security/#notifications-integrations-ai)). Labelling and export never call the model, so they work while local AI is disabled." },
        { type: "paragraph", text: "Model decisions are advisory and explanations cite bounded monitoring evidence. No configuration secrets, bodies, screenshots or arbitrary error text are sent to the model. Suggested edges and troubleshooting actions require operator review. Controlled scenarios do not establish real-world accuracy." },
      ],
    },
    {
      id: "audit",
      title: "Audit log",
      blocks: [
        {
          type: "paragraph",
          text:
            "Tenant administrators and platform superadministrators can review [audit events](/docs/security/#audit-proxy-trust) for mutations and important authentication/authorization paths. Records include actor type, identifier and label, action, resource, outcome, HTTP status, IP address, user agent, structured details, and timestamp.",
        },
        {
          type: "table",
          columns: ["Filter", "Behavior"],
          rows: [
            ["`action`", "Exact action name; discover available actions from the actions endpoint"],
            ["`outcome`", "`success`, `failure`, or `denied`"],
            ["`actor_id`", "Filter by actor identifier"],
            ["`from` / `to`", "RFC 3339 time bounds"],
            ["`page` / `page_size`", "Paginated results"],
            [
              "`sort` / `dir`",
              "Order by `occurred_at` (the default, newest first), `action`, `resource` (type, then ID) or `outcome`, `asc` or `desc`; ties read newest first. Any other value is HTTP 400",
            ],
          ],
        },
        {
          type: "paragraph",
          text:
            "AI labels record as `incident.label` (resource `incident/{id}`), `alert.label` (alert triage, resource `alert/{id}`), `monitor.label` (dependency suggestions), and `ai-label.export`; AI settings changes record as `ai-setting.update`. A viewer's refused label attempt is recorded as `denied`.",
        },
        {
          type: "paragraph",
          text:
            "Bulk alert triage (`POST /alerts/bulk/acknowledge`, `/resolve`, `/unacknowledge`, `/snooze` and `/unsnooze`) records one `alert.acknowledge`, `alert.resolve`, `alert.unacknowledge`, `alert.snooze` or `alert.unsnooze` event per requested alert (resource `alert/{id}`), shaped like the single action's, with `details.bulk: true`. An alert the batch skipped records as `failure` (HTTP 404, as the single action would answer) with `details.reason`. These per-alert events are written before the response, so they are not subject to the recorder's buffer, and they replace the usual request-level event; a refused or invalid bulk request (403, 400) records one request-level event as any other write does.",
        },
        {
          type: "paragraph",
          text:
            "Kubernetes discovery writes record named events: `discovery-integration.create`, `.update`, `.delete` and `.rotate-token` (resource `discovery-integration/{id}`; `.update` carries `details.added` and `details.removed`, the namespaces actually added or removed, plus `details.name_changed` and `details.location_changed`, and an edit that changes nothing records no event), `discovery-link.create` and `.delete` (resource `discovery-service/{id}`, `details.monitor_id`), and `discovery.approve`, filed under the monitor the approval created (resource `monitor/{id}`, with `details.discovered_service_id` and `details.suggestion_key`); a repeated approval that only returns the already linked monitor (`created: false`) writes nothing and records no event. As with bulk triage, these replace the request-level event on success, and a refused or failed discovery write records one request-level event.",
        },
        {
          type: "paragraph",
          text:
            "Audit writes are best-effort asynchronous operational records, not a transactional ledger. The platform `AUDIT_RETENTION_DAYS` setting defaults to 365 days; `0` keeps records indefinitely.",
        },
        {
          type: "callout",
          tone: "info",
          title: "Audit and telemetry retention are separate",
          text:
            "Changing a tenant's monitor retention does not change platform audit retention. Set and validate both against your incident-response and compliance requirements.",
        },
      ],
    },
    {
      id: "import",
      title: "Import monitors",
      blocks: [
        {
          type: "paragraph",
          text:
            "Monitor imports support JSON, YAML, and CSV with format autodetection. JSON/YAML can contain an array, a single object, common wrappers such as `items`, `monitors`, or `data`, a versioned portable YAML bundle, an Uptime Kuma backup export, a Better Stack or UptimeRobot API response, and a Gatus configuration file. YAML also recognizes common source wrappers such as `services`, `endpoints`, and `checks`.",
        },
        {
          type: "paragraph",
          text:
            "Five source schemas are recognized and translated automatically, so the field-mapping step is already complete when preview reports them: the portable bundle (`portable_monitor_export`), an Uptime Kuma export (`uptime_kuma_export`), a Better Stack export (`better_stack_export`), an UptimeRobot export (`uptimerobot_export`), and a Gatus configuration (`gatus_config`). Everything else goes through field mapping. For the four foreign tools, preview lists every monitor that will be created with its translation notes, every record that will not be, with the reason, and the settings ignored for the whole file. A domain monitor an importer adds from a source's domain-expiration check is labeled in the review table, and the review step can leave all of them out. A URL whose query carries a credential (`api_key=`, `token=`, ...) keeps it, stored as written and masked on reads, with a note naming the parameter: move it to a secret header where the endpoint allows. A monitor with no name of its own is named after its URL's host and path, never its query.",
        },
        {
          type: "list",
          ordered: true,
          items: [
            "Upload the source to import preview.",
            "Review detected field/type mappings, normalization, warnings, and row errors.",
            "Correct ambiguous group members and unsupported types.",
            "Execute the import and retain the per-row created/skipped/error report.",
            "Open created monitors and test credentials, location assignments, notification routing, and dependencies before enabling broad alerting.",
          ],
        },
        {
          type: "paragraph",
          text:
            "Mapped/simple import currently specializes in HTTP, ping, DNS, gRPC, and groups. Defaults include a 60-second interval, 30-second timeout, and enabled state when omitted. Duplicate name+type combinations are matched case-insensitively and skipped.",
        },
        {
          type: "callout",
          tone: "warning",
          title: "Preview is mandatory operationally",
          text:
            "The raw portable path accepts every monitor type in the live registry, including the database, broker, and WebSocket types. Agent rows in the simple mapped path are skipped, and masked exported secrets do not reconstruct valid credentials — re-enter them after import.",
        },
      ],
    },
    {
      id: "import-uptime-kuma",
      title: "Migrating from Uptime Kuma",
      blocks: [
        {
          type: "paragraph",
          text:
            "Upload an Uptime Kuma backup export and the importer translates monitor types, assertions, tags, retry tolerance, and group nesting on its own. Uptime Kuma 1.x writes that file from Settings → Backup → Export. Version 2.0 removed the backup button and Uptime Kuma has never had a REST API, so `scripts/kuma-export` in this repository logs into a running instance over its socket.io API and writes the same file: `go run ./scripts/kuma-export -url https://kuma.internal -user admin` (password from `-pass` or `$KUMA_PASSWORD`, plus `-totp` when two-factor is enabled).",
        },
        {
          type: "paragraph",
          text:
            "Translated types: `http`, `keyword` (a body assertion, inverted for invert-keyword), and `json-query` (a JSON path assertion) become HTTP monitors; `port` becomes TCP; `ping` and `dns` map directly; `push` becomes a push monitor; `grpc-keyword` becomes a gRPC health probe; `postgres`, `mysql`, `mongodb`, and `redis` map to their database monitors; and `group` becomes a group, with Uptime Kuma's parent links inverted into membership so nesting survives.",
        },
        {
          type: "paragraph",
          text:
            "Accepted status codes become explicit status ranges, redirect limits and TLS verification carry over on HTTPS monitors, and Uptime Kuma's certificate-expiry notification becomes `tls_min_days_valid`, which raises a dedicated TLS-expiry alert rather than failing the check — the same notify-only behavior. Retry counts land in `consecutive_failures_threshold`, clamped to 1-10.",
        },
        {
          type: "paragraph",
          text:
            "Monitors with no faithful equivalent are listed as not imported rather than approximated: `docker`, `sqlserver`, `steam`, `gamedig`, `mqtt`, `kafka-producer`, `radius`, `snmp`, `tailscale-ping`, `manual`, `smtp`, `real-browser`, and `rabbitmq` — Uptime Kuma polls the RabbitMQ management HTTP API while this platform performs an AMQP handshake, so recreate those by hand. Unsupported DNS record types and port monitors without a port are reported the same way.",
        },
        {
          type: "callout",
          tone: "warning",
          title: "Three behavior changes to expect",
          text:
            "Monitors using Uptime Kuma's \"upside down\" mode are skipped, because inverted up/down cannot be expressed and importing one would alert backwards. Push monitors receive a new token, so every heartbeat sender must be repointed at the new URL before the monitor is enabled. Database monitors are imported disabled: their password is deliberately not carried over, and the per-monitor notes name what to re-enter. An HTTP monitor whose basic-auth username arrives without its password (`scripts/kuma-export` redacts it) is imported disabled too, with a note to re-enter the basic auth password.",
        },
        {
          type: "callout",
          tone: "info",
          title: "Notifications and credentials are never migrated",
          text:
            "Uptime Kuma notifier configuration holds credentials, so `notificationIDList` is ignored and each affected monitor carries a note naming the notifiers it referenced — attach alert channels after import. HTTP basic auth is carried into the monitor's `basic_auth_username` and encrypted `basic_auth_password` (an `Authorization` header Uptime Kuma replaced with it is dropped, as Kuma did), and credential-bearing request headers into the encrypted `secret_headers`; OAuth2, NTLM, and client-certificate auth are dropped and reported. Monitor descriptions, retry intervals, resend intervals, and custom ICMP packet sizes have no equivalent and are reported as dropped.",
        },
      ],
    },
    {
      id: "import-better-stack",
      title: "Migrating from Better Stack",
      blocks: [
        {
          type: "paragraph",
          text:
            "Better Stack has no export button, so fetch your configuration from its Uptime API with an API token and upload the JSON. A single `GET /api/v2/monitors` response (or a single resource such as `GET /api/v2/monitors/{id}`) imports on its own; to keep heartbeats and groups, fetch those endpoints too and combine the responses into one file. Each response is one page: fetch every page by following `pagination.next` — preview warns about every response in the file whose next page the file does not hold.",
        },
        {
          type: "code",
          language: "bash",
          title: "Export from Better Stack",
          code: 'for path in monitors heartbeats monitor-groups heartbeat-groups; do\n  curl -s -H "Authorization: Bearer $BETTER_STACK_API_TOKEN" \\\n    "https://uptime.betterstack.com/api/v2/$path" > "$path.json"\ndone\njq -s . monitors.json heartbeats.json monitor-groups.json heartbeat-groups.json > better-stack.json',
        },
        {
          type: "table",
          columns: ["Better Stack", "Imported as"],
          rows: [
            ["`status`", "HTTP monitor accepting any 2xx"],
            ["`expected_status_code`", "HTTP monitor with `expected_statuses`"],
            ["`keyword`, `keyword_absence`", "HTTP monitor with a body `contains` (case-sensitive) or `not_contains` (any case) assertion"],
            ["`ping`, `tcp`", "Ping and TCP monitors"],
            ["`dns`", "DNS A-record lookup of the domain in `request_body` on the server in `url`; `required_keyword` becomes an expected answer"],
            ["Heartbeat", "Push monitor: `period` is the expected interval and `grace` the grace period, with a new push URL. A period over a day keeps its deadline: the interval is a day and the rest is added to the grace period"],
            ["Monitor and heartbeat groups", "Groups with their members; a paused group is created disabled"],
            ["`domain_expiration`", "One domain monitor per registrable domain (daily, down after two failing lookups, the widest warning window)"],
            ["`udp`, `smtp`, `pop`, `imap`, `playwright`", "Not imported, with the reason"],
          ],
        },
        {
          type: "paragraph",
          text:
            "`check_frequency` becomes the interval and `request_timeout` the timeout: seconds for HTTP monitors, milliseconds for the others — a non-HTTP value under 2000 could be either, so the default timeout is kept with a note. The confirmation and recovery periods are time-based in Better Stack and counted in checks here: one more than the period divided by the interval, rounded up, capped at 10 — a zero confirmation period means the first failure marks the monitor down, and an export without one keeps the default of two failing checks. Paused monitors are created disabled. Request headers carry over, credential-named ones into the encrypted `secret_headers`; `auth_username` and `auth_password` become basic auth. Disabled SSL verification, `follow_redirects: false`, and `ssl_expiration` (as `tls_min_days_valid`) carry over on HTTPS monitors.",
        },
        {
          type: "callout",
          tone: "warning",
          title: "Not migrated",
          text:
            "Regions, notification settings (call, SMS, email, push, critical alerts), escalation policies, team wait, and maintenance windows are ignored, each with one note for the whole file — assign locations and alert channels after import. The IPv4/IPv6-only setting is dropped with a note. A monitor whose basic-auth username arrives without its password (or with one redacted to asterisks) is created disabled with a note to re-enter it.",
        },
        {
          type: "callout",
          tone: "info",
          title: "Verified against documentation only",
          text:
            "This importer was built from Better Stack's API documentation, not live exports. After importing, check: keyword monitors (the case rule is not documented, so a required keyword must match its case and a forbidden one is refused in any case); DNS monitors (imported as an A lookup of `request_body` on the server in `url`, with `required_keyword` as an expected answer); non-HTTP timeouts; confirmation periods where the export had none; and basic auth, in case the export did not carry `auth_password`.",
        },
      ],
    },
    {
      id: "import-uptimerobot",
      title: "Migrating from UptimeRobot",
      blocks: [
        {
          type: "paragraph",
          text:
            "UptimeRobot's dashboard has no full monitor export. Call API v2 `getMonitors` with a read-only API key, asking for the optional details, and upload the JSON response. A response from the newer API (`{\"data\": [{\"friendlyName\": ...}]}`) is refused rather than guessed at. A response holds at most 50 monitors: fetch further pages with `offset` and import each file (preview warns when the response's total is larger than what the file holds).",
        },
        {
          type: "code",
          language: "bash",
          title: "Export from UptimeRobot",
          code: 'curl -s -X POST https://api.uptimerobot.com/v2/getMonitors \\\n  -d "api_key=$UPTIMEROBOT_API_KEY" -d format=json -d ssl=1 \\\n  -d custom_http_headers=1 -d custom_http_statuses=1 -d http_request_details=true \\\n  -d alert_contacts=1 -d mwindows=1 > uptimerobot.json',
        },
        {
          type: "table",
          columns: ["UptimeRobot type", "Imported as"],
          rows: [
            ["1 (HTTP)", "HTTP monitor"],
            ["2 (Keyword)", "HTTP monitor with a body assertion: \"alert when exists\" becomes `not_contains`, \"alert when not exists\" becomes `contains`, case-insensitive unless the monitor was case-sensitive (without `keyword_case_type`, the stricter reading: a required keyword matches its case, a forbidden one any case)"],
            ["3 (Ping)", "Ping monitor"],
            ["4 (Port)", "TCP monitor on the predefined port (HTTP, HTTPS, FTP, SMTP, POP3, IMAP) or the custom one"],
            ["5 (Heartbeat)", "Push monitor expecting a check-in every interval, with a new push URL (an interval over a day moves its excess into the grace period, keeping the deadline)"],
            ["Any other type", "Not imported, with the reason"],
          ],
        },
        {
          type: "paragraph",
          text:
            "The interval and timeout carry over and paused monitors (status 0) are created disabled. The HTTP method, POST body (raw text as is, key-value data as a JSON object) and custom headers carry over, credential-named headers into the encrypted `secret_headers`; HTTP basic auth becomes `basic_auth_username` and the encrypted `basic_auth_password`, while a monitor using digest authentication is not imported. Custom HTTP statuses apply on top of UptimeRobot's default, where 2xx and 3xx are up. Ignored SSL errors become `tls_skip_verify`, and SSL expiry reminders become `tls_min_days_valid` of 30 days, a TLS-expiry alert while the check keeps passing.",
        },
        {
          type: "callout",
          tone: "warning",
          title: "Not migrated",
          text:
            "Alert contacts and maintenance windows are ignored, each with one note for the whole file. UptimeRobot exposes no failure confirmation setting, so monitors get the default of two consecutive failing checks. Domain expiry reminders are not translated: add domain monitors by hand. A monitor whose basic-auth username arrives without its password (or with one redacted to asterisks) is created disabled with a note to re-enter it.",
        },
        {
          type: "callout",
          tone: "info",
          title: "Verified against documentation only",
          text:
            "This importer was built from UptimeRobot's API v2 documentation, not live exports. After importing, check keyword monitors (which `keyword_type` value means \"alert when present\", and the direction of `keyword_case_type`), that basic-auth passwords arrived in plain text rather than redacted, and the SSL settings, which come from the `ssl` object `getMonitors` returns with `ssl=1`.",
        },
      ],
    },
    {
      id: "import-gatus",
      title: "Migrating from Gatus",
      blocks: [
        {
          type: "paragraph",
          text:
            "Upload Gatus's `config.yaml`. When Gatus reads a configuration directory, merge the files' `endpoints` and `external-endpoints` lists into one file first. Each endpoint becomes a monitor by its URL scheme, each `group` becomes a group (two endpoints with one name in different groups become `group / name`), and `enabled: false` creates the monitor disabled.",
        },
        {
          type: "table",
          columns: ["Gatus endpoint", "Imported as"],
          rows: [
            ["`http://`, `https://`", "HTTP monitor; `graphql: true` posts the body as `{\"query\": ...}`"],
            ["`icmp://`", "Ping monitor"],
            ["`tcp://`, `tls://`", "TCP monitor, with a TLS handshake for `tls://`"],
            ["`ws://`, `wss://`", "WebSocket monitor sending the body as its message"],
            ["`grpc://`, `grpcs://`", "gRPC health probe (`grpc.health.v1`), with a note to confirm the endpoint serves it"],
            ["Endpoint with a `dns` block", "DNS monitor querying the server in `url` (A, AAAA, CNAME, TXT, MX, NS)"],
            ["External endpoint with `heartbeat.interval`", "Push monitor with a new push URL (an interval over a day moves its excess into the grace period, keeping the deadline); without a heartbeat it is not imported"],
            ["`udp://`, `sctp://`, `starttls://`, `ssh://`", "Not imported, with the reason"],
          ],
        },
        {
          type: "table",
          columns: ["Condition", "Translation"],
          rows: [
            ["`[STATUS] == 200`, `== any(200, 201)`, `== pat(2*)`, `!=`, `<`, `>=`, ...", "Expected status codes and ranges, the intersection of every status condition. An HTTP endpoint without one accepts every status, as in Gatus"],
            ["`[RESPONSE_TIME] < 300`", "`max_latency_ms` on HTTP and WebSocket, `max_rtt_ms` (average of the echoes) on ping"],
            ["`[BODY] == value`, `pat(...)`, `any(...)`, `!=`", "A body assertion (HTTP, ignoring surrounding whitespace for a plain value), the expected substring of the first message (WebSocket), or an expected answer (DNS). `pat()` is translated with `*` wildcards only; any other pattern syntax is dropped with a note"],
            ["`[BODY].path` compared with `==`, `!=`, `<`, `<=`, `>`, `>=`", "A JSON assertion on the same path (`[0]` becomes `.0`; `true`/`false` a boolean check; `pat` and `any` a regex)"],
            ["`len([BODY].items)`, `has([BODY].id) == true`", "The array length (`items.#`) and an `exists` assertion"],
            ["`[CERTIFICATE_EXPIRATION] > 48h`", "`tls_min_days_valid`, in days rounded up with a note (so `> 12h` is one day; `> 0` is zero, an alert once expired), on HTTPS and `tls://` monitors"],
            ["`[DOMAIN_EXPIRATION] > 720h`", "A separate domain monitor for the registrable domain, rounded up the same way"],
            ["`[CONNECTED] == true`, `[DNS_RCODE] == NOERROR`, gRPC `[BODY].status == SERVING`", "Already what the check requires"],
            ["`[CONNECTED] == false`, any other `[DNS_RCODE]`, status conditions no code satisfies", "The endpoint is not imported: it expects failure, and importing it would alert backwards"],
            ["Anything else (`[IP]`, `has(...) == false`, a response time on TCP, ...)", "Dropped, with a note on the monitor naming the condition"],
          ],
        },
        {
          type: "paragraph",
          text:
            "`interval` and `client.timeout` (Gatus's default 10 s) carry over. Headers carry over, credential-named ones into the encrypted `secret_headers`, and `user:password@` in a URL becomes basic auth. `client.insecure` becomes `tls_skip_verify` and `client.ignore-redirect` stops following redirects. An endpoint's alert `failure-threshold` and `success-threshold` (else the provider's `default-alert`, else Gatus's 3 and 2) become its consecutive-failure and recovery thresholds, taking the lowest across its enabled alerts.",
        },
        {
          type: "callout",
          tone: "warning",
          title: "Behavior changes and what is not migrated",
          text:
            "Gatus fails an endpoint once a certificate or domain is inside its expiration bound; here those raise TLS-expiry and domain-expiry alerts while the check keeps passing. With `client.insecure`, an expired certificate no longer fails the check at all — only the TLS-expiry alert reports it — and the monitor carries a warning saying so. Environment variables (`$VAR`, `${VAR}`) are never resolved: an endpoint whose host is one is not imported, and one using them elsewhere — URL, headers, body, DNS query, or a condition (which is dropped) — keeps them verbatim and is created disabled with a note naming them. `$$`, Gatus's escape for a literal `$`, becomes `$`. Alerting providers and maintenance windows are ignored with one note for the file. OAuth2, Identity-Aware Proxy, and client-certificate authentication have no equivalent, so those monitors are created disabled; a custom DNS resolver, network, and proxy are dropped with a note.",
        },
        {
          type: "callout",
          tone: "info",
          title: "Verified against documentation only",
          text:
            "This importer was built from Gatus's documentation, not a running instance. After importing, check: `pat()` conditions (translated as `*` wildcards), whole-body `[BODY] ==` conditions (surrounding whitespace is ignored), `grpc://` endpoints (imported as health probes), and DNS endpoints with a `[BODY]` condition (the answer must be among the records returned).",
        },
      ],
    },
    {
      id: "export",
      title: "Export monitors",
      blocks: [
        {
          type: "paragraph",
          text:
            "The monitor export endpoint produces a versioned YAML bundle with monitor configuration (secrets and URL credentials masked), tags, retry thresholds, and group membership names; when any monitor has a config-as-code key it is version 2, which adds each key and, by key, the keyed members and dependencies (otherwise version 1, which older releases import). `keyed=true` (the Export dialog's \"Monitors with a key\") exports only keyed monitors, the file [apply](/docs/monitors/#config-as-code) manages. Import reads versions 1 and 2, does not set keys, and leaves masked URL credentials out with a preview warning. Exports no longer write `alert_policy_names`; a bundle from an older release that still carries the field imports normally and the field is ignored. Export only produces this portable bundle — there is no Uptime Kuma-direction export.",
        },
        {
          type: "callout",
          tone: "warning",
          title: "Export is not a secret backup or universal round trip",
          text:
            "Protected values returned by the API are masked, so the bundle never contains usable credentials. Treat export as reviewed configuration material: store it securely, inspect import preview, and re-enter secrets through designated fields.",
        },
      ],
    },
    {
      id: "admin-checklist",
      title: "Administration checklist",
      blocks: [
        {
          type: "list",
          items: [
            "Keep at least two controlled superadministrator recovery paths.",
            "Grant tenant `editor` or `viewer` by default and reserve `admin` for governance tasks.",
            "Issue integration-specific, expiring API keys with the minimum scope.",
            "Test OIDC login and local/recovery login after every proxy, issuer, or public-origin change.",
            "Review audit events, inactive users, stale API keys, and tenant memberships on a schedule.",
            "Define both telemetry and audit retention intentionally.",
            "Treat AI hypotheses as advisory and protect the installation-owned local-service token.",
            "Always preview monitor imports and test imported monitors before relying on them for production alerts.",
          ],
        },
      ],
    },
  ],
};
