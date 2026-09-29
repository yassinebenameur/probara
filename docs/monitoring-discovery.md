# Kubernetes discovery and monitoring coverage

Open **Discovery** in the operator UI. Discovery proposes monitors; it never
creates them automatically. Coverage reports observed inventory, explicitly
linked monitors, result freshness, and configured notification reachability.
It is not a measure of all your infrastructure or an application-quality score.

## Install

1. Upgrade the API and apply migration `000098_monitoring_discovery` using the
   normal migration workflow. Deploy the web application from the same revision.
2. As an editor or admin, select **Connect cluster**. Enter a cluster name and
   1–20 existing namespaces. Optionally select an existing private check location.
3. Enter the externally reachable **HTTPS API origin** and the collector image.
   Download the generated manifest. Its Secret contains an integration-scoped
   random token, available only at creation or rotation. Keep the file private.
4. Apply it to the intended cluster. The collector runs in the first selected
   namespace; each selected namespace receives a Role and RoleBinding.
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

The collector reaches the Kubernetes API through the in-cluster
`KUBERNETES_SERVICE_HOST`/`KUBERNETES_SERVICE_PORT` and the mounted
service-account CA and token.

Apply the downloaded file using the intended kubeconfig context:

```sh
kubectl --context your-cluster apply -f probara-discovery-<integration-prefix>.yaml
kubectl --context your-cluster -n <first-namespace> logs \
  deployment/probara-discovery-<integration-prefix>
```

Remove the downloaded credential file after installation. The generated
Deployment uses one replica, a non-root container, read-only root filesystem,
resource limits, and a mounted Secret. Role grants are **list Services and
Ingresses only**, scoped to the selected namespaces. The collector does not
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
  the old resource remains marked missing. Changing the grouping annotation
  moves the resource; existing monitor links are not silently reassigned.

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
returns the existing monitor. An exact target/location match is refused with a
request to link the existing monitor. This is conservative duplicate prevention
for discovery approvals, not a global uniqueness constraint on monitor CRUD.

Unlinking does not delete the monitor. Removing an integration deletes its
inventory and links, but retains monitors and result history. There is no
automatic deletion or target rewrite when a resource changes or disappears.

## Interpret coverage

| Finding | Meaning |
|---|---|
| No enabled monitor linked | No explicit link points to a nondeleted, enabled monitor. An unlinked match remains a candidate. |
| No recent results | An enabled linked non-group monitor has no result received by the platform within its S-F1 freshness window: `max(90 seconds, 3 × interval)`, or `interval + grace_period_seconds` for push monitors (`monitorstate.EvidenceWindowSeconds`, the same window the absence watchdog uses). Worker-reported timestamps are not used. |
| No reachable alert channel | The shared alert-routing rules find no active destination, including group roll-up rules. This does not test delivery to the external provider. |
| Group freshness not assessed | Group state is derived rather than checked. Link leaf checks to assess their result freshness. |
| Inventory stale | No complete snapshot has arrived for 15 minutes, or no snapshot has ever arrived. New approvals are blocked. |
| Resource missing, grace | All resources for the service are absent from a complete snapshot, with at least one missing for less than 15 minutes. |
| Resource missing | All resources have remained absent past the grace period. Monitors remain intact. |

Observed monitor health is displayed separately from coverage. A failing but
fresh, routed monitor can have no gaps in these assessed dimensions. A successful
monitor with broken routing does have a gap. Missing inventory is not inferred
from failed scans. Health for stale checks is displayed as no recent evidence.

The first release does not assess functional journeys, expected geographic
redundancy, dependency completeness, notification-provider delivery, ownership,
or criticality. A generic linked monitor does not certify every discovered
endpoint. Inspect suggestions to assess additional targets. No composite score
is shown. The summary counts every service of the integration; search, the
gaps-only filter and sorting apply across all services before paging (50 per
page). The setup location and channel pickers show the
first 100 entries; configure other choices from the regular monitor editor.

## Reconciliation and limits

The collector runs immediately, then five minutes after each completed cycle.
Each cycle has a two-minute deadline and 30-second request timeout. It reloads
projected Kubernetes and Probara tokens; HTTPS certificate checks stay enabled
and redirects are refused. Kubernetes list pagination is followed to completion.
A forbidden list, expired continuation, oversized page, or failed namespace
aborts the whole scan; it never uploads a partial inventory as a complete one.

Snapshots carry a server-issued inventory revision. The API checks that revision
and the complete namespace set under an integration row lock, validates the whole
payload, then replaces the observed membership in one transaction. Out-of-order
or replayed revisions return 409. An empty **valid** snapshot marks everything
missing; malformed or incomplete uploads do not. Limits are 20 integrations per
workspace, 20 namespaces and 2,000 resources per scan, 32 suggestions per resource,
4 MiB per Kubernetes page and uploaded snapshot. The collector retains the first
32 distinct suggestions per resource; this is a bounded suggestion list, not
proof of full endpoint coverage. Missing historical resources remain stored until
the integration is removed.

Configuration is immutable in this first release. To change namespaces or grouping
scope, create a replacement integration and explicitly link existing monitors.
The reinstall action rotates the collector credential: reapply its new manifest
before expecting scans to resume. Rotation invalidates old snapshot revisions.

## API

Normal endpoints use existing tenant authentication and write permissions:

- `GET/POST /api/v1/discovery/integrations`
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

A collector token cannot use ordinary authenticated APIs or create monitors.
Read-only users can inspect coverage but cannot obtain/rotate credentials or
mutate inventory links. Snapshot data is untrusted and is never executed as a
check without operator approval and normal worker network guards. The API
derives which targets are internal instead of trusting the collector: every
Service suggestion (which must target that Service's own
`<name>.<namespace>.svc` address), plus Ingress hosts that are single-label,
IP literals, or end in `.svc`, `.cluster.local`, `.local` or `.internal`.
Internal targets can only be approved with a private location. Ingress
suggestions must be `http(s)://host/` without a port or path.

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
the same; unlink and relink it; remove the Service and verify missing/grace state
after the next scan; restore it and verify service identity and monitor history
remain. Revoke the collector's Ingress-list permission and confirm inventory is
retained, then becomes stale rather than marking all resources missing.
