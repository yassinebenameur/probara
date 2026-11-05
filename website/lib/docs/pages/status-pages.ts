import type { DocPage } from "../types";

export const STATUS_PAGES_PAGE: DocPage = {
  slug: "status-pages",
  group: "Use Probara",
  title: "Status pages",
  description:
    "Publish branded, live public status pages with sections, incidents, maintenance, and versioned templates.",
  eyebrow: "Public communication",
  readingTime: "28 min read",
  keywords: [
    "status page",
    "public incidents",
    "status page template",
    "SSE",
    "custom CSS",
    "status badge",
    "Atom feed",
    "embed widget",
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
            [
              "`visibility`",
              "Who may view the page: `public` (default), `unlisted`, `password` or `private`; see [visibility](#visibility)",
            ],
            [
              "`password`",
              "Write-only visitor password, 8 to 72 bytes. Omit to keep, `\"\"` to clear; required for `password` visibility. Reads report only `has_password`",
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
            "Unless its [visibility](#visibility) gates it, only select monitors, names, descriptions, incidents, and maintenance details intended for unauthenticated visitors. The active built-in template does not currently render monitor target URLs, but custom templates and future use of schema-reserved fields must still be reviewed for disclosure.",
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
              "Draws a [response-time chart](#monitor-data) in each component's expanded panel for the selected range. On by default",
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
              "[Content-Security-Policy](#content-security-policy) for the public page: `strict`, `compatible`, `off`, or empty for automatic",
            ],
            [
              "`allowed_embed_origins`",
              "Up to 20 origins (`https://host[:port]`, a leading `*.` host wildcard allowed) that may [embed the page](#content-security-policy) in an iframe. `[]` clears the list",
            ],
            [
              "`seo_description`",
              "Search and link-preview text, up to 300 characters: the page's `meta description`, `og:description` and `twitter:description`. `\"\"` clears it; the page then uses `description`, else a generic line. See [search and link previews](#seo)",
            ],
          ],
        },
        {
          type: "paragraph",
          text:
            "Settings are part of the page definition and can be supplied at creation or update. `show_monitor_tags`, `show_monitor_url`, `show_monitor_tls` and `show_agent_metrics` are reserved in the schema but not yet consumed by the built-in renderer; storing them does not make those panels appear. The page editor offers the response-time chart toggle and the search description; the remaining settings are set through the API or the template screen. Custom template authors must verify which data is present in the current template context.",
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
      id: "content-security-policy",
      title: "Content-Security-Policy and embedding",
      blocks: [
        {
          type: "paragraph",
          text:
            "Every public page response carries a `Content-Security-Policy` built from the page's `csp_mode` and a fresh random nonce, plus `X-Content-Type-Options: nosniff` and `Referrer-Policy: no-referrer`. Set the mode under **Security policy** on the page's template screen, or through the `settings` object of the API.",
        },
        {
          type: "table",
          columns: ["Mode", "What runs"],
          rows: [
            [
              "`strict`",
              "Only `<script>` elements carrying `nonce=\"{{cspNonce}}\"` run. Stylesheets and fonts may also come from any `https:` origin (custom CSS `@import` and `@font-face`), and images from `https:` or `http:` (a remote `logo_url`) — none of them can run code. Connections and the push service worker stay on the page's own origin. No plugins, no `<base>` rewriting.",
            ],
            [
              "`compatible`",
              "Inline scripts and styles, plus scripts, styles, fonts, images, frames and connections from any `https:` or `http:` origin. For pages whose custom HTML or CSS loads analytics, chat widgets or web fonts. On an HTTPS page the browser still refuses `http:` scripts as mixed content.",
            ],
            [
              "`off`",
              "No resource policy. Only `frame-ancestors` is sent, because embedding is controlled separately.",
            ],
            [
              "empty (automatic)",
              "`compatible` while the page has custom CSS, custom head/footer HTML or a published custom template, `strict` otherwise. Existing customized pages keep working without edits, and an uncustomized page's remote logo keeps rendering under `strict`.",
            ],
          ],
        },
        {
          type: "paragraph",
          text:
            "In templates, `{{cspNonce}}` returns the response's nonce: put `nonce=\"{{cspNonce}}\"` on every inline `<script>` and `<style>`, as the default template does. The custom head and footer HTML settings are not template source, but the literal text `{{cspNonce}}` inside them is replaced with the nonce too. The template editor warns when a draft has a `<script>` without the nonce. A nonce changes on every response, so never cache or copy it.",
        },
        {
          type: "paragraph",
          text:
            "Framing (`frame-ancestors`) is allowed for the page's own origin, the operator UI (`APP_BASE_URL` on the status-page service, which the template editor's preview needs) and the page's `allowed_embed_origins`. Any other site that puts the page in an iframe gets a blank frame. The draft preview route admits any ancestor while `APP_BASE_URL` is unset, because the service cannot name the editor's origin; it shows only data the live page already publishes.",
        },
        {
          type: "callout",
          tone: "info",
          title: "Switch a customized page to strict",
          text:
            "Add `nonce=\"{{cspNonce}}\"` to the scripts in your template and HTML snippets, set the mode to `strict`, and check the browser console on the published page: blocked resources are reported there.",
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
              "`GET /public/status/{slug}/summary.json`",
              "The page's current status as a [versioned JSON contract](#summary-json). Readable cross-origin",
            ],
            [
              "`GET /public/status/{slug}/badge.svg`",
              "Page [status or uptime badge](#badges)",
            ],
            [
              "`GET /public/status/{slug}/monitors/{id}/badge.svg`",
              "Badge for one component the page shows; any other monitor ID is a 404",
            ],
            [
              "`GET /public/status/{slug}/embed.js`",
              "[Embeddable widget](#embed-widget) script",
            ],
            [
              "`GET /public/status/{slug}/feed.atom`",
              "[Atom feed](#feed) of incidents and maintenance. `feed.rss` redirects here permanently",
            ],
          ],
        },
        {
          type: "paragraph",
          text:
            "The page itself is sent with `Cache-Control: no-cache` and an `ETag`, so browsers revalidate it every time. `summary.json` and the badges are sent with `Cache-Control: public, max-age=60`, the feed with `max-age=300` and `embed.js` with `max-age=3600`, each with an `ETag`, so a CDN in front of the status service can absorb README badges and feed readers. Every route under `/public/status/{slug}/` is dispatched by the same handler as the page.",
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
      id: "seo",
      title: "Search and link previews",
      blocks: [
        {
          type: "paragraph",
          text:
            "The built-in template's `<head>` carries a `meta description`, Open Graph tags (`og:type`, `og:title`, `og:description`) and a `twitter:card` of type `summary`, so a shared link unfurls with the page title and description. The description is `seo_description`, else the page `description`, else \"Live status, uptime and incident history for {title}.\" An absolute `http(s)` `logo_url` also becomes `og:image`.",
        },
        {
          type: "paragraph",
          text:
            "`<link rel=\"canonical\">` and `og:url` need an absolute URL, so they appear only when the status-page service knows its public origin: set [`STATUS_PAGE_BASE_URL`](/docs/configuration/#status-page-service) (Helm `statusPage.baseUrl`) and the page's canonical URL becomes `{STATUS_PAGE_BASE_URL}/public/status/{slug}`. Without it the tags are left out rather than guessed. The head also links the [Atom feed](#feed) with `<link rel=\"alternate\" type=\"application/atom+xml\">`, so feed readers discover it from the page URL.",
        },
      ],
    },
    {
      id: "summary-json",
      title: "Summary JSON",
      blocks: [
        {
          type: "paragraph",
          text:
            "`GET /public/status/{slug}/summary.json` is a stable, documented view of what the page shows, for scripts, dashboards and the [widget](#embed-widget). It is built with the page from the same data and cached with it, so it never disagrees with the rendered page, and it carries `Access-Control-Allow-Origin: *` (without credentials) so any site can read it. Fields are only ever added; a removed or redefined field bumps `version`.",
        },
        {
          type: "table",
          columns: ["Field", "Meaning"],
          rows: [
            ["`version`", "Contract version, currently `1`"],
            ["`page`", "`slug`, `title`, and `url` (the canonical URL; omitted when `STATUS_PAGE_BASE_URL` is unset)"],
            [
              "`status`",
              "`indicator` — `operational`, `issues` or `maintenance`, the page header's own rule (any down, degraded or unknown component is `issues`); `description` — the header label, e.g. \"All systems operational\"; `counts` — `total`, `operational`, `degraded`, `down`, `maintenance`, `unknown`",
            ],
            [
              "`uptime`",
              "Page-level availability in percent for `1h`, `24h`, `7d`, `30d` and `90d`, rounded to two decimals; `null` for a range with no data",
            ],
            [
              "`components[]`",
              "Every component the page renders, in page order: `id` (the monitor ID), `name` (its public display name), `section`, `status` (`operational`, `degraded`, `down`, `maintenance` or `unknown`) and the same `uptime` object",
            ],
            [
              "`incidents[]`",
              "Published incidents that are not resolved: `id`, `title`, `state`, `published_at`, `affected_components`, and `latest_update` (`message`, `created_at`) when there is one",
            ],
            [
              "`maintenance[]`",
              "Windows in progress or starting within 14 days: `title`, `starts_at`, `ends_at`, `in_progress`, `affected_components`",
            ],
            ["`updated_at`", "When this snapshot was built (at most the render-cache TTL old)"],
          ],
        },
      ],
    },
    {
      id: "badges",
      title: "Status badges",
      blocks: [
        {
          type: "paragraph",
          text:
            "`/public/status/{slug}/badge.svg` is a flat SVG badge for the page, and `/public/status/{slug}/monitors/{id}/badge.svg` one for a single component, labelled with its public name. Only components the page renders have a badge; any other monitor ID returns 404, so badges never reveal a monitor the page does not show. Embed them as ordinary images, for example `![status](https://status.example.com/public/status/acme/badge.svg)` in a README.",
        },
        {
          type: "table",
          columns: ["Query", "Effect"],
          rows: [
            [
              "`metric=status` (default)",
              "The page badge shows the worst component state: `outage`, `degraded`, `unknown`, `maintenance`, else `operational`. A component badge shows that component's status",
            ],
            [
              "`metric=uptime`",
              "Availability for `range` with the page's own colors: green from 99%, amber from 95%, red below; `no data` when the range is empty",
            ],
            ["`range`", "`1h`, `24h`, `7d`, `30d` (default) or `90d`; anything else is a 400"],
          ],
        },
      ],
    },
    {
      id: "embed-widget",
      title: "Embeddable widget",
      blocks: [
        {
          type: "paragraph",
          text:
            "Add the page's current status to another site with one script tag. The widget renders where the tag sits (or into `data-target`), inside a shadow root so neither page's CSS reaches the other, reads `summary.json` next to its own URL and refreshes every 60 seconds. Everything from the summary is inserted as text, never as HTML.",
        },
        {
          type: "code",
          language: "html",
          code: '<script src="https://status.example.com/public/status/acme/embed.js" async\n        data-components="true" data-theme="light"></script>',
        },
        {
          type: "table",
          columns: ["Attribute", "Effect"],
          rows: [
            ["`data-target`", "CSS selector of the element to render into, instead of after the script tag"],
            ["`data-components`", "`true` lists every component with its status"],
            ["`data-theme`", "`light` or `dark`; by default the visitor's color scheme"],
            ["`data-refresh`", "Seconds between refreshes, at least 30; `0` turns refreshing off"],
          ],
        },
        {
          type: "paragraph",
          text:
            "The widget is a script, not a frame, so `allowed_embed_origins` does not apply to it: that list controls only which sites may put the full page in an `<iframe>` (see [CSP and embedding](#content-security-policy)). Instead, the embedding site's own Content-Security-Policy must allow the status origin in `script-src` (for `embed.js`) and `connect-src` (for `summary.json`). The widget styles itself with a constructed stylesheet where the browser supports one and a `<style>` element otherwise; if the embedding site restricts `style-src`, check the widget under that policy, and allow `'unsafe-inline'` styles if it renders unstyled.",
        },
      ],
    },
    {
      id: "feed",
      title: "Incident and maintenance feed",
      blocks: [
        {
          type: "paragraph",
          text:
            "`/public/status/{slug}/feed.atom` is an Atom feed of the page's published incidents and its maintenance windows; the page footer links it and the page head advertises it. Each incident entry carries its state, summary, affected components, resolution time and its five newest public updates, and is updated whenever the incident or one of those updates changes. Maintenance entries cover windows in progress or starting within 14 days, like the page. Unpublished incidents and incidents published only to other pages never appear.",
        },
        {
          type: "paragraph",
          text:
            "The feed is bounded: the 50 most recently changed publications, read without the page's monitor and uptime work, so frequent polling stays cheap. Entry links point at the page — absolute when `STATUS_PAGE_BASE_URL` is set, otherwise relative to the feed URL, which feed readers resolve.",
        },
      ],
    },
    {
      id: "visibility",
      title: "Visibility, passwords and kiosk tokens",
      blocks: [
        {
          type: "table",
          columns: ["Visibility", "Who can view the page"],
          rows: [
            ["`public`", "Anyone. The default"],
            ["`unlisted`", "Anyone with the link. Every response carries `X-Robots-Tag: noindex` and the page a robots `noindex` meta tag. Not access control"],
            ["`password`", "Visitors who enter the page password, and [kiosk tokens](#kiosk-tokens). The page URL shows a password form"],
            ["`private`", "Kiosk tokens only. Without one the page and every route under it answer 404, as if it did not exist"],
          ],
        },
        {
          type: "paragraph",
          text:
            "The check runs before every route under `/public/status/{slug}/` — the page, `/data`, `/stream`, the push routes, the draft preview and any route added later — and before the render cache, so changing a page to password or private takes effect on the next request even for a cached page. Only `/public/status/sw.js` (no page data) and the unlock route are outside it. Set visibility and the password on the page's edit screen or through `visibility` and `password` on create and update.",
        },
        {
          type: "paragraph",
          text:
            "Entering the password sets a session cookie for that page, valid 30 days. Changing or clearing the password signs every visitor out. Unlock attempts are limited to 5 a minute per client per page and 60 a minute per page. Responses for password and private pages send `Cache-Control: private, no-store`, so neither the browser nor a shared cache keeps them.",
        },
      ],
    },
    {
      id: "kiosk-tokens",
      title: "Kiosk tokens",
      blocks: [
        {
          type: "paragraph",
          text:
            "A kiosk token opens a password or private page without the password, for wall displays and dashboards. Create one with a label under **Access** on the page's edit screen (or `POST /api/v1/status-pages/{id}/access-tokens`); the token is shown once, together with the kiosk URL `…/public/status/{slug}?token=<token>`. Only a hash is stored. Opening that URL sets a session cookie, so the page's live updates keep working without the token. Revoking a token (`DELETE …/access-tokens/{accessId}`) ends every session it opened on that session's next request. A page holds at most 50 active tokens.",
        },
        {
          type: "table",
          columns: ["Route", "Response"],
          rows: [
            ["`POST /public/status/{slug}/unlock`", "Checks the form field `password`. Success sets the session cookie and redirects to the page (303); a wrong password shows the form again (401); too many attempts answer 429"],
            ["`GET /api/v1/status-pages/{id}/access-tokens`", "The page's kiosk tokens, revoked ones included, without token values"],
            ["`POST /api/v1/status-pages/{id}/access-tokens`", "Creates a token from `{label}` and returns it once in `token`"],
            ["`DELETE /api/v1/status-pages/{id}/access-tokens/{accessId}`", "Revokes a token; idempotent"],
          ],
        },
        {
          type: "list",
          items: [
            "Password and private pages need `STATUS_PAGE_SESSION_SECRET` on the status-page service. Without it they are unavailable — a password page answers 503 and a private page 404 — while public and unlisted pages serve as usual. See [configuration](/docs/configuration/) and [deployment](/docs/deployment/).",
            "Browser notifications are offered on public and unlisted pages only. A page that becomes password or private stops notifying, including browsers that subscribed before.",
            "The session cookie is `SameSite=Lax`, so a password or private page framed by another site gets no session. Open kiosk displays on the page itself.",
            "The draft preview of a gated page opens with a valid preview token only when `STATUS_PAGE_PREVIEW_SECRET` is set; otherwise the editor's session on the page is needed.",
            "The in-page customizer proxy (`/_sp_api/`) refuses calls naming a password or private page unless the caller holds a session for it.",
          ],
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
            "The active built-in view shows current state and the configured monitor/global uptime summaries. It supports ordered sections, active/recent incidents, and maintenance. A page lists at most 50 published incidents: every open one first, then the most recently updated resolved ones. Group and passive monitors present derived or reported state rather than an active network-check result.",
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
            "With `show_latency_charts` on, a component's expanded panel also draws its response time for the selected range, with its average and maximum: the mean latency of successful checks per cell (two minutes at `1h`, an hour at `24h`, a day at `7d` and `30d`, two days at `90d`). Failed checks are left out, and a cell with no successful timed check is a gap in the line, never zero. `1h` and `24h` come from the same raw checks and hourly rollups as the uptime strips; `7d` and longer from the daily rollups. A group charts its members' checks pooled together.",
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
            "Public page data includes applicable maintenance with an occurrence in progress or starting within the next 14 days. A window is applicable when it directly covers a selected monitor or covers a selected group and its members. Each window appears once, at its earliest such occurrence (times in UTC); a repeating window is labelled \"Repeats\" and lists up to three following starts. Custom templates read that line as `RepeatText` on each `MaintenanceWindows` entry, next to `TimeRangeText`.",
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
              "The response's Content-Security-Policy nonce, for `nonce=\"{{cspNonce}}\"` on inline scripts and styles ([details](#content-security-policy))",
            ],
          ],
        },
        {
          type: "paragraph",
          text:
            "Each range has its own strip data (a JSON array of `{label, uptime}` cells, `uptime` -1 for no data, already resampled to the cell count the functions return) and uptime figure. The page exposes `.GlobalUptime1hJSON`, `.GlobalUptime24JSON`, `.GlobalUptime7JSON`, `.GlobalUptime30JSON` and `.GlobalUptime90JSON` with matching `…Value` strings. Each monitor exposes `.History1hJSON`, `.History24hJSON`, `.History7dJSON`, `.History30dJSON` and `.History90dJSON` for its row, `.ExpandedHistory1hJSON`, `.ExpandedHistory30dJSON` and `.ExpandedHistory90dJSON` for its expanded strip, and `.Uptime1hValue` through `.Uptime90Value`. The `1h` fields were added later; a template written before them simply offers no `1h` range.",
        },
        {
          type: "table",
          columns: ["Later fields", "Content"],
          rows: [
            [
              "`.ShowLatencyCharts`",
              "The page's `show_latency_charts` setting",
            ],
            [
              "`.HasLatency`, `.Latency1hJSON` … `.Latency90dJSON` (per monitor)",
              "Response-time series for `1h`, `24h`, `7d`, `30d` and `90d`: a JSON array of mean milliseconds per fixed cell, oldest first, `null` for a cell without data. `.HasLatency` is false when every range is empty",
            ],
            [
              "`.MetaDescription`",
              "The description the head tags use (`seo_description`, else the description, else a generic line)",
            ],
            [
              "`.CanonicalURL`",
              "Absolute page URL from `STATUS_PAGE_BASE_URL`; empty when unset",
            ],
            [
              "`.FeedURL`",
              "The Atom feed URL (absolute when `.CanonicalURL` is set)",
            ],
            [
              "`.OGImageURL`",
              "`logo_url` when it is an absolute `http(s)` URL, else empty",
            ],
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
            "Review built-in visibility settings and treat the URL, tag, agent-metric, and TLS flags as schema-reserved until the renderer explicitly supports them.",
            "Set `STATUS_PAGE_BASE_URL` so the page carries a canonical link and `og:url`, and check a shared link's preview.",
            "Preview light and dark themes at desktop and mobile widths.",
            "Exercise unknown, suspect, degraded, down, maintenance, and incident states in a safe preview environment.",
            "Check custom HTML/CSS for content security, accessibility, focus states, contrast, and responsive overflow, and that the page runs under its [CSP mode](#content-security-policy) without console violations.",
            "Configure the preview secret and confirm the public status service has a reachable API/data path.",
            "Publish, open the anonymous route in a clean browser session, and verify SSE/fallback refresh behavior.",
          ],
        },
      ],
    },
  ],
};
