import type { DocPage } from "../types";

export const STATUS_PAGES_PAGE: DocPage = {
  slug: "status-pages",
  group: "Use Probara",
  title: "Status pages",
  description:
    "Publish branded, live public status pages with sections, incidents, maintenance, and versioned templates.",
  eyebrow: "Public communication",
  readingTime: "27 min read",
  keywords: [
    "status page",
    "public incidents",
    "status page template",
    "SSE",
    "custom CSS",
    "status badge",
    "Atom feed",
    "email subscribers",
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
              "Who may view the page: `public` (default), `unlisted`, `password` or `private` — see [visibility and kiosk tokens](#visibility)",
            ],
            [
              "`password`",
              "Write-only visitor password, 8–72 characters; reads report `has_password` only",
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
            "A public or unlisted page is readable by anyone who has its URL. Only select monitors, names, descriptions, incidents, and maintenance details intended for unauthenticated visitors, or make the page password-protected or private. The active built-in template does not currently render monitor target URLs, but custom templates and future use of schema-reserved fields must still be reviewed for disclosure.",
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
              "Off keeps each monitor's target out of the built-in page, including its search filter and the row's detail line (which otherwise falls back to the target), and out of `/data`",
            ],
            ["`show_monitor_uptime`", "Controls per-monitor uptime in the built-in view. Off removes it everywhere on the page: the uptime strips, the metric column and kiosk tiles (shown as `—`), the search filter, `/data`, `summary.json` and the uptime badges"],
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
              "On by default. Off sends `noindex, nofollow` in a robots meta tag and an `X-Robots-Tag` header; the page stays public. Applies to public pages only: unlisted, password and private pages are always `noindex, nofollow` (the settings UI shows the toggle forced off)",
            ],
            [
              "`enable_email_subscriptions`",
              "Off by default. Shows a subscribe form on public and unlisted pages; see [email subscribers](#email-subscribers). Turning it on is refused while the API cannot send subscriber mail",
            ],
            [
              "`auto_publish_incidents`",
              "Off by default. Publishes a monitor's outage to the page after 5 minutes and resolves it on recovery; see [automatic publishing](#auto-publish)",
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
              "Public JSON-shaped page data for clients. It carries only what the page's settings show: no uptime with `show_monitor_uptime` off, no target URL with `show_monitor_url` off, no average latency with `show_latency_charts` off, no tags with `show_monitor_tags` off and no certificate dates with `show_monitor_tls` off",
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
              "Removes a browser push subscription. Always 204 for a page the visitor may see, so it cannot be used to probe which endpoints exist",
            ],
            [
              "`POST /public/status/{slug}/unlock`",
              "The password form's target on a password page; sets the visitor session cookie. `GET` returns to the page",
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
            [
              "`POST /public/status/{slug}/subscribe`",
              "The subscribe form's target (a plain form post, no script needed); forwarded to the API. See [email subscribers](#email-subscribers)",
            ],
            [
              "`GET /public/status/{slug}/subscription`",
              "The page a confirmation or unsubscribe link opens. It acts on nothing until the visitor presses its button",
            ],
            [
              "`POST /public/status/{slug}/subscription/confirm` and `…/unsubscribe`",
              "That page's buttons; forwarded to the API",
            ],
          ],
        },
        {
          type: "paragraph",
          text:
            "Every route under `/public/status/{slug}/` — those above and any added later — passes the page's [visibility](#visibility) check before it does anything else, including serving the cached render. The only exceptions are `/unlock`, which is how a visitor gets through the check, and `sw.js`, which serves no page data.",
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
            "Custom templates receive the same values as `.SEOTitle`, `.MetaDescription`, `.SiteName`, `.CanonicalURL` (empty without a public origin), `.OGImageURL` (empty unless the logo is absolute), `.Robots` (`index, follow` or `noindex, nofollow`), `.FeedURL` and `.SummaryURL`. A template that prints no robots tag is still covered: with indexing off every page response carries `X-Robots-Tag: noindex, nofollow`. Indexing is off for every page that is not [public](#visibility). The unlock form of a password page carries none of these tags: only the page title, its logo and `noindex, nofollow`; a private page without a credential answers `404` with no head at all.",
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
            "Each page publishes three read-only add-ons beside it. They show nothing the page itself does not: the same rendered components, the same published incidents and the same maintenance list. Components are named as the page names them, including in incident and maintenance \"affected\" lists: the display name the page gives the monitor, else the monitor's own name. For a public or unlisted page they are readable from any site (`Access-Control-Allow-Origin: *`, never with credentials), carry an `ETag` and answer `If-None-Match` with `304`; every add-on is sent with `X-Content-Type-Options: nosniff`, a `default-src 'none'` Content-Security-Policy and `X-Robots-Tag: noindex`, and returns `404` for an unknown page. A password or private page's add-ons pass the same [access check](#visibility) as the page and work only for a browser that has unlocked it, privately — see the table there.",
        },
        {
          type: "table",
          columns: ["Add-on", "Content and caching"],
          rows: [
            [
              "`feed.atom`",
              "Atom 1.0 (`application/atom+xml`). One entry per published incident, the 50 most recently changed that are unresolved or changed in the last 90 days, each quoting its five newest public updates; unpublished incidents and internal notes never appear. One entry per maintenance window the page lists, at its occurrence in progress or next one. Entry ids are stable and specific to the page: an incident keeps its id through every update, the same incident on two pages is a different entry in each feed, and each occurrence of a repeating window is its own entry. Text is plain (`type=\"text\"`), so incident wording never becomes markup in a reader. Built at most once per render-cache TTL and dropped with the page's cache on a change, whatever query string a request carries. `Cache-Control: public, max-age=300`",
            ],
            [
              "`summary.json`",
              "The page summary described below. Shares the page's render, so it is at most as stale as the page (the render cache TTL, `STATUS_PAGE_CACHE_TTL`). Its `ETag` ignores `generated_at`, so a poll answers `304` while the data is unchanged. `Cache-Control: public, max-age=60`",
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
      id: "visibility",
      title: "Visibility and kiosk tokens",
      blocks: [
        {
          type: "table",
          columns: ["Visibility", "Who can view it", "Search engines", "Feed, summary and badges"],
          rows: [
            [
              "`public` (default)",
              "Anyone with the URL",
              "May index it (`allow_search_indexing`)",
              "Public: readable from any site, cached by shared caches for their max-age, embeddable",
            ],
            [
              "`unlisted`",
              "Anyone with the URL. A hidden link, not access control: anyone who learns the URL can share it",
              "Never: every response carries `X-Robots-Tag: noindex, nofollow` and the page a robots meta tag, whatever `allow_search_indexing` says. Never named on a listing, sitemap or other discovery surface",
              "As for a public page: anyone with the URLs can read and embed them",
            ],
            [
              "`password`",
              "Visitors who enter the page's password, and browsers opened with a kiosk token. Everyone else gets the unlock form",
              "Never",
              "Only for a browser that unlocked the page: `private, no-store`, no `ETag`, no CORS grant, `Cross-Origin-Resource-Policy: same-origin`. Refused (`401`) to everyone else, so they cannot be embedded on another site or polled by a feed reader",
            ],
            [
              "`private`",
              "Only browsers opened with a kiosk token. Everyone else gets exactly the response of a page that does not exist",
              "Never",
              "Only for a browser with a kiosk session, privately as for a password page. Everyone else gets `404`, the same as a page that does not exist",
            ],
          ],
        },
        {
          type: "paragraph",
          text:
            "Set visibility and the password on the page's settings in the UI, or with `visibility` and `password` on [create and update](/docs/api/). The password is write-only: omit it to keep the stored one, send `\"\"` to remove it (refused while the page is password-protected), anything else replaces it. Leaving `password` visibility keeps the stored password, so switching back needs no new one.",
        },
        {
          type: "paragraph",
          text:
            "A visitor who enters the right password, or opens a kiosk URL, gets a session cookie: signed with `STATUS_PAGE_SESSION_SECRET`, `HttpOnly`, `SameSite=Lax`, scoped to the page's path, and `Secure` per `STATUS_PAGE_COOKIE_SECURE` — by default on when `STATUS_PAGE_BASE_URL` is `https://`, else only for requests that arrived over HTTPS (behind a TLS-terminating proxy that needs `TRUSTED_PROXY_CIDRS`, so set the base URL or the flag). Nothing about the session is stored. A password session lasts 30 days and ends early when the password is set, changed or removed; a kiosk session lasts 400 days (the browser maximum) and ends at once when its token is revoked or the page stops accepting tokens. Every open live stream re-checks the page and the session every 15 seconds, so a stream opened while the page was public ends once it is not, and a visibility or password change also closes the page's streams at once so they reconnect through the check.",
        },
        {
          type: "paragraph",
          text:
            "**Kiosk tokens** are for wall displays and dashboards. Create one from the page's Kiosk tokens panel (or `POST /api/v1/status-pages/{id}/access-tokens` with a label); the token is shown once, and only its SHA-256 hash is stored. Open `/public/status/{slug}?token=<token>` on the display once: the page swaps the token for a session cookie and redirects to the same URL without it, so the token leaves the address bar and is not re-sent by the page's refreshes or carried in a `Referer`. The page does this for every visibility, so a token never lingers in the URL of a page that was made public or unlisted. A token opens a private page and also bypasses the password on a password page. Each page can hold 50 active tokens; revoked ones stay listed with who created them and when.",
        },
        {
          type: "callout",
          tone: "warning",
          title: "Kiosk URLs are bearer credentials until first use",
          text:
            "The browser still records the kiosk URL it was given as the source of the redirect, so it stays in that browser's history; use a dedicated browser profile on the display. Any proxy, ingress or load balancer in front of the status service that logs query strings records the full kiosk URL, token included. Turn query-string logging off for `/public/status/` (or strip the `token` parameter from access logs) on those hops, and revoke a token whose URL was exposed.",
        },
        {
          type: "table",
          columns: ["Route", "Password page, no session", "Private page, no session"],
          rows: [
            ["The page", "Unlock form (`401`)", "`404`, same as a missing page"],
            ["`/data`, `/stream`, `summary.json`, `feed.atom`, the badges, and every other sub-route, including ones added later", "`401`", "`404`, same as a missing page"],
            ["`/preview/draft`", "`401` unless it carries a valid preview token", "`404` unless it carries a valid preview token"],
            ["`/push/subscribe`", "`401`; with a session still `404` — no notifications on gated pages", "`404`"],
            ["`/unlock`", "Checks the password", "`404`, same as a missing page"],
            ["The in-page API proxy (`/_sp_api/`)", "Refused for calls naming the page", "Refused for calls naming the page"],
          ],
        },
        {
          type: "paragraph",
          text:
            "Every response for a password or private page — allowed or refused — is `Cache-Control: private, no-store` with no `ETag`, no CORS grant and `Cross-Origin-Resource-Policy: same-origin`, so neither a shared cache, a browser revalidation nor another site can hand it to someone else. The status service checks visibility before its render cache, on every request, so a page rendered while it was public is never served from the cache once it is not.",
        },
        {
          type: "paragraph",
          text:
            "Wrong passwords and wrong kiosk tokens share one failure budget: 5 a minute per visitor address (IPv6 counted per /64) and 30 a minute per page. Only failures count, so the right password never costs anything. When a page's budget is spent, only visitors who already failed are refused (`429`); everyone else still gets a check. A visitor whose address cannot be told apart — every visitor arrives from the proxy's address when `TRUSTED_PROXY_CIDRS` is unset — has no budget of its own and is limited by the page budget alone, so one guesser can hold every such visitor at the unlock form; the service logs a warning at startup when password or private pages exist without trusted proxies. A wrong password gets one generic message.",
        },
        {
          type: "paragraph",
          text:
            "Pages on one origin are isolated from each other as far as the browser allows: a password or private page answers a request from another status page on the same origin — a script fetching it, or a frame — as if the visitor had no credential (`Sec-Fetch-Site` together with `Referer`; a same-origin sub-request without a `Referer` is refused; browsers without `Sec-Fetch-*` get the `Referer` check alone), and its `frame-ancestors` names only the operator UI, never `'self'`. A link from another page (a top-level navigation) still works. The add-ons follow the same rules: another status page cannot read a gated page's summary or feed, or show its badge, with the visitor's cookie. A gated badge embedded on another site is refused as well: the session cookie is `SameSite=Lax`, which browsers do not attach to a cross-site image or `fetch`, so the request arrives with no credential.",
        },
        {
          type: "callout",
          tone: "warning",
          title: "Pages sharing an origin trust each other's authors",
          text:
            "Custom templates and custom head/footer HTML run script on the status origin. That script cannot fetch or frame another page's gated content, but it can open another page in a window it controls and read it, and it can read anything the visitor's browser shows on this origin. Give template-editing rights only to people you would trust with every gated page on the installation. Full isolation needs a hostname per page, which custom domains will provide. Slugs are unique across the installation, so someone who can create status pages learns that a slug is taken, including a private page's.",
        },
        {
          type: "callout",
          tone: "warning",
          title: "Password and private pages need STATUS_PAGE_SESSION_SECRET",
          text:
            "Without the [session secret](/docs/configuration/#status-page-service) on the status-page service, a password page answers `503` and a private page `404` — they cannot be opened, and the service logs a warning — while public and unlisted pages serve as usual. The API does not hold the secret, so it accepts these visibilities either way. The release installer and `scripts/start-local-services.sh` generate one; on Helm set `secrets.statusPageSessionSecret` or an existing Secret. Changing it signs every visitor out.",
        },
        {
          type: "list",
          items: [
            "Custom templates are covered too: the unlock form is its own small page (title, logo and primary colour only) and never uses the page template.",
            "The built-in template reloads when a live refresh is refused (`401`/`404`), so a wall display whose token was revoked shows that instead of freezing on the last render. A custom template's own script does what its author wrote.",
            "Notifications already queued for a page when it turns password or private are closed without being sent.",
            "A visitor session belongs to the page's current URL: renaming the slug signs its visitors out, and a kiosk display must open the new URL with its token.",
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
            ["An incident published on the page, a public update posted on it, its resolution", "Sent, naming the incident; an update or resolution replaces the earlier notification"],
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
            "Browser subscriptions store only the opaque browser endpoint and its public keys: no visitor identity or visit history. [Email subscriptions](#email-subscribers) are a separate opt-in that does store the visitor's address.",
            "Password and private pages offer no notifications: a subscription outlives the visitor session that made it. Switching a page to password or private stops notifications to its existing subscribers; switching it back resumes them.",
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
      id: "email-subscribers",
      title: "Email subscribers",
      blocks: [
        {
          type: "paragraph",
          text:
            "With `enable_email_subscriptions` on, a public or unlisted page shows a form where visitors leave an email address. The address gets a confirmation email first (double opt-in): nothing else is sent until its link is followed, and the link works for 48 hours. Confirmed subscribers are then emailed about what is published on the page.",
        },
        {
          type: "table",
          columns: ["Event", "Email"],
          rows: [
            ["An operator publishes an incident on the page (or publishes it again after taking it down)", "Sent, with the incident's summary, state and affected components as the page names them"],
            ["A public update is posted on an incident the page shows", "Sent, with the update's text"],
            ["An incident the page shows is resolved", "Sent"],
            ["An incident changes state without a public update, is edited, or its components change", "Not sent"],
            ["Internal notes", "Never sent"],
            ["A maintenance window that covers a component of the page is scheduled, or its schedule or components change, while it has an occurrence ahead", "Sent once, with its next occurrence; a recurring window's mail describes its weekly rule"],
            ["An announced one-off maintenance window starts", "Sent once, at the start"],
            ["Each occurrence of a recurring window", "Not sent: the window was announced once, with its rule"],
            ["A window that starts immediately (a snooze)", "Not sent"],
            ["A monitor outage published by [automatic publishing](#auto-publish)", "Sent, like an operator's publication, and its resolution"],
          ],
        },
        {
          type: "list",
          items: [
            "Every email goes to one subscriber, in the page's name and accent colour, as HTML and plain text, links back to the page, and carries the subscriber's own unsubscribe link in the body and as a one-click `List-Unsubscribe` header (RFC 8058). Unsubscribing deletes the address.",
            "The subscribe form answers the same way for any address, new, waiting for confirmation, already subscribed or unsubscribed before: the page never tells whether an address is subscribed. Confirmation emails are sent in the background, so the answer's timing does not tell either.",
            "An address gets at most 3 confirmation emails a day per page, however often it is submitted. A page takes at most 100 new addresses an hour and 10,000 subscribers in total; past either limit the form says the page is not accepting subscriptions, for every address. The status-page service also allows each visitor 5 subscription requests a minute.",
            "Only plain addresses are accepted (letters, digits and the usual symbols in dot-separated words, with a domain), so nothing a visitor types can reach an email header.",
            "Unconfirmed addresses are deleted 7 days after their last request.",
            "Delivery is at most once per subscriber and event, retried with backoff on a temporary failure; a message whose sending was interrupted after the relay may have received it is not sent again. Events that could not be emailed within 6 hours (mail not configured, the API down) are dropped rather than sent late.",
            "The settings page lists subscribers with their state, removes one, and exports them all as CSV; exports are recorded in the audit log, as are confirmations and unsubscribes (by a hash of the address, never the address).",
          ],
        },
        {
          type: "callout",
          tone: "info",
          title: "Public and unlisted pages only",
          text:
            "Password and private pages show no subscribe form and email nobody: an email would carry the page's incident text to an address that once had access, past the page's password and sessions. Changing a page to password or private stops its emails (its subscribers are kept); changing it back resumes them.",
        },
        {
          type: "paragraph",
          text:
            "Email subscriptions need the API to send mail: `SMTP_HOST` and `SMTP_FROM`, `APP_BASE_URL` (one-click unsubscribe goes through the API there), `STATUS_PAGE_BASE_URL` (every link in a subscriber email points to the page there) and `PROBARA_SECRETS_KEY` (links are signed with it). Without them the setting cannot be turned on, and the settings page says what is missing. The status-page service forwards the form to the API (`STATUS_PAGE_INTERNAL_API_URL`; see [configuration](/docs/configuration/)) and stores nothing itself.",
        },
        {
          type: "paragraph",
          text:
            "A page rendered by a custom template gets no form unless the author adds one. `.SubscribeEnabled` says whether the page offers subscriptions and `.SubscribeAction` is the form's target:",
        },
        {
          type: "code",
          language: "html",
          code:
            '{{if .SubscribeEnabled}}\n<form method="post" action="{{.SubscribeAction}}">\n  <label for="email">Email address</label>\n  <input id="email" name="email" type="email" autocomplete="email" required maxlength="254" />\n  <button type="submit">Subscribe</button>\n</form>\n{{end}}',
        },
      ],
    },
    {
      id: "auto-publish",
      title: "Automatic publishing of outages",
      blocks: [
        {
          type: "paragraph",
          text:
            "With `auto_publish_incidents` on, a monitor the page shows that has been down for 5 minutes gets an incident published on the page, and the incident is resolved once the monitor recovers. Subscribers are emailed (and pushed to) as for an incident an operator publishes. The delay is fixed.",
        },
        {
          type: "list",
          items: [
            "An outage is the monitor's open availability alert. The 5 minutes count from when it opened, or from its last reopening as a relapse.",
            "Nothing is published for a monitor in maintenance, one that is paused, one whose outage a [dependency](/docs/dependencies/) explains, or one already passing again while its recovery is confirmed.",
            "Resolution waits for the alert to resolve, which happens only once the recovery is confirmed (\"mark up after N passes\").",
            "Nothing is published while an unresolved incident about the monitor is already on the page, and an outage is published at most once per page: if an operator takes the incident down or resolves it, it is not put back while that outage lasts.",
            "The incident is the monitor's automatic incident, the same one the workspace setting \"create an incident when an alert fires\" opens, so a monitor never gets two. One created for publishing is titled \"Service disruption\" and names no monitor; the page lists the affected component under it by the name the page uses. An incident the alerter created earlier keeps its own title.",
            "Only automatically published incidents are resolved automatically; an operator publishing the same incident by hand makes it theirs.",
          ],
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
        {
          type: "paragraph",
          text:
            "Publishing, public updates and resolution reach the page's [email subscribers](#email-subscribers) and [browser notification](#browser-notifications) subscribers. The publish form shows how many email subscribers a page will notify.",
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
        {
          type: "paragraph",
          text:
            "Which maintenance is emailed to [subscribers](#email-subscribers): a window when it is scheduled or rescheduled, and a one-off window again when it starts. A recurring window is announced once with its rule, never at each occurrence.",
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
          type: "paragraph",
          text:
            "On a password or private page the preview token also stands in for the visitor's credential, so the template editor works without a password or kiosk token. That holds only while the secret is set: without it a gated page's preview needs a visitor session like any other route, and the editor's preview cannot open it.",
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
            "Choose the page's visibility; for a password or private page, confirm `STATUS_PAGE_SESSION_SECRET` is set and open it once through the unlock form or a kiosk URL. Its feed, summary and badges then work only in a browser that unlocked it.",
            "Publish, open the anonymous route in a clean browser session, and verify SSE/fallback refresh behavior.",
          ],
        },
      ],
    },
  ],
};
