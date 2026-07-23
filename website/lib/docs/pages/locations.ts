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
            "Copy the generated Docker or Kubernetes configuration into the target network.",
            "Start the worker and wait for its heartbeat. The backend considers a location connected when its latest heartbeat is within roughly one minute.",
            "Assign the location to one or more active monitors and choose a failure quorum.",
          ],
        },
        {
          type: "code",
          language: "yaml",
          title: "Shape of generated worker environment",
          code:
            "image: ghcr.io/yassinebenameur/probara-worker:latest\nenvironment:\n  NATS_URL: tls://nats.example.com:4222\n  WORKER_LOCATION_ID: <location-uuid>\n  LOCATION_CREDENTIAL: <generated-secret>\n  HTTP_PORT: \"8080\"\n  METRICS_PORT: \"9090\"\n  HTTP_BLOCK_PRIVATE_IPS: \"true\"",
        },
        {
          type: "callout",
          tone: "warning",
          title: "A public NATS URL is required",
          text:
            "Generated deployment information requires `PUBLIC_NATS_URL` to use `tls://` or `wss://`, without embedded user information. It must be reachable from the location network and terminate with the NATS authentication callout enabled for location credentials.",
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
            "Use TLS or WSS for the public NATS endpoint as required by deployment-info generation.",
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
            "Workers block private, loopback, link-local, and reserved destinations by default. The generated private-location configuration deliberately keeps `HTTP_BLOCK_PRIVATE_IPS=true`, so merely moving a monitor to an internal worker does not automatically permit every internal address.",
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
            "Add only the CIDRs the worker is expected to monitor. Disabling private-IP blocking globally broadens the worker into an internal network request primitive and weakens SSRF containment.",
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
            "The stored quorum is constrained to at least one and at most the selected location count. A down aggregate opens normal availability alerts; degraded does not. Degraded state also does not use the suspect fast-recheck path.",
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
            "Location workers publish lightweight core-NATS heartbeats approximately every 15 seconds with jitter. The API derives `connected` from the latest accepted heartbeat and treats a location as disconnected after about one minute without one.",
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
            "Mesh alerts are directional and use the tenant's default notification channels. They maintain their own lifecycle rather than changing an application monitor's state.",
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
