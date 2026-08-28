import type { DocPage } from "../types";

export const STATUS_PAGES_PAGE: DocPage = {
  slug: "status-pages",
  group: "Use Probara",
  title: "Status pages",
  description:
    "Publish branded, live public status pages with sections, incidents, maintenance, and versioned templates.",
  eyebrow: "Public communication",
  readingTime: "24 min read",
  keywords: [
    "status page",
    "public incidents",
    "status page template",
    "SSE",
    "custom CSS",
  ],
  sections: [
    {
      id: "page-model",
      title: "Status page model",
      blocks: [
        {
          type: "table",
          columns: ["Variable", "Purpose and validation"],
          rows: [
            [
              "`slug`",
              "Public URL identifier using lowercase letters, numbers, and hyphens",
            ],
            ["`title`", "Required public page title"],
            ["`description`", "Optional public summary"],
            ["`logo_url`", "Optional brand logo URL"],
            [
              "`primary_color` / `secondary_color`",
              "Six-digit colors in exact `#RRGGBB` form",
            ],
            [
              "`monitor_ids`",
              "Monitors exposed on the page when using the flat layout",
            ],
            [
              "`display_names`",
              "Per-monitor public labels, each up to 80 characters",
            ],
            [
              "`sections`",
              "Ordered groups with titles up to 80 characters and ordered monitor entries",
            ],
          ],
        },
        {
          type: "paragraph",
          text:
            "Each monitor can appear only once across a page's sections. A section entry can override its public display name and stores an explicit position so the presentation order is stable.",
        },
        {
          type: "callout",
          tone: "warning",
          title: "A public page exposes operational information",
          text:
            "Only select monitors, names, descriptions, incidents, and maintenance details intended for unauthenticated visitors. The active built-in template does not currently render monitor target URLs, but custom templates and future use of schema-reserved fields must still be reviewed for disclosure.",
        },
      ],
    },
    {
      id: "presentation-settings",
      title: "Presentation and disclosure settings",
      blocks: [
        {
          type: "table",
          columns: ["Setting", "Current built-in behavior"],
          rows: [
            [
              "`show_monitor_tags`",
              "Present in the settings schema; not currently wired into the built-in template",
            ],
            [
              "`show_monitor_url`",
              "Present in the settings schema; not currently wired into the built-in template",
            ],
            ["`show_monitor_uptime`", "Controls per-monitor uptime in the built-in view"],
            [
              "`show_monitor_tls`",
              "Present in the settings schema; not currently wired into the built-in template",
            ],
            [
              "`show_latency_charts`",
              "Present in the settings schema; not currently wired into the built-in template",
            ],
            [
              "`show_agent_metrics`",
              "Present in the settings schema; not currently wired into the built-in template",
            ],
            [
              "`show_global_uptime`",
              "Controls the page-level uptime summary in the built-in view",
            ],
            ["`show_footer`", "Controls the built-in footer region"],
            ["`footer_text`", "Built-in custom footer text, up to 250 characters"],
            ["`default_theme`", "Initial built-in theme: `light` or `dark`"],
            ["`allow_theme_toggle`", "Allows the built-in visitor theme switch"],
            [
              "`enable_push_notifications`",
              "Offers visitors a browser-notification opt-in. Off by default, and inert unless the deployment also configures a VAPID keypair — see [visitor notifications](#browser-notifications)",
            ],
            ["`custom_css`", "Page-scoped custom stylesheet, up to 128 KiB"],
            [
              "`custom_head_html`",
              "Trusted custom head markup, up to 64 KiB. Injected after the built-in head, so a `<link rel=\"icon\">` here replaces the default monochrome favicon",
            ],
            [
              "`custom_footer_html`",
              "Trusted custom footer markup, up to 64 KiB",
            ],
          ],
        },
        {
          type: "paragraph",
          text:
            "Settings are part of the page definition and can be supplied at creation or update. Several visibility fields are reserved in the schema but are not yet consumed by the active built-in renderer; storing them does not make those panels appear. Custom template authors must verify which data is present in the current template context.",
        },
        {
          type: "paragraph",
          text:
            "Separately from these settings, the built-in template automatically shows a small \"Certificate expires soon\" note on a component whose monitor has an open [`tls_expiry` alert](/docs/alerting/#host-and-mesh) — the certificate is inside its `tls_min_days_valid` window while the component itself remains operational, so the note never changes the component's status. Custom templates receive this as the boolean `CertExpiresSoon` field on each monitor entry.",
        },
        {
          type: "callout",
          tone: "warning",
          title: "Custom HTML is trusted author content",
          text:
            "The template engine escapes ordinary data values, but custom head/footer HTML and template source are intentionally authored markup. Restrict status-page editing to trusted tenant roles and review changes before publication.",
        },
      ],
    },
    {
      id: "public-routes",
      title: "Public routes and live updates",
      blocks: [
        {
          type: "table",
          columns: ["Route", "Response"],
          rows: [
            [
              "`GET /public/status/{slug}`",
              "Rendered public status page",
            ],
            [
              "`GET /public/status/{slug}/data`",
              "Public JSON-shaped page data used by the renderer and clients",
            ],
            [
              "`GET /public/status/{slug}/stream`",
              "Server-sent event stream for live refresh notifications",
            ],
            [
              "`GET /public/status/{slug}/preview/draft`",
              "Draft-template preview, protected by a preview token when configured",
            ],
            [
              "`GET /public/status/sw.js`",
              "Push service worker. Served from the parent path, not under a slug, so its scope covers the page URL; see [visitor notifications](#browser-notifications)",
            ],
            [
              "`POST /public/status/{slug}/push/subscribe`",
              "Stores a browser push subscription. Returns 404 unless the page enables notifications and the deployment has VAPID keys",
            ],
            [
              "`POST /public/status/{slug}/push/unsubscribe`",
              "Removes a browser push subscription. Always 204, so it cannot be used to probe which endpoints exist",
            ],
          ],
        },
        {
          type: "paragraph",
          text:
            "The status-page service caches rendered data and subscribes to `statuspage.updates` on core NATS. Monitor, incident, maintenance, or page changes invalidate relevant cache entries and notify connected SSE clients. The rendered page also performs a periodic fallback refresh, currently about every 60 seconds.",
        },
        {
          type: "callout",
          tone: "info",
          title: "Live delivery is eventually consistent",
          text:
            "Core NATS status messages are not durable. PostgreSQL/API data remains authoritative, and the fallback refresh repairs a missed invalidation without requiring the visitor to reload manually.",
        },
      ],
    },
    {
      id: "monitor-data",
      title: "Monitor data on a public page",
      blocks: [
        {
          type: "paragraph",
          text:
            "The active built-in view shows current state and the configured monitor/global uptime summaries. It supports ordered sections, active/recent incidents, and maintenance. Group and passive monitors present derived or reported state rather than an active network-check result.",
        },
        {
          type: "paragraph",
          text:
            "Monitor state uses the same `unknown`, `up`, `suspect`, `down`, and `degraded` model as the authenticated application. Template authors should preserve distinct labels rather than mapping every non-up state to a single outage color.",
        },
        {
          type: "paragraph",
          text:
            "Visitors can switch among list, compact, and kiosk presentation modes, select uptime ranges, search monitors, filter by status, and use keyboard/URL preferences supported by the shared template. Those visitor controls are distinct from saved page settings.",
        },
        {
          type: "callout",
          tone: "warning",
          title: "Platform-generated stale results are not uptime samples",
          text:
            "Agent and push [freshness failures](/docs/agents/#freshness) can change current public state, but platform-source results are excluded from standard [monitor analytics](/docs/monitors/#operations). Current status and historical uptime therefore answer related but not identical questions.",
        },
      ],
    },
    {
      id: "incidents",
      title: "Publish incidents",
      blocks: [
        {
          type: "list",
          ordered: true,
          items: [
            "Create or open an [incident](/docs/alerting/#incidents) in the authenticated application.",
            "Attach the affected monitors and relevant alerts.",
            "Add public timeline updates while keeping internal notes private.",
            "Publish the incident to one or more status pages.",
            "Continue status transitions and public updates through investigation, identification, monitoring, and resolution.",
            "Unpublish from an individual page when it is no longer relevant there.",
          ],
        },
        {
          type: "paragraph",
          text:
            "Publication is monitor-aware: only incident content associated with monitors selected on that status page is eligible for that page's public context. Merely attaching an incident to a page does not make unrelated internal monitors public.",
        },
        {
          type: "callout",
          tone: "info",
          title: "Public updates and internal notes are different timeline types",
          text:
            "Use public updates for subscriber-facing communication. Use internal notes for hypotheses, credentials-free diagnostic detail, and response coordination that should never render publicly.",
        },
      ],
    },
    {
      id: "maintenance",
      title: "Show maintenance",
      blocks: [
        {
          type: "paragraph",
          text:
            "Public page data includes applicable ongoing and recent maintenance windows. A window is applicable when it directly covers a selected monitor or covers a selected group and its members.",
        },
        {
          type: "paragraph",
          text:
            "Maintenance changes [alert behavior](/docs/alerting/#maintenance) but does not stop checks. A page can continue showing observed state while also explaining the scheduled work, which helps visitors distinguish planned degradation from an unannounced incident.",
        },
      ],
    },
    {
      id: "template-lifecycle",
      title: "Template lifecycle",
      intro:
        "Every page has a built-in default template and at most one editable draft. Publishing is versioned and previous published source is archived.",
      blocks: [
        {
          type: "table",
          columns: ["Action", "Effect"],
          rows: [
            ["Load default", "Returns the built-in template source"],
            [
              "Save draft",
              "Validates and stores one unpublished template without affecting visitors",
            ],
            ["Preview draft", "Renders draft source against current page data"],
            [
              "Publish",
              "Archives the current published template, promotes the draft, and increments the version",
            ],
            ["Discard draft", "Deletes only the draft"],
            [
              "Revert",
              "Copies an archived version into a new current published version; history remains append-only",
            ],
            [
              "Reset",
              "Archives the current published template and restores the built-in source as a new version",
            ],
          ],
        },
        {
          type: "paragraph",
          text:
            "Template source must be non-empty, parse as Go `html/template`, and remain within 512 KiB. Published versions can be listed and their source retrieved for review or rollback.",
        },
        {
          type: "callout",
          tone: "success",
          title: "Revert does not erase history",
          text:
            "A revert creates a new published version from old source. You retain an audit-friendly sequence instead of moving the version pointer backward.",
        },
      ],
    },
    {
      id: "template-authoring",
      title: "Author templates safely",
      blocks: [
        {
          type: "paragraph",
          text:
            "Templates use Go `html/template`, which contextually escapes ordinary data values. Use the structures exposed by the default template as the compatibility reference and preview with real page data before publication.",
        },
        {
          type: "table",
          columns: ["Template function", "Purpose"],
          rows: [
            [
              "`expandedStripCells`",
              "Build the expanded monitor strip cells expected by the current renderer",
            ],
            [
              "`globalStripCells`",
              "Build the global summary strip cells",
            ],
            ["`join`", "Join string values for display"],
            ["`monitorStripCells`", "Build standard monitor strip cells"],
            ["`typeIcon`", "Return the display icon for a monitor type"],
          ],
        },
        {
          type: "callout",
          tone: "warning",
          title: "Template functions are an append-only compatibility surface",
          text:
            "Existing function names are retained for published templates. Still, copy the current default source before a major redesign so your template follows the current page-data structure and accessibility conventions.",
        },
      ],
    },
    {
      id: "preview-security",
      title: "Protect draft previews",
      blocks: [
        {
          type: "paragraph",
          text:
            "When `STATUS_PAGE_PREVIEW_SECRET` is configured, draft preview URLs require a [signed preview token](/docs/security/#status-pages-artifacts). Without that secret, the development preview path is open to anyone who can reach it.",
        },
        {
          type: "callout",
          tone: "warning",
          title: "Configure preview signing outside local development",
          text:
            "Drafts may contain unreleased incident messaging, brand changes, or diagnostic markup. Set a strong [preview secret](/docs/configuration/#status-page-service) and restrict network exposure before using the preview route in a shared or production environment.",
        },
      ],
    },
    {
      id: "template-library",
      title: "Reusable template library",
      blocks: [
        {
          type: "paragraph",
          text:
            "Tenants can save validated template source as a named library entry. Names are limited to 100 characters. List responses expose metadata; source is retrieved from the dedicated source route.",
        },
        {
          type: "paragraph",
          text:
            "Applying a library template copies its source into the target page's draft workflow. It does not create a live link: later edits to the library entry do not silently change already published pages.",
        },
        {
          type: "callout",
          tone: "info",
          title: "Library entries are tenant-scoped",
          text:
            "A template created by one tenant is not a cross-tenant marketplace item. Duplicate or promote source through an explicit, reviewed process when standardizing multiple tenants.",
        },
      ],
    },
    {
      id: "release-checklist",
      title: "Publication checklist",
      blocks: [
        {
          type: "list",
          items: [
            "Verify the slug, public title, monitor selection, display names, and section order.",
            "Review built-in visibility settings and treat the URL, tag, agent-metric, TLS, and latency flags as schema-reserved until the renderer explicitly supports them.",
            "Preview light and dark themes at desktop and mobile widths.",
            "Exercise unknown, suspect, degraded, down, maintenance, and incident states in a safe preview environment.",
            "Check custom HTML/CSS for content security, accessibility, focus states, contrast, and responsive overflow.",
            "Configure the preview secret and confirm the public status service has a reachable API/data path.",
            "Publish, open the anonymous route in a clean browser session, and verify SSE/fallback refresh behavior.",
          ],
        },
      ],
    },
  ],
};
