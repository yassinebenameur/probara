# Multi-Location Monitoring & Monitor Roadmap

**Date:** 2026-06-11
**Status:** Design discussion / roadmap — not yet implemented
**Context:** Triggered by an AWS architecture review and asking whether this product can monitor a multi-VPC AWS estate (cross-network checks, multi-location checks, network-interlink verification).

---

## 1. Current capabilities assessment

### Execution model today

Scheduler → NATS JetStream `check-jobs` stream → pool of stateless Go workers executing checks. Monitor types: HTTP, ping, DNS, gRPC, SIP, synthetic API, synthetic browser, push, agent (host metrics), group (rollup).

- Checker registry: `worker/internal/worker/registry.go`
- Frontend type registry: `MONITOR_TYPE_META` in `web/components/monitors/MonitorForm.tsx`
- Job payload: `shared/models/check_job_payload.go`

### What works for a multi-VPC architecture today

Wherever the worker is deployed is the vantage point. Deployed in a hub network (e.g. a management VPC with Transit Gateway routes to all spokes), the product can already monitor:

- **Internal load balancers and in-cluster services** via HTTP/gRPC — SSRF guard (`HTTP_BLOCK_PRIVATE_IPS`) supports an `HTTP_ALLOWED_CIDRS` whitelist for private ranges.
- **Cross-account dependencies** — e.g. an HTTP check on a registry endpoint that another environment depends on (the prod→dev ECR pull dependency from the AWS review, finding F6).
- **Public edge + TLS** — cert checks support min-days-valid (relevant wherever a wildcard cert is renewed manually).
- **Host metrics** on bastions/VPN nodes via the existing agent.

### Gaps (verified in code)

1. **No location concept.** Job payload carries only monitor ID, type, config, timeout. All workers consume one subject; first worker wins. No "run this from inside VPC X."
2. **No spatial quorum.** Only `consecutive_failures_threshold` (temporal). No "down only if M of N locations agree," no per-location results.
3. **No path-validation primitives.** No raw TCP connect check; no traceroute; DNS checker uses `net.DefaultResolver` (cannot target a VPC/internal resolver, so private zones can't be validated).
4. **The agent is metrics-only** — it reports CPU/mem/disk over REST; it cannot execute checks from inside a network.

---

## 2. Multi-location design

### Core idea: locations are worker deployments

A location is just a worker deployment with an identity — no new agent protocol needed.

1. **`locations` table + `location` (or `locations[]`) field on monitors.**
   - Scheduler publishes to `check-jobs.<location>` subjects instead of one shared subject.
   - Workers start with a `WORKER_LOCATION` env and subscribe to their own subject plus a `default` subject for unpinned monitors.
   - Remote workers only need NATS reachability (a Transit Gateway / VPN provides it). One worker Helm release per cluster/VPC/region.
   - Small change to scheduler + worker bootstrap; zero change to check logic.

2. **Multi-location fan-out + quorum.**
   - Monitors pinned to N locations → scheduler enqueues N jobs per interval.
   - Results gain a `location` column; status evaluation becomes "down if ≥M of N locations report failure."
   - Per-location latency history falls out for free — this doubles as inter-network path health.

3. **Two new check primitives.**
   - **TCP connect check** (host:port, optional TLS) — validates SG/route reachability without requiring an HTTP service on the target (databases, message queues, SSH).
   - **Custom resolver on the DNS check** (`nameserver` config field via custom `net.Resolver`) — validates internal/private-zone resolution from each location.

4. **Location liveness (build into phase 1).**
   - Worker heartbeat / last-seen per location, tracked server-side, with an alert on silence.
   - Critical for trust: if an entire site loses connectivity, its worker stops consuming jobs, so its checks go *stale* rather than red. "Location X hasn't picked up jobs for 2 minutes" distinguishes "site's links are down" from "site's checks are failing."

5. **Per-location supported check types** (column on locations table from the start) — heavy types (synthetic browser needs the Playwright image) restricted to designated locations.

**Sequencing:** location-pinning + liveness → quorum → TCP/DNS-resolver checks.

### Cross-network / bidirectional mesh checks

Each location's worker plays two roles:

- **Prober** — executes checks pinned to its location.
- **Target** — exposes a tiny echo endpoint (HTTP 200 + a TCP port) published inside its network via an internal LB.

A cross-network check is an ordinary monitor with two coordinates: *run-from location* and *target*. Example: monitor "dev → prod link" = TCP/HTTP check, `location: dev`, target = prod worker's echo endpoint over the TGW.

**Direction matters.** Security groups are stateful — return traffic is always allowed for an established connection — so `A → B` succeeding proves nothing about whether B can *initiate* into A. Asymmetric failures are the common real-world case (one-sided SG tightening, one-way route propagation). Bidirectional verification therefore requires two independent monitors per pair: full mesh = **N×(N−1) directed checks**. For 3 VPCs (dev / prod / management), that's 6 edges.

| Monitor | Runs from | Targets | A failure means |
|---|---|---|---|
| dev → prod | dev worker | prod echo | prod ingress SG / TGW route from dev side broken |
| prod → dev | prod worker | dev echo | dev ingress SG / TGW route from prod side broken |

Surfacing in the app:

- A **"VPC mesh" group monitor** containing the directed checks — existing group rollup handles aggregation; status page shows one mesh tile, expandable per edge.
- **Per-edge latency charts** (location + latency already on each result) — degradation visible before hard failure.
- **Alert rules per edge** with directional messages ("prod cannot reach management").
- Later UX nicety: a "create connectivity mesh" wizard that auto-generates the N×(N−1) monitors + group for N locations — pure composition, no new backend.
- Future: an N×N **connectivity matrix screen** (directed-edge grid, latency heatmap).

Refinements:

- **Check real services too, not just echo endpoints.** The echo mesh proves "the road is open"; cross-location checks on real targets (registry endpoints, internal NLBs) prove "the destination is alive." Echo edge + service check failing together → network; service check alone → app.
- **Test representative ports.** SG rules are per-port; an echo on a random high port can stay green while 443 between networks is blocked. One mesh edge can carry 2–3 port variants (443, 5432, 5672) matching real cross-network flows.

### What this makes the product

Location-pinning is the single primitive that turns a centralized checker into a **multi-site monitoring platform**, covering both senses with the same code path:

1. **Public vantage points** (Pingdom-style): workers in multiple regions checking public endpoints; quorum kills single-location false alerts; per-location latency shows geographic degradation.
2. **Private vantage points** (Datadog/Checkly "private locations" — their most enterprise-gated feature): workers inside private networks checking internal services, cross-VPC paths, private DNS.

Whether a worker sits in a public region or a private VPC is purely a deployment decision, invisible to the scheduler.

---

## 3. Concrete first use cases

Once location-pinning ships, immediate monitors worth creating on a typical
multi-VPC AWS estate:

- Cross-account container-registry endpoint checks from the dependent environment (supply-chain dependency)
- Directed mesh edges between environments (validates Transit Gateway / security-group trust scoping)
- VPN load-balancer reachability; Direct Connect-dependent routes
- Internal DNS: private hosted zones via the custom-resolver DNS check
- Wildcard cert expiry with min-days-valid alerting
- Internal ALBs/NLBs from the management location

---

## 4. Monitor type roadmap

Each new type is cheap to add structurally (checker in `worker/internal/worker/registry.go`, entry in `MONITOR_TYPE_META` in `web/components/monitors/MonitorForm.tsx`); the cost is config UI and assertion design.

### Tier 1 — urgent (table stakes vs. free competition)

| Type | Check | Notes |
|---|---|---|
| TCP connect | host:port + optional TLS | Prerequisite for the VPC mesh; ~1 day |
| PostgreSQL | connect + auth + optional query assertion + latency | Common in-cluster (e.g. CloudNativePG); `pgx` |
| Redis | connect + PING/command + latency | `go-redis` |
| MongoDB | connect + admin ping | Managed or in-cluster; `mongo-driver` |
| MySQL/MariaDB | same shape as Postgres | Market expectation, cheap alongside |
| RabbitMQ/AMQP | connect + auth + optional probe publish/consume | Brokers fail in ways TCP can't see |
| WebSocket | upgrade handshake + optional send/expect | Already on roadmap |
| DNS custom resolver | `nameserver` field on existing DNS check | From multi-location design; private zones |

**Cross-cutting prerequisite:** DB/broker checks put credentials in monitor configs → encrypt configs at rest + write-only secret handling in API/UI *before* shipping this tier (later: Vault/external-secret references as an enterprise gate). Shapes the config schema — don't retrofit.

### Tier 2 — strategic fills

- **Cert/domain expiry standalone** — TLS on any host:port (incl. STARTTLS), plus WHOIS domain expiry; feeds the cert/DNS estate inventory.
- **SMTP** — connect, STARTTLS, auth, optional test mail.
- **Kafka / MQTT / NATS** — same shape as AMQP; NATS check is self-relevant for self-host deployments.
- **Prometheus query** — PromQL against a Prom endpoint, threshold assertion. Bridges an existing in-cluster Prometheus (the real observability plane) into status pages/alerting; rare among uptime tools.
- **Kubernetes health** — ServiceAccount/kubeconfig: deployment replicas ready, node conditions, API-server cert expiry. K8s-first buyers hand-roll this today; pairs with private locations (worker already in-cluster).

### Tier 3 — moat types

1. **SIP REGISTER** — auth against registrar, registration latency; low effort, deepens existing OPTIONS check.
2. **Synthetic call** — real SIP INVITE + RTP, assert answered, measure post-dial delay. Browser-check equivalent for telephony.
3. **RTP/audio quality** — jitter, loss, MOS on established media.
4. **IVR journey** — call → expect prompt (STT on received audio) → DTMF → expect next prompt. "Playwright for phone trees."
5. **ASR/TTS/LLM semantic checks** — send known WAV to ASR, assert transcript; text to TTS, assert plausible audio; LLM endpoint latency + semantic assertion. Zero incumbents in "uptime for AI voice stacks". Cheaper than full call testing (HTTP + media assertions) — do early.
6. **Traceroute/path** — hop-by-hop with change detection; complements mesh ("where" vs. the mesh's "is it broken"). Lower priority.

### Order

Now: TCP + Postgres + Redis (+ config-secrets groundwork, shared "Databases" picker category). Next: RabbitMQ, Mongo, MySQL, WebSocket, cert/domain expiry. Then: Prometheus query + Kubernetes, SIP REGISTER. Strategic bets with the enterprise roadmap: ASR/TTS checks early, then synthetic call → RTP quality → IVR.
