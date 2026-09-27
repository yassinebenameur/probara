# Location worker authentication (design)

Status: **proposed** (2026-07-11). No code changes yet. Addresses the P1
finding that private-location workers share one global, anonymous NATS
identity, allowing cross-location job consumption and cross-tenant result
forgery.

## Problem / threat model

The multi-location feature ships worker processes to customer networks (e.g.
Zaion's per-VPC fleets). Each worker only needs NATS reachability, so
`PUBLIC_NATS_URL` is exposed beyond the platform cluster. Today that NATS
endpoint is **anonymous and unauthorized**:

- `shared/queue/nats.go:63` — `nats.Connect(natsURL, reconnectOptions()...)`
  with no user/pass, token, nkey, JWT, or TLS. The server runs bare
  (`docker-compose.yml:30` publishes 4222; the Helm StatefulSet args are just
  `-js -m 8222 -sd /data`). No `nats.conf`, no accounts, no users anywhere.
- A worker's location is **self-asserted**: it picks its JetStream filter
  subject from `WORKER_LOCATION_ID` (`worker/internal/worker/worker.go:149-154`).
  Nothing binds the connection to that location.
- Results ingest **trusts the payload verbatim**: `tenant_id`, `monitor_id`,
  `location_id` are read straight from the message
  (`scheduler/internal/ingest/ingest.go:172-227`, `buildResult` at 229+). Same
  for heartbeats (`ingest/heartbeat.go`) and mesh edges (`ingest/mesh.go`).

Given one credential is handed to every private location, any location host —
or anyone who can reach the exposed 4222 — can:

1. **Steal jobs** — subscribe to / drain another location's
   `check.jobs.loc.<id>` or the default work queue, silently blackholing
   monitoring (JetStream work-queue = at-most-once delivery, so an attacker
   ack'ing a job means the real worker never sees it).
2. **Forge results** — publish to `check.results` claiming any tenant/monitor/
   location, faking `up` (hiding real outages) or `down` (false alerts, paging).
3. **Spoof liveness** — publish `locations.heartbeat` for any location, or fake
   mesh edge state.
4. **Read cross-tenant data** — job payloads carry monitor config (URLs,
   headers, and for some types decrypted-at-worker secrets).

This is a cross-tenant integrity break, not merely a location-isolation gap.
It was accepted as a known follow-up at multi-location GA ("per-location NATS
creds are a hardening follow-up", see `docs/multi-location-and-product-strategy.md`).

## Design overview

Three layers, defense in depth. Layer 1 (server authz) is the load-bearing
control; layers 2–3 make identity verifiable and enforced platform-side.

### Layer 1 — NATS server accounts + subject-scoped authorization

Enable NATS authorization so each connection has an identity and per-subject
publish/subscribe permissions enforced **by the server**. Two accounts:

- **`platform`** — used by api, scheduler, alerter, status-page, and the
  in-cluster default worker fleet. Full publish/subscribe on `check.*`,
  `checks.*`, `locations.*`, and JetStream API. This is the trusted core.
- **`locations`** — one *user* per location. A location user `loc-<uuid>` is
  permitted:
  - subscribe: `check.jobs.loc.<uuid>`, `checks.test.loc.<uuid>` and the
    JetStream pull-consumer API subjects scoped to its durable
    (`$JS.API.CONSUMER.MSG.NEXT.CHECK_JOBS.check-workers-loc-<uuid>`,
    `$JS.ACK.CHECK_JOBS.check-workers-loc-<uuid>.>`).
  - publish: `check.results`, `checks.test.loc.<uuid>` (reply),
    `locations.heartbeat`.
  - **cannot** subscribe to `check.jobs.default`, another location's subject,
    or the raw stream; **cannot** create/delete consumers.

Cross-account visibility (platform ↔ locations) is granted with account
**exports/imports**: the `platform` account exports the job stream's
per-location delivery and imports the results/heartbeat subjects the location
account publishes. This keeps locations unable to see each other while the
platform sees all.

Note the subject grant still can't stop a location from *claiming* a wrong
`location_id` **inside** a `check.results` payload (the subject is the same
`check.results` for everyone). That is what Layer 3 closes.

### Layer 2 — Per-location credential lifecycle

Provision a credential per location and surface it through the existing deploy
flow. Reuse the scoped-API-key primitives from enterprise auth rather than
inventing new crypto.

- **Storage.** New migration `000077_add_location_credentials` adding to
  `locations`: `nats_user` (text, = `loc-<uuid>`), `credential_hash` (bcrypt,
  via `shared/auth.HashAPIKey`), `credential_prefix` (SHA256 prefix for fast
  lookup, mirroring `000007`/`000076` on `api_keys`), `credential_issued_at`,
  `credential_rotated_at`. The plaintext secret is shown **once** at generate/
  rotate time, never stored.
- **Credential form.** Decision below (nkey/JWT vs token). Whichever, the
  secret is generated in `api/internal/services/locations`, persisted as
  hash+prefix, and returned once.
- **Rotation & revoke.** `POST /locations/{id}/credential:rotate` issues a new
  secret and invalidates the old; deleting/ disabling a location revokes it.
  With NATS JWT + account resolver, revocation is immediate server-side; with a
  static token file it takes effect on the location's next reconnect.
- **Deploy snippet.** `api/internal/services/locations/deploy.go:31` currently
  emits only `NATS_URL` + `WORKER_LOCATION_ID`. Add the credential (a mounted
  `.creds` file for nkey/JWT, or `NATS_CREDENTIAL` env for token) to the docker
  run / compose output and to `LocationDeployInfo`.
- **Helm.** `deployment-worker-location.yaml` gains a credential env/volume
  sourced from a Secret; `values.yaml` `worker.locations[]` entries carry a
  secret ref.
- **Client.** `shared/queue.NewClient` learns an auth option
  (`nats.UserCredentials(path)` or `nats.Token(...)`), wired from a new
  `NATS_CREDENTIAL` / `NATS_CREDS_FILE` config key in
  `shared/config/config.go:235-240`.

### Layer 3 — Ingest-side identity binding

Even with Layer 1, the shared `check.results` subject means the platform must
verify the *claimed* identity against the *authenticated* connection.

- The results-ingest consumer runs in the trusted `platform` account, so it
  cannot read the publishing location user directly from the subject. Bind
  instead by requiring the worker to authenticate the payload:
  - **Preferred:** the worker signs each `CheckResultMessage` (and heartbeat /
    mesh result) with its location credential; ingest looks up the location by
    `location_id`, loads `credential_prefix`/`hash`, and verifies. Reject on
    mismatch. This makes `location_id` forgery-resistant without relying on
    subject scoping.
  - **Minimum:** if signing is deferred, at least reject results whose
    `location_id` is unknown/disabled and whose `tenant_id` doesn't match the
    location's tenant (`locations.tenant_id`). This blocks cross-tenant forgery
    but not same-tenant cross-location spoofing.
- Apply the same check to `ingest/heartbeat.go` and `ingest/mesh.go`.
- Metric + structured log on every rejection (`ingest_rejected_total{reason}`)
  so tampering is observable.

## nkey vs JWT vs static token — recommendation

| Option | Provisioning | Revocation | Per-location scoping | Ops cost |
|---|---|---|---|---|
| **Static token** (per-user token in `authorization.users`) | rewrite `nats.conf` + reload on every location add | reload | yes, but config grows unbounded | low now, bad at scale |
| **nkey** (seed per location) | `nats.conf` `authorization.users` with nkey pubkeys | reload | yes | medium; still config-file churn |
| **JWT + nats-account-resolver** | operator/account/user JWTs; users minted by API, pushed to resolver | **immediate**, server-side | native (permissions in the user JWT) | higher setup, best at scale |

**Recommendation: JWT + memory/NATS account resolver.** It is the only option
where adding/rotating/revoking a location is an API operation with no server
config reload, and permissions travel in the signed user JWT — a natural fit
for "one user per location, minted on demand." We already mint per-entity
secrets for API keys, so the operator→account→user hierarchy is a modest
extension. Ship an **operator + two accounts** (`platform`, `locations`); the
API service holds the `locations` account signing key and mints location user
JWTs at credential-generate time.

If JWT is judged too heavy for v1, the **nkey** fallback is acceptable *only*
with Layer 3 signing in place (so identity is still verified platform-side) and
a documented config-reload path. A plain shared token is **not** acceptable —
it is the status quo with extra steps.

## Rollout ordering

NATS work-queue streams forbid a filterless consumer coexisting with filtered
ones — the constraint that already dictates
`scale workers to 0 → scheduler → new workers`
(see `scheduler/internal/scheduler/scheduler.go:260-264`,
`CHECK_JOB_LEGACY_CONSUMERS`). Enabling auth adds an auth cutover on top:

1. **Migration + API** deploy first (adds credential columns; endpoints to
   generate/rotate). No behavior change while NATS is still open.
2. **Generate credentials** for every existing location; distribute out of band
   (they'll be needed before the server flips to auth-required).
3. **Enable NATS auth** in permissive/verify-mode if the deployment allows it
   (log unauthenticated connections without dropping), to catch stragglers.
4. **Flip auth-required** during a maintenance window: workers scaled to 0,
   scheduler restarted with platform creds, then workers redeployed with their
   per-location creds. Ordering is: platform services (with platform creds) →
   default worker fleet → per-location workers.
5. **Enable Layer 3** ingest rejection last (after confirming all live workers
   sign / carry correct identity), else valid results get dropped mid-cutover.

Because auth-required is a hard cutover for external locations, coordinate the
window with affected tenants; a stale-credential worker fails closed (can't
publish results → location goes `connected=false` after 60s, monitors degrade
to unknown for that location rather than silently wrong).

## Verify coverage

Extend `scripts/verify-auth.sh` (or a new `scripts/verify-location-auth.sh`)
with checks that, against a locally auth-enabled NATS:

- a location cred can subscribe to its own job subject but **not** another's
  nor `check.jobs.default`;
- a location cred **cannot** publish a `check.results` for a different tenant's
  monitor (Layer 3 rejection, asserted via `ingest_rejected_total`);
- rotate invalidates the old cred;
- an anonymous connection is refused.

## Deliberately deferred

- TLS/mTLS on the NATS listener (transport confidentiality) — separate, larger
  op; job payloads still contain sensitive config until then, so this should be
  the very next follow-up.
- Fine-grained per-monitor authorization (a location can still publish results
  for any monitor *assigned to it*; we don't verify assignment at ingest).
- Rate limiting / quota per location account.

## Key file references

- Connect seam (add auth here): `shared/queue/nats.go:62-80`.
- Config (add credential key): `shared/config/config.go:235-240`.
- Worker subject selection: `worker/internal/worker/worker.go:149-165`.
- Ingest trust points: `scheduler/internal/ingest/ingest.go:172-227`,
  `ingest/heartbeat.go`, `ingest/mesh.go`.
- Deploy snippet: `api/internal/services/locations/deploy.go:31-64`.
- Helm location worker: `helm/monitoring-platform/templates/deployment-worker-location.yaml:52-70`.
- NATS server: `docker-compose.yml:22-40`,
  `helm/monitoring-platform/templates/nats-statefulset.yaml`.
- Reusable crypto: `shared/auth/auth.go` (HashAPIKey/CompareAPIKey/prefix),
  migration pattern `shared/db/migrations/000076_add_api_key_scope_expiry.up.sql`.
