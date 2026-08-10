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
            "Administrator access tokens default to a short lifetime (15 minutes in the standard configuration), while refresh tokens default to 30 days and rotate on use. A rotated refresh token stays valid for a 60-second reuse window so concurrent refreshes from parallel requests or multiple tabs do not end the session. Disabled users are checked against database state on authenticated requests.",
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
            "On an empty installation, the public bootstrap status and first-user endpoints allow exactly the initial account to be created. That first active account is a superadministrator. OIDC just-in-time login can also initialize the first superadministrator on an empty installation.",
        },
        {
          type: "table",
          columns: ["User variable", "Validation and behavior"],
          rows: [
            [
              "`username`",
              "3–64 characters; letters, numbers, `.`, `_`, and `-`",
            ],
            ["`email`", "Optional, used for OIDC linking and SSO-only users"],
            ["`password`", "12–128 characters when local login is enabled"],
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
            "A user with email and no password can be provisioned as SSO-only. Superadministrator-only user routes create, list, update, and delete accounts. Safeguards prevent deleting the current user or leaving the installation without an administrator.",
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
            "API keys ignore arbitrary tenant selection and remain pinned to their creation tenant. The tenant-list endpoint is for administrator sessions and returns only allowed tenant context. Public tenant create/update/delete management is not exposed through the normal product API.",
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
            "On an empty installation, the first successful JIT login becomes the initial superadministrator.",
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
            "Superadmins can map identity-provider groups to roles under Settings → OIDC group mappings (or via `/api/v1/oidc-group-mappings`). A mapping targets either one tenant with a role (`admin`, `editor`, `viewer`) or the platform (grants `superadmin`). Groups are read from the ID-token claim named by [`OIDC_GROUPS_CLAIM`](/docs/configuration/#oidc); request the `groups` scope via `OIDC_SCOPES` so the provider sends it.",
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
              "`0` keeps telemetry indefinitely; otherwise 30–3,650 days",
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
            "[Retention](/docs/operations/#retention) applies to monitoring telemetry such as check results, mesh history, and rollups. Audit retention is configured separately at platform level. Dashboard group tags change presentation only; they do not create monitor groups or notification rollup.",
        },
      ],
    },
    {
      id: "ai-settings",
      title: "Tenant AI settings",
      blocks: [
        {
          type: "table",
          columns: ["Variable", "Meaning"],
          rows: [
            ["`enabled`", "Permit tenant AI features"],
            [
              "`provider`",
              "Provider identifier; current default is `openai_compat`",
            ],
            [
              "`base_url`",
              "OpenAI-compatible API base endpoint",
            ],
            ["`model`", "Model name sent to the provider"],
            ["`json_mode`", "Request structured JSON output where supported"],
            ["`max_tokens`", "Response token limit"],
            ["`timeout_seconds`", "Provider request timeout"],
            [
              "`api_key`",
              "Write-only secret: omit to keep, send empty to clear, send a value to replace",
            ],
            [
              "`has_api_key`",
              "Read-time boolean indicating whether tenant secret material exists",
            ],
          ],
        },
        {
          type: "paragraph",
          text:
            "Tenant settings override effective environment fallback configuration when present. The API key is encrypted at rest when the [platform master encryption key](/docs/security/#encryption-at-rest) is configured. The connection-test action is compute-only and can be run without saving a new monitor or incident.",
        },
        {
          type: "paragraph",
          text:
            "AI configuration enables dependency suggestions and asynchronous incident analysis. It does not automatically apply suggested graph edges or remediation steps.",
        },
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
          ],
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
            "Monitor imports support JSON, YAML, and CSV with format autodetection. JSON/YAML can contain an array, a single object, common wrappers such as `items`, `monitors`, or `data`, and a versioned portable YAML bundle. YAML also recognizes common source wrappers such as `services`, `endpoints`, and `checks`.",
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
      id: "export",
      title: "Export monitors",
      blocks: [
        {
          type: "paragraph",
          text:
            "The monitor export endpoint produces a versioned YAML bundle with monitor configuration, tags, and group membership names. Compatibility data can include legacy alert-policy names, but policy management is retired.",
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
            "Treat AI output as advisory and protect provider API keys with platform secret encryption.",
            "Always preview monitor imports and test imported monitors before relying on them for production alerts.",
          ],
        },
      ],
    },
  ],
};
