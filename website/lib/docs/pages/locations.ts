import type { DocPage } from "../types";

export const LOCATIONS_PAGE: DocPage = {
  slug: "locations",
  group: "Use Probara",
  title: "Private locations",
  description:
    "Deploy credential-scoped workers, run checks from private networks, configure quorum, and monitor the location mesh.",
  eyebrow: "Distributed monitoring",
  readingTime: "20 min read",
  keywords: [
    "private locations",
    "remote worker",
    "NATS",
    "quorum",
    "mesh monitoring",
  ],
  sections: [
    {
      id: "overview",
      title: "What a private location is",
      blocks: [
        {
          type: "paragraph",
          text:
            "A private location is a tenant-scoped execution target backed by one or more remote worker processes. It lets an active monitor run from a branch, data center, VPC, or other network that the default worker fleet cannot reach.",
        },
        {
          type: "callout",
          tone: "info",
          title: "Location workers are queue-only",
          text:
            "A remote location worker connects to NATS and exposes its own health/metrics listeners. It does not receive a PostgreSQL connection string. The backend publishes only jobs addressed to that location.",
        },
        {
          type: "table",
          columns: ["Field", "Meaning"],
          rows: [
            ["`name`", "Required tenant-unique display name, up to 100 characters"],
            [
              "`slug`",
              "Cosmetic derived identifier, up to 60 characters; do not use it as authentication",
            ],
            ["`description`", "Optional operator context"],
            ["`enabled`", "Whether the location receives scheduled work"],
            [
              "`connected`",
              "Derived from recent worker heartbeats, not a manually editable flag",
            ],
            ["`last_seen_at`", "Most recent accepted worker heartbeat"],
            [
              "`offline`",
              "Seen at least once, enabled for at least 180 seconds, and no heartbeat for 180 seconds: the rule the offline alert uses and the page's **Offline**",
            ],
            ["`open_alert_id`", "The location's open `location_offline` alert, when there is one"],
            [
              "`mesh_endpoint`",
              "Optional host-and-port endpoint other locations probe for mesh health",
            ],
            ["`monitor_count`", "Current number of monitors assigned to the location"],
          ],
        },
      ],
    },
    {
      id: "create-and-deploy",
      title: "Create and deploy a worker",
      blocks: [
        {
          type: "list",
          ordered: true,
          items: [
            "Create the location in the UI or location API.",
            "Open its deployment information. Creation generates a random location credential that is stored encrypted when platform secret encryption is enabled.",
            "Copy the generated [Docker or Kubernetes configuration](/docs/deployment/#private-location-workers) into the target network.",
            "Start the worker and wait for its heartbeat. The backend considers a location connected when its latest heartbeat is within roughly one minute.",
            "Assign the location to one or more active monitors and choose a failure quorum.",
          ],
        },
        {
          type: "code",
          language: "yaml",
          title: "Shape of generated worker environment",
          code:
            "image: ghcr.io/yassinebenameur/probara-worker:<platform version>\nenvironment:\n  NATS_URL: wss://nats.example.com\n  WORKER_LOCATION_ID: <location-uuid>\n  LOCATION_CREDENTIAL: <generated-secret>\n  HTTP_PORT: \"8080\"\n  METRICS_PORT: \"9090\"",
        },
        {
          type: "paragraph",
          text:
            "The snippet pins the worker to the platform's own release (the API's version tag), so a new location speaks the same queue contract as the scheduler that feeds it. An API built without a release version, such as a local build, references the moving `:alpha` channel tag instead. Set `LOCATION_WORKER_IMAGE` on the API (Helm: `api.locationWorkerImage`) to point remote sites at a mirror or a different tag. After a platform upgrade, upgrade existing location workers to the same tag.",
        },
        {
          type: "paragraph",
          text:
            "A location worker from before [TCP monitors](/docs/monitors/#network-protocols) gained `tls_ca_pem` and `tls_server_name` ignores both fields: a TLS TCP monitor that relies on a private CA or on a server name other than its host fails on that location with a certificate error (unknown authority, or a name mismatch) until the worker is upgraded. Upgrade the location workers before giving TCP monitors either field.",
        },
        {
          type: "paragraph",
          text:
            "Locations behind a firewall that allows only outbound HTTPS connect over `wss://` on 443: the broker's [websocket listener](/docs/deployment/#nats-websocket) behind an HTTPS ingress or reverse proxy. A `tls://` URL instead needs the broker's TCP port open outbound. The snippet carries a one-line comment naming which of the two it uses. A `wss://` URL may carry a path (`wss://probara.example.com/nats`) when the listener shares a host with the UI.",
        },
        {
          type: "callout",
          tone: "warning",
          title: "A public NATS URL is required",
          text:
            "Generated deployment information requires `PUBLIC_NATS_URL` to use `tls://` or `wss://`, without embedded user information. It must be reachable from the location network and terminate with the [NATS authentication callout](/docs/security/#private-locations-nats) enabled for location credentials.",
        },
        {
          type: "paragraph",
          text:
            "The generated NATS username is the location UUID and the generated password is the location credential. Treat both the deployment output and any copied manifest as secrets.",
        },
      ],
    },
    {
      id: "credentials-and-secrets",
      title: "Credential and secret boundaries",
      blocks: [
        {
          type: "paragraph",
          text:
            "The NATS authentication callout binds a worker credential to one tenant location and limits its queue access. A worker cannot select another tenant or subscribe to arbitrary location work merely by changing an environment variable.",
        },
        {
          type: "paragraph",
          text:
            "Before a job containing protected monitor configuration is sent to a private location, the backend re-encrypts those values for that location credential. The worker receives what it needs for execution; it does not receive the platform's master secrets key.",
        },
        {
          type: "list",
          items: [
            "Store the location credential in a Kubernetes Secret or equivalent secret store, not in a committed values file.",
            "Use TLS or WSS for the [public NATS endpoint](/docs/configuration/#public-urls-and-private-location-auth) as required by deployment-info generation.",
            "Limit broker network exposure and use the generated credential only for its intended location.",
            "Replace a compromised location rather than treating the display slug as a rotatable secret.",
          ],
        },
      ],
    },
    {
      id: "target-policy",
      title: "Monitor private targets safely",
      blocks: [
        {
          type: "paragraph",
          text:
            "Workers allow private, loopback, link-local, and reserved destinations unless `HTTP_BLOCK_PRIVATE_IPS=true`: the code default is `false`, and the generated private-location configuration does not set it. Add it, with a narrow `HTTP_ALLOWED_CIDRS`, so that moving a monitor to an internal worker does not permit every internal address that worker can reach.",
        },
        {
          type: "code",
          language: "dotenv",
          title: "Permit only a trusted application subnet",
          code:
            "HTTP_BLOCK_PRIVATE_IPS=true\nHTTP_ALLOWED_CIDRS=10.42.16.0/20",
        },
        {
          type: "callout",
          tone: "warning",
          title: "Prefer narrow allowlists",
          text:
            "Add only the CIDRs the worker is expected to monitor. Disabling private-IP blocking globally broadens the worker into an internal network request primitive and weakens [SSRF containment](/docs/security/#ssrf-network-policy).",
        },
        {
          type: "paragraph",
          text:
            "A private-location worker places no [SIP test call](/docs/monitors/#network-protocols) until its operator sets `SIP_CALL_ENABLED=true`: such calls are real and possibly billed to the trunk they go through, so a test-call monitor assigned to a location whose worker has not opted in fails there with `sip_call: disabled on this worker`. `SIP_CALL_ALLOWED_DESTINATIONS` further limits which numbers the worker will call. It is also the only opt-in for [call quality](/docs/monitors/#network-protocols): with `call.media`, the call also sends RTP and RTCP to the address in the far end's SDP answer, vetted by the worker's own network policy and the [media scope rule](/docs/security/#ssrf-network-policy). A location worker that predates call quality fails such a monitor with `invalid SIP config: call.media: media analysis is not available yet`, placing no call, until it is upgraded.",
        },
      ],
    },
    {
      id: "assignment",
      title: "Assign monitors and configure quorum",
      blocks: [
        {
          type: "paragraph",
          text:
            "Active monitor types can target one or more locations. Group, agent, and push monitors cannot because they derive state from members or inbound reports. A monitor with no selected locations runs on the default worker fleet.",
        },
        {
          type: "table",
          columns: ["Reports", "Quorum", "Aggregate state"],
          rows: [
            ["Paris down, New York up", "1", "`down`"],
            ["Paris down, New York up", "2", "`degraded`"],
            ["Paris down, New York down", "2", "`down`"],
            ["Paris suspect, New York up", "2", "`suspect`"],
            [
              "Paris unreported, New York up",
              "2",
              "`up` while no down report reaches quorum",
            ],
          ],
        },
        {
          type: "paragraph",
          text:
            "The stored quorum is constrained to at least one and at most the selected location count. A down aggregate opens normal [availability alerts](/docs/alerting/#availability); degraded does not. Degraded state also does not use the suspect fast-recheck path.",
        },
        {
          type: "paragraph",
          text:
            "Only locations with fresh evidence vote. A location that stops reporting for three check intervals (minimum 90 seconds) is treated exactly like one that never reported: its last state stops counting, so a silent-while-up location cannot mask a real outage and a silent-while-down one cannot pin the monitor down. When no location has fresh evidence — or a location-less monitor receives no results at all — the platform's absence watchdog moves the monitor to `unknown` rather than leaving its last state frozen. No data is never rendered as healthy.",
        },
        {
          type: "callout",
          tone: "info",
          title: "Choose quorum from the failure you care about",
          text:
            "Use quorum 1 when any vantage-point failure is actionable. Use a majority or all-locations quorum when you want to alert only for broad outages while still surfacing regional failures as degraded.",
        },
      ],
    },
    {
      id: "heartbeats",
      title: "Connection and heartbeat behavior",
      blocks: [
        {
          type: "paragraph",
          text:
            "Location workers publish lightweight core-NATS heartbeats approximately every 15 seconds with jitter. The API derives `connected` from the latest accepted heartbeat; after about one minute without one the Locations page shows the location as **Disconnected** (amber while monitors run from it), and after three minutes as **Offline**. The scheduler accepts a heartbeat only when the worker's clock is within 2 minutes of its own, so a worker whose clock drifted further reads offline while its checks keep flowing; the scheduler then logs a warning naming the location and the skew every 5 minutes. Keep worker hosts on NTP.",
        },
        {
          type: "paragraph",
          text:
            "A location that stays silent for 180 seconds is **offline**: it is enabled, has reported at least once, has sent no accepted heartbeat since, and was enabled at least 180 seconds ago (a location re-enabled before its worker is back gets the same grace). Offline opens one `location_offline` [alert](/docs/alerting/#model) for the location, routed to the workspace's default channels, and resolves it when the location reports again or is disabled or deleted; the closing notification says which. An operator who resolves it while the location is still silent gets a \"closed by an operator\" notice, and a new alert opens and pages on the next evaluation. The alert is always on and the threshold is fixed. A location that never connected is never offline: nothing was lost. Only locations seen within the last 7 days alert, so upgrading never pages for long-abandoned ones. There is no flap damping: a link that drops for more than three minutes at a time pages each time it drops, and sends a resolve each time it comes back. The alert says when the location was last heard from and how many monitors run from it, and how many of those have no other location; those go `unknown` without fresh checks, while offline itself never changes a monitor's state. The Locations page links an offline location to its open alert, and the API reports `offline` and `open_alert_id` on each location.",
        },
        {
          type: "callout",
          tone: "info",
          title: "No storm when the platform loses heartbeats",
          text:
            "Heartbeats reach the database through NATS and the scheduler, so when that path or the database stalls, every location goes quiet at once, then crosses the threshold one by one. Once the installation's newest heartbeat is more than a minute old, the locations last heard within a minute of it (at least two of them) are held: no offline alert opens for them until that newest heartbeat is 15 minutes old, after which they alert like any other. A location that went quiet earlier is a real outage and alerts as usual, and so does a second site that dies long after a first. An alerter that has just restarted also waits until a heartbeat arrives after its start, or 180 seconds. Holds are logged by the alerter and exported as `probara_alerter_location_offline_guard{reason}` (limits in `probara_alerter_location_offline_guard_limit_seconds{reason}`); a single-location installation cannot tell a dead site from a platform fault and still alerts.",
        },
        {
          type: "callout",
          tone: "warning",
          title: "Connected does not mean every check is healthy",
          text:
            "A heartbeat proves that a worker can reach NATS. It does not prove that the worker can resolve a monitor hostname, reach the target, pass TLS validation, or access its configured internal subnet.",
        },
      ],
    },
    {
      id: "mesh",
      title: "Location mesh monitoring",
      intro:
        "A location with a `mesh_endpoint` can participate in directional connectivity checks between private locations.",
      blocks: [
        {
          type: "paragraph",
          text:
            "For each ordered pair of enabled participating locations in the same tenant, Probara creates a directed edge. With `N` locations, the full directed mesh has `N × (N − 1)` edges. A source worker sends HTTP to the target's `/mesh/echo` endpoint and verifies that the response identifies the expected target location.",
        },
        {
          type: "table",
          columns: ["Mesh setting or state", "Behavior"],
          rows: [
            ["Default interval", "30 seconds"],
            ["Default timeout", "5 seconds"],
            ["Default failure threshold", "3 consecutive failures"],
            ["Suspect recheck", "Approximately 20 seconds"],
            [
              "States",
              "`unknown`, `up`, `suspect`, and `down`, maintained independently per direction",
            ],
            [
              "History",
              "Query by source, target, and time window; includes latency and error information",
            ],
            [
              "Staleness",
              "Means no recent edge result; it is not conclusive proof that the target is down",
            ],
          ],
        },
        {
          type: "code",
          language: "text",
          title: "Direction matters",
          code:
            "Paris ──probe──► New York   can be DOWN\nParis ◄──probe──── New York   can remain UP",
        },
        {
          type: "paragraph",
          text:
            "Mesh alerts are directional and use the tenant's [default notification channels](/docs/alerting/#routing). They maintain their own lifecycle rather than changing an application monitor's state. As soon as a location misses its heartbeat window (about one minute, well before it counts as offline), alerts on paths to or from it stay open but send nothing and read as suppressed, for as long as its [offline alert](#heartbeats) is about to open or is open: a dead site pages once, instead of once per path. While that alert cannot open (the location is beyond the 7-day horizon, or held by the platform-fault guard), its paths page for themselves.",
        },
        {
          type: "callout",
          tone: "info",
          title: "Manual mesh probe is compute-only",
          text:
            "The mesh probe endpoint runs an immediate diagnostic without persisting a normal edge-history result or changing alert lifecycle. It is suitable for viewer and read-key troubleshooting.",
        },
      ],
    },
    {
      id: "disable-delete",
      title: "Disable or delete a location",
      blocks: [
        {
          type: "paragraph",
          text:
            "Disabling a location excludes it from new monitor scheduling and quorum participation without deleting the location record. Deleting it soft-deletes the location, detaches it from monitors, removes its per-location state, and reduces affected quorums as needed.",
        },
        {
          type: "paragraph",
          text:
            "If deletion leaves a monitor with no assigned location, the monitor returns to the default fleet. Its effective state resets to `unknown` and its failure counter resets before new default-fleet results arrive.",
        },
        {
          type: "callout",
          tone: "warning",
          title: "Plan topology changes",
          text:
            "Detaching or deleting locations can change both where a monitor executes and how many failures constitute an outage. Review affected monitor assignments, network reachability, and alert expectations before making the change.",
        },
      ],
    },
  ],
};
