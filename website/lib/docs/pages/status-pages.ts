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
    "status badge",
    "Atom feed",
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
            [
              "`csp_mode`",
              "The page's [Content-Security-Policy](#content-security-policy): `strict`, `compatible`, `off`, or empty for automatic. Set in the template editor's Quick branding panel",
            ],
            [
              "`seo_title`",
              "The page's `<title>`, `og:title` and `twitter:title`, up to 120 characters. Empty uses `<title> · Status`. See [search and link previews](#search-and-previews)",
            ],
            [
              "`seo_description`",
              "The meta, Open Graph and Twitter description, up to 300 characters. Empty uses the page description, else a fixed sentence naming the page",
            ],
            [
              "`allow_search_indexing`",
              "On by default. Off sends `noindex, nofollow` in a robots meta tag and an `X-Robots-Tag` header; the page stays public",
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
            [
              "`GET /public/status/{slug}/feed.atom`",
              "Atom feed of published incidents and listed maintenance; see [feed, summary and badges](#feed-summary-badges)",
            ],
            [
              "`GET /public/status/{slug}/summary.json`",
              "Versioned machine-readable summary of the page",
            ],
            [
              "`GET /public/status/{slug}/badge.svg`",
              "Overall status badge",
            ],
            [
              "`GET /public/status/{slug}/monitors/{id}/badge.svg` and `…/uptime.svg`",
              "Status and uptime badges of a component the page shows",
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
      id: "search-and-previews",
      title: "Search and link previews",
      blocks: [
        {
          type: "paragraph",
          text:
            "The built-in head carries a `<title>`, a meta description, a robots meta tag, Open Graph tags (`og:type`, `og:site_name`, `og:title`, `og:description`, and `og:image` when the page logo is an absolute `http`/`https` URL) and a `summary` Twitter card. With `STATUS_PAGE_BASE_URL` set on the status service it also carries a canonical link and `og:url`; without it they are left out rather than guessed. Two `<link rel=\"alternate\">` tags point feed readers and tools at the page's Atom feed and `summary.json`.",
        },
        {
          type: "paragraph",
          text:
            "The default description never states the current status. Search engines and link previews keep a snapshot for days, and \"All systems operational\" frozen in a search result during an outage misleads more than it helps; the page title already says what the page is. Set `seo_description` to describe your services in your own words.",
        },
        {
          type: "paragraph",
          text:
            "Custom templates receive the same values as `.SEOTitle`, `.MetaDescription`, `.SiteName`, `.CanonicalURL` (empty without a public origin), `.OGImageURL` (empty unless the logo is absolute), `.Robots` (`index, follow` or `noindex, nofollow`), `.FeedURL` and `.SummaryURL`. A template that prints no robots tag is still covered: with indexing off every page response carries `X-Robots-Tag: noindex, nofollow`.",
        },
      ],
    },
    {
      id: "feed-summary-badges",
      title: "Feed, summary and badges",
      blocks: [
        {
          type: "paragraph",
          text:
            "Each page publishes three read-only add-ons beside it. They show nothing the page itself does not: the same rendered components under their public names, the same published incidents and the same maintenance list. They are readable from any site (`Access-Control-Allow-Origin: *`, never with credentials), carry an `ETag` and answer `If-None-Match` with `304`, are sent with `X-Content-Type-Options: nosniff`, a `default-src 'none'` Content-Security-Policy and `X-Robots-Tag: noindex`, and return `404` for an unknown page.",
        },
        {
          type: "table",
          columns: ["Add-on", "Content and caching"],
          rows: [
            [
              "`feed.atom`",
              "Atom 1.0 (`application/atom+xml`). One entry per published incident, the 50 most recently changed that are unresolved or changed in the last 90 days, each quoting its five newest public updates; unpublished incidents and internal notes never appear. One entry per maintenance window the page lists, at its occurrence in progress or next one. Entry ids are stable: an incident keeps its id through every update, and each occurrence of a repeating window is its own entry. Text is plain (`type=\"text\"`), so incident wording never becomes markup in a reader. `Cache-Control: public, max-age=300`",
            ],
            [
              "`summary.json`",
              "The page summary described below. Shares the page's render, so it is at most as stale as the page (the render cache TTL, `STATUS_PAGE_CACHE_TTL`). `Cache-Control: public, max-age=60`",
            ],
            [
              "`badge.svg`",
              "Overall status: `operational`, `maintenance`, or on an \"Issues detected\" page the worst component state (`outage`, `degraded`, `unknown`). `Cache-Control: public, max-age=60`",
            ],
            [
              "`monitors/{id}/badge.svg`",
              "A component's status: `operational`, `degraded`, `down`, `maintenance` or `unknown`. `{id}` is the monitor id; a monitor the page does not render has no badge (`404`), including the members of a group the page shows only as the group",
            ],
            [
              "`monitors/{id}/uptime.svg?range=30d`",
              "A component's uptime over `1h`, `24h`, `7d`, `30d` (default) or `90d`, computed and rounded exactly as the page shows it. `404` when the page hides per-component uptime (`show_monitor_uptime` off)",
            ],
          ],
        },
        {
          type: "paragraph",
          text:
            "Badges are SVG images measured on the server with fixed 11 px text, so they keep their size whatever font the viewer has, and use mid-tone colours that read on light and dark backgrounds. `?style=flat` (default, rounded) or `?style=square` is the only style parameter; the label is always the component's public name or `status`, never caller text. The operator UI's Share panel on each page's settings builds the URLs and Markdown/HTML snippets.",
        },
        {
          type: "paragraph",
          text:
            "`summary.json` (`schema_version` 1) is a stable contract: fields are only added, and a removal or change of meaning bumps the version. Its fields:",
        },
        {
          type: "table",
          columns: ["Field", "Meaning"],
          rows: [
            ["`schema_version`", "Contract version, currently `1`"],
            [
              "`page`",
              "`slug`, `title`, and `url` (the canonical URL; omitted without `STATUS_PAGE_BASE_URL`)",
            ],
            [
              "`status`",
              "`indicator` (`operational`, `issues` or `maintenance`, the page headline's rule), `description` (the headline text) and `counts` of components: `total`, `operational`, `degraded`, `down`, `maintenance`, `unknown`",
            ],
            [
              "`uptime`",
              "Page-level availability in percent for `1h`, `24h`, `7d`, `30d` and `90d` (each `null` without data), or `null` when the page hides its uptime summary (`show_global_uptime` off)",
            ],
            [
              "`sections[]`",
              "`title`, `status` (worst component) and `components[]`, each with `id`, `name`, `status` and `uptime` (the same shape, or `null` when `show_monitor_uptime` is off)",
            ],
            [
              "`incidents[]`",
              "Unresolved published incidents: `id`, `title`, `state`, `published_at`, `affected_components` and `latest_update` (`message`, `created_at`, or `null`)",
            ],
            [
              "`maintenance[]`",
              "Listed windows: `title`, `starts_at`, `ends_at`, `in_progress`, `repeating`, `next_starts_at` and `affected_components`",
            ],
            ["`generated_at`", "When the page data was read (RFC 3339 UTC)"],
          ],
        },
        {
          type: "callout",
          tone: "warning",
          title: "Shared caches may hold an add-on for its max-age",
          text:
            "A proxy or image cache (for example a README image proxy) may keep serving a badge or summary for up to 60 seconds, and a feed for up to 5 minutes, after the page changes. Plan around that delay when you remove a component or change what a page shows.",
        },
      ],
    },
    {
      id: "browser-notifications",
      title: "Visitor browser notifications",
      blocks: [
        {
          type: "paragraph",
          text:
            "A visitor can opt into browser push notifications for a status page. They are told when a component the page shows goes down, and again when it comes back up — including when the tab and the browser are closed. This is real Web Push (a service worker plus a VAPID keypair), not a notification raised by the open page.",
        },
        {
          type: "paragraph",
          text:
            "Two things must both be true for the control to appear: the page sets `enable_push_notifications`, and the deployment configures `STATUS_PAGE_VAPID_PUBLIC_KEY` and `STATUS_PAGE_VAPID_PRIVATE_KEY`. Either alone renders nothing, because either alone would produce a button that cannot work.",
        },
        {
          type: "table",
          columns: ["Transition", "Notification"],
          rows: [
            ["A component enters `down`", "Sent"],
            ["A component returns to `up` from `down`", "Sent"],
            ["`degraded`, in either direction", "Not sent — some but not enough locations are failing, so the component is still up for most visitors"],
            ["`suspect`", "Not sent — mid-confirmation, by design not yet an outage"],
            ["`unknown`, or a paused monitor", "Not sent"],
            ["Any monitor inside an active maintenance window", "Not sent"],
          ],
        },
        {
          type: "paragraph",
          text:
            "Intermediate states are skipped rather than reported, so `up → suspect → down` is one notification, `down → suspect → down` is none, and `down → degraded → up` is a single recovery. A transition that reverses within about a minute never notifies at all.",
        },
        {
          type: "callout",
          tone: "warning",
          title: "Group components do not notify on their own",
          text:
            "A group monitor's state is derived by the alerter rather than written by the state machine, so it produces no state-transition record. A group shown on a page never raises a notification for itself; its member monitors do, and the notification names the member as the page labels it.",
        },
        {
          type: "list",
          items: [
            "Notifications require a secure context. Localhost counts, so development works, but a plain-HTTP deployment shows no control at all.",
            "On iPhone and iPad, Web Push only works for a page added to the Home Screen. The control says so rather than failing silently in an ordinary Safari tab.",
            "Turning the setting off stops new notifications but keeps existing subscriptions, so re-enabling does not force everyone to opt in again.",
            "Rotating the VAPID keypair invalidates every stored subscription — push services bind an endpoint to the key that created it — and every visitor must opt in again.",
            "A page rendered by a custom template gets no control unless the author adds one; `.PushEnabled` and `.PushPublicKey` are available in the template context.",
            "Subscriptions store only the opaque browser endpoint and its public keys. No visitor identity, email address, or visit history is recorded.",
          ],
        },
        {
          type: "paragraph",
          text:
            "Delivery is driven by the recorded state timeline rather than by live events, so a notification is not lost when a message is dropped or a replica restarts. Transitions older than about fifteen minutes are discarded instead of delivered late: a notification saying a service is down, arriving after it recovered, is worse than none.",
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
            "The active built-in view shows current state and the configured monitor/global uptime summaries. It supports ordered sections, active/recent incidents, and maintenance. Group and passive monitors present derived or reported state rather than an active network-check result. A group's status comes from its members' monitor states, not their latest check rows: it shows down only when every member with a verdict is down, degraded when some are down or degraded, and a paused member does not count.",
        },
        {
          type: "paragraph",
          text:
            "Monitor state uses the same `unknown`, `up`, `suspect`, `down`, and `degraded` model as the authenticated application. Template authors should preserve distinct labels rather than mapping every non-up state to a single outage color.",
        },
        {
          type: "paragraph",
          text:
            "Visitors can switch among list, compact, and kiosk presentation modes, select an uptime range (`1h`, `24h`, `7d`, `30d` or `90d`; `24h` by default, kept in the `?range=` URL parameter), search monitors, filter by status, and use keyboard/URL preferences supported by the shared template. Those visitor controls are distinct from saved page settings. Kiosk mode auto-fits tile density to the screen so every monitor stays visible without scrolling on wall displays — tiles progressively shed detail (metrics, then meta line and uptime strip) as the count grows, with the hidden detail available on hover; only past the smallest tier (roughly 500+ monitors on a 1080p display) does the grid fall back to scrolling.",
        },
        {
          type: "paragraph",
          text:
            "The `1h` range is computed from raw check results in one-minute buckets: the page-level and expanded strips draw one cell per minute, a monitor row one cell per two minutes. `24h` uses hourly buckets from the hourly rollups plus the not-yet-rolled-up tail, and `7d` and longer use daily rollups. A minute or hour with no checks is drawn as an empty \"No data\" cell, so a monitor that checks less often than a cell spans shows gaps between its checks at `1h`. At `1h` and `24h`, group rows and the page-level strip pool every member's checks per bucket, so a monitor that checks more often weighs more.",
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
            "Public page data includes applicable maintenance with an occurrence in progress or starting within the next 14 days. A window is applicable when it directly covers a selected monitor or covers a selected group and its members. Each window appears once, at its occurrence in progress or its next one, never at the date its schedule was first saved; a skipped occurrence is not shown. A repeating window is labelled \"Repeats\" with up to three following starts. The page is rendered with times in UTC; the built-in template's script then shows them in the visitor's local time with the UTC times alongside, and visitors without scripts keep the UTC text. Custom templates read the lines as `TimeRangeText` and `RepeatText` on each `MaintenanceWindows` entry, plus `StartsAtISO`, `EndsAtISO` and `NextStartsISO` (space-separated) in RFC 3339 UTC for their own formatting.",
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
              "Number of cells in an expanded monitor strip for a range name (`\"1h\"`, `\"24h\"`, `\"7d\"`, `\"30d\"`, `\"90d\"`)",
            ],
            [
              "`globalStripCells`",
              "Number of cells in the page-level uptime strip for a range name",
            ],
            ["`join`", "Join string values for display"],
            [
              "`monitorStripCells`",
              "Number of cells in a monitor row or kiosk tile strip for a range name",
            ],
            ["`typeIcon`", "Return the display icon for a monitor type"],
            [
              "`cspNonce`",
              "The current response's [CSP](#content-security-policy) nonce: write `<script nonce=\"{{cspNonce}}\">` so an inline script runs under the `strict` policy",
            ],
          ],
        },
        {
          type: "paragraph",
          text:
            "Each range has its own strip data (a JSON array of `{label, uptime}` cells, `uptime` -1 for no data, already resampled to the cell count the functions return) and uptime figure. The page exposes `.GlobalUptime1hJSON`, `.GlobalUptime24JSON`, `.GlobalUptime7JSON`, `.GlobalUptime30JSON` and `.GlobalUptime90JSON` with matching `…Value` strings. Each monitor exposes `.History1hJSON`, `.History24hJSON`, `.History7dJSON`, `.History30dJSON` and `.History90dJSON` for its row, `.ExpandedHistory1hJSON`, `.ExpandedHistory30dJSON` and `.ExpandedHistory90dJSON` for its expanded strip, and `.Uptime1hValue` through `.Uptime90Value`. The `1h` fields were added later; a template written before them simply offers no `1h` range.",
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
      id: "content-security-policy",
      title: "Content-Security-Policy",
      blocks: [
        {
          type: "paragraph",
          text:
            "Every rendered page carries an enforced `Content-Security-Policy` with a fresh script nonce per response. The cached render keeps a placeholder that is swapped for each response's nonce, so caching and `ETag` revalidation still work; a `304` carries no policy, so the browser keeps the header that matches its cached body. The policy depends on the page's `csp_mode`:",
        },
        {
          type: "table",
          columns: ["Mode", "What the page may load"],
          rows: [
            [
              "`strict`",
              "Only `<script>` elements carrying `nonce=\"{{cspNonce}}\"` run, so inline event handlers and unmarked scripts do not. Stylesheets and fonts may also come from any `https:` origin (custom CSS `@import` and `@font-face`), and images from `https:` or `http:` (a remote logo) — none of them can run code. Connections and the push service worker stay on the page's own origin. No plugins, no `<base>` rewriting.",
            ],
            [
              "`compatible`",
              "Inline scripts and styles, plus scripts, styles, fonts, images, frames and connections from any `https:` or `http:` origin. For pages whose custom HTML or CSS loads analytics, chat widgets or web fonts. On an HTTPS page the browser still refuses `http:` scripts as mixed content.",
            ],
            ["`off`", "No resource policy; only `frame-ancestors` is sent."],
            [
              "empty (automatic)",
              "`compatible` while the page has custom CSS, custom head/footer HTML or a published custom template, `strict` otherwise. Customized pages keep working without edits, and an uncustomized page's remote logo still renders under `strict`.",
            ],
          ],
        },
        {
          type: "paragraph",
          text:
            "In every mode `frame-ancestors` admits only the page's own origin and the operator UI origin (`APP_BASE_URL` on the status service), which the template editor needs to preview the page in an iframe. Other sites can no longer frame a status page; link to it instead. The draft preview route admits any ancestor while `APP_BASE_URL` is unset, so the editor works from whatever host serves the UI.",
        },
        {
          type: "paragraph",
          text:
            "The built-in template's inline script carries the nonce. To move a customized page to `strict`, add `nonce=\"{{cspNonce}}\"` to every `<script>` in a custom template; custom head and footer HTML may contain the same literal `{{cspNonce}}`, which is replaced too. Then set `csp_mode` to `strict` and check the browser console on the preview.",
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
