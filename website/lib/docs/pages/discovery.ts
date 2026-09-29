import type { DocPage } from "../types";

export const DISCOVERY_PAGE: DocPage = {
  slug: "discovery",
  group: "Use Probara",
  title: "Discovery and coverage",
  description: "Discover Kubernetes Services and Ingresses, review monitor suggestions, and inspect monitoring coverage gaps.",
  eyebrow: "Monitoring inventory",
  readingTime: "8 min read",
  keywords: ["Kubernetes", "discovery", "coverage", "inventory", "monitor suggestions"],
  sections: [
    {
      id: "setup", title: "Connect a cluster", blocks: [
        { type: "paragraph", text: "Open Discovery → Connect cluster as an editor or admin. Choose a name, 1–20 existing namespaces, and an optional private location. Enter the HTTPS API origin and the collector image from your release (`ghcr.io/yassinebenameur/probara-discovery-collector:<version>`, or one you built and published yourself), download the manifest, and apply it to the intended cluster. The API requires migration 000098. The collector runs in the first selected namespace and lists Services and Ingresses every five minutes." },
        { type: "paragraph", text: "The manifest contains namespaced list-only Roles, a ServiceAccount, a non-root Deployment, and a Secret with an integration-scoped token. Kubernetes credentials stay in the cluster. The collector does not use a tenant API key, NATS, or PostgreSQL. It proposes checks; existing workers execute them after approval. Private targets require a separately deployed location worker with the necessary network access." },
        { type: "callout", tone: "warning", title: "Keep the manifest private", text: "Its collector token is returned only at creation or rotation. Remove the downloaded file after applying it. Reinstalling rotates the token and invalidates the old collector credential; apply the new manifest to resume scans." },
        { type: "code", language: "bash", code: "# Only when the published image is not available:\ndocker build -f cmd/discovery-collector/Dockerfile -t registry.example.com/probara-discovery:discovery-v1 .\n# Publish to your registry through your usual process, then use that image in setup.\nkubectl --context your-cluster apply -f probara-discovery-<integration-prefix>.yaml" },
      ],
    },
    {
      id: "suggestions", title: "Review suggestions", blocks: [
        { type: "table", columns: ["Resource", "Suggestion"], rows: [
          ["Ingress with a concrete hostname", "HTTP GET /; HTTPS when spec.tls declares the hostname or a wildcard covering it (*.example.com covers api.example.com). Wildcard and catch-all rules need manual configuration."],
          ["Service TCP port", "TCP connectivity from a private location. This does not prove authenticated application health."],
          ["Service with probara.io/health-path", "HTTP health check. Specify probara.io/health-port for a multi-port service and optional probara.io/health-scheme: https. Invalid or oversized health hints fall back to TCP without blocking inventory scans."],
          ["UDP or ExternalName Service", "Inventory only; no automatic check suggestion."],
        ] },
        { type: "paragraph", text: "Set probara.io/discover: false to exclude a resource. Set probara.io/service to the same name on resources in one namespace to group their suggestions under one service. Grouping values are trimmed; values over 100 bytes are ignored. Otherwise identity is namespace/kind/name within the integration. Changing this grouping does not silently move existing monitor links." },
        { type: "paragraph", text: "Existing monitors of the same target are candidates for explicit linking — matching ignores host case, a trailing root slash, an explicit default port, and the .cluster.local suffix of a Service name. Verify their assertions and location. Approving a target that an existing monitor already checks from the same location is refused; link that monitor instead. Approval selects a name, interval, execution location, expected HTTP status, and workspace-default or specific active notification channels. Monitor creation, its initial state interval, assignments, and inventory link are atomic. Repeated approval returns the linked monitor. Customize other assertions in the normal monitor editor afterward." },
      ],
    },
    {
      id: "coverage", title: "Understand the coverage table", blocks: [
        { type: "paragraph", text: "Coverage and health are separate. The table reports missing enabled monitor links, stale check evidence, unreachable alert routing, missing resources, and stale inventory. Alert reachability uses the same shared rules as the alerter, including group roll-up. It reports configured reachability, not successful delivery to a provider. A healthy check can still have a routing gap; a failing check can be fresh and properly routed." },
        { type: "paragraph", text: "Check evidence is fresh by the same rule the platform uses for absence detection: received by Probara within three monitor intervals (at least 90 seconds), or interval plus grace period for push monitors. The worker's own timestamps are not used. Group freshness cannot be assessed from check results; link leaf checks. Inventory becomes stale after 15 minutes without a complete scan and blocks new approvals. Missing resources have a 15-minute grace period. Missing or stale inventory never deletes a monitor. Unlinking and deleting an integration preserve monitor history." },
        { type: "callout", tone: "info", title: "Scope of the assessment", text: "This release does not measure functional journeys, dependency completeness, location redundancy, ownership, or criticality. A generic linked monitor does not certify all suggested endpoints. No composite coverage score is shown. Location and channel setup pickers show the first 100 entries; other choices remain available through the monitor editor." },
      ],
    },
    {
      id: "limits", title: "Reconciliation and limits", blocks: [
        { type: "paragraph", text: "Every scan must list both resource kinds in every configured namespace, including all pagination pages. Any list failure aborts the upload. The API validates the complete snapshot under a revision check and applies it transactionally; stale or replayed snapshots return 409. A valid empty snapshot marks resources missing, while an incomplete scan leaves the prior inventory intact." },
        { type: "paragraph", text: "Limits: 20 integrations per workspace, 20 namespaces and 2,000 resources per scan, the first 32 suggestions per resource, and 4 MiB per page/upload. Each cycle has a two-minute deadline. Historical missing resources remain until the integration is removed. Namespace configuration is immutable: create a replacement integration and link existing monitors to change scope." },
        { type: "paragraph", text: "Management APIs live under /api/v1/discovery and use tenant authentication and normal write permissions; [MCP](/docs/mcp/) clients reach the same routes as tools (`get_discovery_coverage`, `approve_discovery_suggestion`, …). The scoped collector token works only on /api/v1/discovery-collectors/{id}/config and /snapshot; it cannot create monitors. Snapshot data is untrusted: the API decides which targets are internal (every Service address, plus single-label, IP, .svc, .cluster.local, .local and .internal hosts) and so need a private location, whatever the collector reports, and refuses a Service suggestion that does not target that Service's own cluster address. HTTPS verification remains enabled and collector clients refuse redirects." },
      ],
    },
  ],
};
