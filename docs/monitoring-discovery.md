# Kubernetes discovery and monitoring coverage

Open **Discovery** in the operator UI. Discovery proposes monitors; it never
creates them automatically. Coverage reports observed inventory, explicitly
linked monitors, result freshness, and configured notification reachability.
It is not a measure of all your infrastructure or an application-quality score.

## Install

1. Upgrade the API and apply migration `000098_monitoring_discovery` using the
   normal migration workflow. Deploy the web application from the same revision.
2. As an editor or admin, select **Connect cluster**. Enter a cluster name and
   1–20 existing namespaces (entering more shows an error naming how many to
   remove; nothing is dropped). The first one is the integration's **home
   namespace** (`home_namespace`), where the collector runs; it is fixed for
   the life of the integration. The others can be added or removed later (see
   [Change namespaces](#change-namespaces)). Optionally select an existing
   private check location.
3. Enter the externally reachable **HTTPS API origin** and the collector image.
   The image field is prefilled with the published repository
   (`ghcr.io/yassinebenameur/probara-discovery-collector:`); add your release
   version as the tag, or replace it with your own build. The browser remembers
   the image of the last downloaded manifest: a published image comes back
   without its tag (it named the previous release), an image of your own as is.
   Download the generated manifest. Its Secret contains an integration-scoped
   random token, available only at creation or rotation. Keep the file private.
4. Apply it to the intended cluster. The collector runs in the home namespace;
   each configured namespace receives a Role and RoleBinding.
5. Wait for the first complete scan, then expand services to review suggestions.

Use the published collector image for your release,
`ghcr.io/yassinebenameur/probara-discovery-collector:<version>` (the Docker
build workflow builds it on every push and publishes it with the other images
only on release tags `vX.Y.Z`, plus `alpha`, `beta` or `latest`). For an
unreleased revision, build it from the repository root, publish it to your own
registry using your normal process, and enter that reference in the setup form:

```sh
docker build -f cmd/discovery-collector/Dockerfile \
  -t registry.example.com/probara-discovery:discovery-v1 .
```

### Image visibility (one-time)

A new GHCR package in a personal account starts **private**, so the first
release tag that pushes `probara-discovery-collector` creates it private and
`docker-build`'s "Check the image is publicly pullable" step fails that leg.
Once:

1. Open github.com/users/yassinebenameur/packages/container/probara-discovery-collector/settings.
2. Set visibility to **Public** and link the package to the probara
   repository, like the other `probara-*` packages.
3. Verify anonymously: `docker logout ghcr.io && docker manifest inspect
   ghcr.io/yassinebenameur/probara-discovery-collector:<tag>`, then re-run the
   failed leg (its image is already pushed; the check passes).

### Collector settings

The generated manifest sets `PROBARA_URL` and `PROBARA_DISCOVERY_ID` and mounts
the token Secret at the default `PROBARA_DISCOVERY_TOKEN_FILE` path; you only
need these when writing your own manifest:

| Variable | Default | Constraint |
|---|---|---|
| `PROBARA_URL` | none (required) | HTTPS origin of the Probara API, with no path, query or credentials |
| `PROBARA_DISCOVERY_ID` | none (required) | The integration's UUID |
| `PROBARA_DISCOVERY_TOKEN_FILE` | `/var/run/probara/token` | File holding the integration's collector token (the mounted Secret) |
| `SSL_CERT_FILE` | unset (the image's public CA bundle) | PEM bundle trusted for the Probara origin; only for an origin behind an internal CA |

The collector reaches the Kubernetes API through the in-cluster
`KUBERNETES_SERVICE_HOST`/`KUBERNETES_SERVICE_PORT` and the mounted
service-account CA and token.

### Probara behind an internal CA

The collector verifies the Probara origin against the public CA roots in its
image. If the origin's certificate comes from an internal CA, put that CA in a
ConfigMap in the collector's namespace (the home namespace), mount
it, and set `SSL_CERT_FILE` to the mounted file. A bundle holding only that CA
is enough: the Probara origin is the only peer verified against it (the
Kubernetes API connection uses the service-account CA).

```sh
kubectl -n <home-namespace> create configmap probara-ca --from-file=ca.crt=internal-ca.pem
kubectl -n <home-namespace> patch deployment probara-discovery-<integration-prefix> -p '{"spec":{"template":{"spec":{
  "volumes":[{"name":"probara-ca","configMap":{"name":"probara-ca"}}],
  "containers":[{"name":"collector",
    "env":[{"name":"SSL_CERT_FILE","value":"/etc/probara-ca/ca.crt"}],
    "volumeMounts":[{"name":"probara-ca","mountPath":"/etc/probara-ca","readOnly":true}]}]}}}}'
```

The generated manifest does not include this, so reapply the patch after
applying a regenerated manifest (reinstalling rotates the token).

Apply the downloaded file using the intended kubeconfig context:

```sh
kubectl --context your-cluster apply -f probara-discovery-<integration-prefix>.yaml
kubectl --context your-cluster -n <home-namespace> logs \
  deployment/probara-discovery-<integration-prefix>
```

Remove the downloaded credential file after installation. The generated
Deployment uses one replica, a non-root container, read-only root filesystem,
resource limits, and a mounted Secret. Role grants are **list Services and
Ingresses only**, scoped to the configured namespaces. The collector does not
read Kubernetes Secrets, Pods, logs, or workloads, and cannot modify cluster
resources. No Kubernetes credentials, tenant API key, NATS credential, or
PostgreSQL credential is sent to Probara or included in the snapshot.

The collector needs outbound HTTPS to the Kubernetes API and Probara API.
The generated manifest does not change your network policy. Private application
checks still require a separately deployed [location worker](location-worker-auth.md)
that can resolve/reach the discovered targets and whose egress policy allows them.
The discovery collector is not a check worker.

## What is discovered

- Ingress rules with concrete DNS hostnames propose HTTP checks of `/`. HTTPS
  is selected when `spec.tls[].hosts` lists that host or a wildcard covering it
  (`*.example.com` covers `api.example.com`, not `a.b.example.com`). Wildcard and catch-all
  rules remain inventory entries but do not produce guessed targets. The review
  form lets you choose an expected HTTP status. Edit a resulting monitor to add
  other paths, credentials, redirects, TLS expiry thresholds, or assertions.
- Services propose TCP connectivity checks for declared TCP ports. UDP and
  ExternalName services do not receive automatic suggestions. Connectivity alone
  does not establish authenticated database/broker or application health.
- `probara.io/health-path` on a Service replaces the matching TCP suggestion with
  an HTTP health check. Use `probara.io/health-port` when there is more than one
  port, and optional `probara.io/health-scheme: https`. Paths must start with `/`
  and cannot contain a query or fragment. Do not put secrets in annotations. Malformed or oversized health-path hints
  fall back to TCP suggestions; invalid health-port hints do too. Grouping values
  are trimmed, and values longer than 100 bytes are ignored. Invalid optional
  hints therefore do not prevent the rest of the inventory from refreshing.
- `probara.io/discover: "false"` excludes a resource. Resources are otherwise
  included by default within the configured namespace allowlist.
- `probara.io/service: payments` groups resources in the **same namespace and
  integration** under one service. Without this annotation, the identity is
  namespace/kind/name. Resources retain Kubernetes UIDs and first/last-seen times.
  A recreated resource with a new UID can therefore join the same service while
  the old resource remains marked missing (until it is pruned after 7 days).
  Changing the grouping annotation moves the resource; existing monitor links
  are not silently reassigned. The old service, now empty, is deleted at once
  when nothing links to it; with links it stays and is reported missing (grace,
  then missing) until 7 days after it last had a present resource, when it is
  deleted with its links. Its expanded row says its resources moved: link the
  monitors to the new service and unlink them from the old one. Reverting the
  grouping within that time moves the resources back and clears the missing
  state.

Example Service annotations:

```yaml
metadata:
  annotations:
    probara.io/service: payments
    probara.io/health-path: /readyz
    probara.io/health-port: "8080"
    probara.io/health-scheme: http
```

Apply the same `probara.io/service` annotation to the corresponding Ingress if
you want its external and internal checks reviewed together.

## Approve or link

Each suggestion explains its source. Existing monitors of the same target are
offered as **candidates**, never linked automatically. Matching folds host case,
a trailing root slash, an explicit default port, and the `.cluster.local`
suffix of a Service name, so `https://app.example.com` matches the suggestion
`https://app.example.com/`. A match does not prove that the
existing monitor has the correct assertions or location. You can also link a
monitor manually from the monitor picker (searches every monitor by name or target), without matching a suggestion.

Approval selects a name, interval (30–86,400 seconds), execution location, HTTP
expected status when applicable, and workspace-default or explicit active-channel
routing. It creates an enabled monitor with a 10-second timeout and two-failure
confirmation. Internal Service targets require an enabled private location in
the same tenant. HTTP checks of an Ingress can run from the default fleet or a
private location; an Ingress hostname does not imply public accessibility.

Creation, initial state interval, location/channel assignments, and the inventory
link commit atomically. Repeating an approval for an already linked suggestion
returns the existing monitor with `"created": false` (the first approval
returns `"created": true`); a replay writes nothing and records no
`discovery.approve` audit event. An exact target/location match is refused with a
request to link the existing monitor. This is conservative duplicate prevention
for discovery approvals, not a global uniqueness constraint on monitor CRUD.

Unlinking does not delete the monitor. Removing an integration deletes its
inventory and links, but retains monitors and result history. Retention (see
below) deletes inventory rows and, for a long-empty service, its links, never a
monitor. There is no target rewrite when a resource changes or disappears.

## Change namespaces

As an editor or admin, select **Edit cluster** (the pencil in the cluster
header) to rename the integration, change its default private location, or add
and remove namespaces. An integration has at most 20 namespaces, active and
pending together. The home namespace is locked: the collector's Deployment,
ServiceAccount and Secret live there, so moving the collector means connecting
a new integration and explicitly linking existing monitors to it. A name or
location change saves at once; a namespace change goes through a review step
that states what each change does, with counts from the namespaces endpoint.
Edits take effect at the next scan (at most five minutes; the collector reads
its configuration every cycle, so it needs no restart).

**Adding** a namespace makes it **pending**. Nothing else changes until the
collector can read it. Each cycle scans the active namespaces first, then each
pending one all-or-nothing: a failure only skips that namespace, with a reason,
and every other namespace keeps syncing. The first snapshot that scans it in
full promotes it to active, and its services join coverage. Until then the
Discovery page shows a pill per pending namespace ("waiting for first scan", or
the skip reason with when it was checked) and a warning with the access
manifest. An active namespace's pill shows its service count, and one that is
still empty after a scan is flagged "no services" (a misspelled namespace under
cluster-wide RBAC would scan fine and find nothing). The collector logs each
skip as `Pending namespace <ns> skipped (<reason>): <hint>`.

| Reason | Meaning |
|---|---|
| `forbidden` | Kubernetes answered 401 or 403: the namespace does not exist, or the access manifest is not applied there. |
| `too_large` | A list page over 4 MiB, or the namespace would take the snapshot past 2,000 resources or the 4 MiB upload cap. The collector fits pending namespaces into the upload in scan order, so a smaller one after it can still be promoted. |
| `inventory_limit` | Promoting it would leave the integration storing more than 10,000 resources (assigned by the API; the active namespaces were still applied). |
| `deadline` | The scan reached the last 30 seconds of its two-minute deadline before this namespace (kept for the upload). |
| `error` | Any other failure: another HTTP status, an unreadable or invalid list response, a transport error. |

A skipped namespace stays pending and is retried every cycle. The **access
manifest** (Apply step of the edit, or the warning's **Access manifest**
button) holds only a Role and RoleBinding per configured namespace, bound to
the collector's ServiceAccount in the home namespace. It has no Secret and no
token, so it can be applied any number of times without rotating the
credential:

```sh
kubectl --context your-cluster apply -f probara-discovery-<integration-prefix>-access.yaml
```

**Removing** a namespace takes it out of coverage at once: summary, totals,
search and paging leave its services out, and approving a suggestion or
linking a monitor on them is refused (404). The next complete scan marks its
resources missing, and retention deletes them, with their services and monitor
links, 7 days after they went missing. Monitors and their history are never
touched. Adding the namespace back within those 7 days brings back the same
services and links once it is promoted again (while it is pending, its stored
resources stay missing and out of coverage). The collector's Role in a removed
namespace is not deleted for you; the Apply step gives the cleanup command:

```sh
kubectl --context your-cluster -n <removed-namespace> delete role,rolebinding \
  probara-discovery-<integration-prefix> --ignore-not-found
```

Edits are deltas, so two editors (or an MCP agent) merge rather than overwrite
each other: adding a namespace that is already active or pending, or removing
one that is not configured, does nothing. An edit does not change the inventory
revision, so it never conflicts with a scan in flight; a scan that raced a
removal is refused with 409 and `Retry-After: 10` and the collector retries
with its new configuration.

## Interpret coverage

| Finding | Meaning |
|---|---|
| No enabled monitor linked | No explicit link points to a nondeleted, enabled monitor. An unlinked match remains a candidate. |
| No recent results | An enabled linked non-group monitor has no result received by the platform within its S-F1 freshness window: `max(90 seconds, 3 × interval)`, or `interval + grace_period_seconds` for push monitors (`monitorstate.EvidenceWindowSeconds`, the same window the absence watchdog uses). Like the watchdog, the age runs from the last result, or from monitor creation before the first one, so a just-approved monitor is fresh. Worker-reported timestamps are not used. |
| No reachable alert channel | The shared alert-routing rules find no active destination, including group roll-up rules. This does not test delivery to the external provider. |
| Group freshness not assessed | Group state is derived rather than checked. Link leaf checks to assess their result freshness. |
| Inventory stale | No complete snapshot has arrived for 15 minutes, or no snapshot has ever arrived (database clock). New approvals are blocked; linking existing monitors is not. It is a fact of the whole integration: the response reports it once (`inventory_stale`) and prefixes it to every row, but the summary counts, the gaps-only filter and the gaps sort ignore it. |
| Resource missing, grace | All resources for the service are absent from a complete snapshot, with at least one missing for less than 15 minutes, or the service's last present resource went missing or moved away (a grouping change) less than 15 minutes ago. Counted in `summary.missing_grace`. |
| Resource missing | All resources have remained absent past the grace period. Counted in `summary.missing`. Monitors remain intact. |

Observed monitor health is displayed separately from coverage. A failing but
fresh, routed monitor can have no gaps in these assessed dimensions. A successful
monitor with broken routing does have a gap. Missing inventory is not inferred
from failed scans. Health for stale checks is displayed as no recent evidence.

The first release does not assess functional journeys, expected geographic
redundancy, dependency completeness, notification-provider delivery, ownership,
or criticality. A generic linked monitor does not certify every discovered
endpoint. Inspect suggestions to assess additional targets. No composite score
is shown. The summary counts every service in the integration's active
namespaces (a pending namespace joins at its first full scan, a removed one
leaves at once); search, the
gaps-only filter and sorting apply across all services before paging (50 per
page). The setup location and channel pickers show the
first 100 entries; configure other choices from the regular monitor editor.

## Reconciliation and limits

The collector runs immediately, then five minutes after each completed cycle.
Each cycle has a two-minute deadline and 30-second request timeout. It reloads
projected Kubernetes and Probara tokens; HTTPS certificate checks stay enabled
and redirects are refused. Kubernetes list pagination is followed to completion.
A forbidden list, expired continuation, oversized page, or failed namespace
aborts the whole scan when the namespace is active; it never uploads a partial
inventory as a complete one. A pending namespace (added, not scanned
successfully yet) is scanned after the active ones and all-or-nothing: on any
failure its resources are dropped from the snapshot and it is reported in
`skipped` with a reason instead (see [Change namespaces](#change-namespaces)).

Snapshots carry a server-issued inventory revision. The API checks that revision
and the namespace set under an integration row lock (every active namespace
scanned, otherwise only pending ones, each once; `skipped` names only pending
namespaces that were not scanned), validates the whole payload, then replaces
the observed membership in one transaction. Out-of-order or replayed revisions
return 409. A snapshot that scanned a namespace removed meanwhile returns 409
with `Retry-After: 10`, so the collector re-reads its configuration within
seconds. An empty **valid** snapshot marks everything
missing; malformed or incomplete uploads do not. Limits are 20 integrations per
workspace, 20 namespaces per integration (active and pending together), 2,000
resources per scan, 32 suggestions per resource,
4 MiB per Kubernetes page and uploaded snapshot. The collector retains the first
32 distinct suggestions per resource; this is a bounded suggestion list, not
proof of full endpoint coverage.

The API processes at most one snapshot per minute per integration
(`discovery_integrations.last_attempt_at`): applied ones, and ones refused with
422 `inventory_limit` after doing the reconciliation work, so a refusal cannot
be repeated back to back while holding the integration lock. An earlier one
gets 429 `rate_limited` with `Retry-After` in whole seconds; the token and this
limit are checked before the body is read, and again under the row lock. The
cheap refusals (400, 409) do not count. The
collector waits for `Retry-After` (bounded to 1 second–1 hour) on 409, 429 or
503 instead of its five-minute interval (a stale-revision 409 carries none, so
it waits the interval), and logs every refusal with its status and
up to 512 bytes of the response body. Unknown JSON fields in a snapshot are
ignored, so a newer collector image keeps syncing with an older API.

Retention, applied in the snapshot transaction (constants in
`api/internal/services/discovery/service.go`, all ages on the database clock):

- A resource keeps the time it first went missing; once that is more than
  7 days ago (`MissingRetention`) the resource is deleted.
- A service is absent from the first complete snapshot without a present
  resource of it (`discovered_services.absent_since`, maintained before
  resources are pruned). Left with no stored resources, it is deleted at once
  when no monitor is linked to it. A linked one stays (reported missing after
  its 15-minute grace) until it has been absent for 7 days, then is deleted
  with its links. One emptied by the 7-day pruning alone has been absent that
  long already, so it goes with its links straight away; one whose last present
  resource moved away gets the full 7 days, even if a sibling resource is
  pruned in the same snapshot.
- An integration stores at most 10,000 resources (`MaxStoredResources`),
  present and missing together, counted after reconciliation and pruning. A
  snapshot that would exceed it is refused with 422 `inventory_limit` and
  rolled back (inventory, revision and last-seen time unchanged; only the
  rate-limit attempt time is kept). When the snapshot includes pending
  namespaces, it is not refused: the active namespaces are reconciled alone and
  those pending namespaces stay pending with reason `inventory_limit`. To free
  room, wait for retention, remove namespaces (their resources still count
  until they are pruned, 7 days after they go missing), or connect a
  replacement integration with fewer namespaces.
- Monitors and their history are never deleted.

The reinstall action rotates the collector credential: reapply its new manifest
before expecting scans to resume. Rotation invalidates old snapshot revisions.
The reinstall manifest grants access to active and pending namespaces alike.

## API

Normal endpoints use existing tenant authentication and write permissions:

- `GET/POST /api/v1/discovery/integrations`
- `PATCH /api/v1/discovery/integrations/{id}` (`name`, `location_id`,
  `add_namespaces`, `remove_namespaces`; see below)
- `GET /api/v1/discovery/integrations/{id}/namespaces`
- `DELETE /api/v1/discovery/integrations/{id}`
- `POST /api/v1/discovery/integrations/{id}/rotate-token`
- `GET /api/v1/discovery/integrations/{id}/coverage?page=1&q=&gaps=true&sort=name|gaps`
  (`q` matches service name or namespace; `summary` is unfiltered)
- `POST /api/v1/discovery/services/{id}/approve`
- `POST /api/v1/discovery/services/{id}/links` (`monitor_id`, optional suggestion `key`)
- `DELETE /api/v1/discovery/services/{id}/links/{monitorID}`

Each of these is also an MCP tool (`discoveryTools` in
`api/internal/mcp/catalog_ops.go`), dispatched through the same router, so the
same authentication and write gate apply. The collector routes below are in
the catalog's `notExposed` list.

Collectors use only their scoped bearer token on:

- `GET /api/v1/discovery-collectors/{id}/config`
- `POST /api/v1/discovery-collectors/{id}/snapshot`

An integration carries `namespaces` (active), `home_namespace`,
`pending_namespaces` (always an array) and `pending_status` (always an object,
`{"<ns>": {"reason": …, "checked_at": …}}` for pending namespaces with a
recorded skip).

`PATCH …/integrations/{id}` takes any of `name` (trimmed, 1–100 characters),
`location_id` (an enabled location of the workspace, or `null` to clear;
absent keeps it), `add_namespaces` and `remove_namespaces` (each at most 20
valid namespace names; a name cannot be in both, and the home namespace cannot
be removed). Unknown fields, including a full `namespaces` list, are refused
with 400. It returns 200 with the integration (never the token), 400
`validation_error` for an invalid edit, a duplicate name, or more than 20
namespaces active and pending together, and 404 for an unknown integration.
A request that changes nothing returns 200 and records no audit event; an
effective one records `discovery-integration.update` with `details.added`,
`details.removed` (effective changes only), `details.name_changed` and
`details.location_changed`.

`GET …/integrations/{id}/namespaces` returns `items`: the home namespace first,
then the other active ones in stored order, then the pending ones, each with
`namespace`, `state` (`active` or `pending`), `home`, `reason` and
`checked_at` (only for a pending namespace with a recorded skip), `services`
(stored discovered services in the namespace, present or missing) and
`linked_services` (those linked to a monitor that is not deleted). The counts ignore state, so a
re-added namespace shows what it brings back.

The collector's `GET …/config` returns `{namespaces, pending, revision}`; its
snapshot adds `skipped` (`{"<ns>": "<reason>"}`, omitted when empty; an
unknown reason is stored as `error`). The snapshot route answers 204 when
applied, 401 for a missing or malformed bearer, 404 for an unknown integration
or wrong token, 400 for an invalid body (including one that leaves out an
active namespace), 409 for a stale revision, 409 with `Retry-After: 10` when it
scanned a namespace that is no longer configured, 429 `rate_limited` and 422
`inventory_limit` (see above). `POST /api/v1/discovery/services/{id}/approve` returns
`{"monitor_id": …, "created": true|false}`. In the coverage response,
`summary.missing` counts `resource_missing` only and `summary.missing_grace`
counts `resource_missing_grace`.

A collector token cannot use ordinary authenticated APIs or create monitors.
Only its SHA-256 hash is stored (`discovery_integrations.token_hash`); the
plaintext is shown only at creation or rotation. If a manifest or token leaks,
rotate the token (or delete the integration), apply the new manifest, and
review the inventory and recent approvals for resources that do not exist in
the cluster. Read-only users can inspect coverage but cannot obtain/rotate
credentials or mutate inventory links. Snapshot data is untrusted and is never
executed as a check without operator approval. The API
derives which targets are internal instead of trusting the collector: every
Service suggestion (which must target that Service's own
`<name>.<namespace>.svc` address), plus Ingress hosts that are single-label,
IP literals, or end in `.svc`, `.cluster.local`, `.local` or `.internal`.
Internal targets can only be approved with a private location. Ingress
suggestions must be `http(s)://host/` without a port or path.

The internal flag is a routing hint, not an SSRF control. It is decided from
the name alone, so a public-looking Ingress host that resolves to a private
address is classed external and can be approved onto the shared fleet. Set
`HTTP_BLOCK_PRIVATE_IPS=true` on every fleet that runs discovered checks (the
code default is `false`), and give a private location only the cluster ranges
it needs through `HTTP_ALLOWED_CIDRS`.

## Verify

```sh
go test -race ./shared/discovery ./cmd/discovery-collector \
  ./api/internal/services/discovery ./api/internal/handlers/discovery
go test -race ./api/internal/services/monitors ./api/internal/api
npm --prefix web test
npm --prefix web run lint
npm --prefix web run typecheck
npm --prefix web run build
```

Database tests use disposable PostgreSQL instances and require Docker. Set
`PROBARA_TEST_POSTGRES_DSN` for a dedicated existing test PostgreSQL server instead.

For a real-cluster acceptance test, use a disposable namespace: deploy a small
HTTP service with the health annotations and an Ingress; connect that namespace;
verify the internal and external suggestions; approve the internal check with a
reachable private location; re-submit approval and verify the monitor ID stays
the same and `created` is `false`; unlink and relink it; remove the Service and verify missing/grace state
after the next scan; restore it and verify service identity and monitor history
remain. Revoke the collector's Ingress-list permission and confirm inventory is
retained, then becomes stale rather than marking all resources missing.
Add a namespace that has a Service but no Role: its pill shows it cannot be
read while the others keep syncing; apply the access manifest and confirm it is
promoted at the next scan. Remove a namespace with a linked monitor: its
services leave coverage and the monitor stays; add it back and confirm the link
returns.
