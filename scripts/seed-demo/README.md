# seed-demo — the Northwind demo tenant

A dev-only tool that builds **Northwind**, a fictional UK retailer/logistics
company with ~150 monitors and 110 days of history, so the operator UI and the
status pages can be judged against lived-in data. The tenant is
`00000000-0000-0000-0000-00000000d3e0`.

```sh
make start-all-local      # the local stack must be running
make seed-demo            # (re)start targets in the background, then seed
make seed-demo-stop       # stop the mock targets and location workers
make seed-demo-teardown   # ...and delete the tenant and its users
```

On macOS the Make targets run both `targets` and `seed` under
`caffeinate -i`, which keeps the Mac from idle-sleeping while they run. It
does not stop a lid-close or a manual sleep. `targets` schedules its
heartbeats and OTLP exports on the wall clock, so after a wake it sends
straight away and the push and agent monitors recover within one check.
Running the binary yourself skips `caffeinate`.

Every target has its own hostname under `northwind.localtest.me`, for example
`http://checkout-api.northwind.localtest.me:19100/api/checkout/health`,
`db-orders-01.northwind.localtest.me:5432` or
`ws://realtime.northwind.localtest.me:19100/ws/basket`. `localtest.me` is a
public wildcard domain whose names all resolve to `127.0.0.1` and `::1`, so
no `/etc/hosts` entries are needed. The ports stay at 191xx because binding
port 80 needs root. Resolving the names needs working DNS: offline, every
mock-backed check fails with a lookup error. The local worker runs with
`HTTP_BLOCK_PRIVATE_IPS` unset (false), so it dials the names directly.

`seed` is idempotent. It deletes and recreates **only** its own tenant, that
tenant's audit rows, and users whose email ends in `@northwind.example`. It
refuses to run if the fixed tenant ID belongs to a tenant with another name.

## Subcommands

| command | what it does |
|---|---|
| `seed` | Creates the tenant and every object below, writes the history, and enables the monitors (~75 s). |
| `targets` | Long-running. Serves the mock endpoints, sends push heartbeats and OTLP host metrics, and supervises three real private-location workers. It reloads the tenant every 20 s and on `SIGHUP` (which `seed` sends after its commit), so re-seeding under a running `targets` needs no restart. A newly loaded push monitor beats one interval after its last seeded heartbeat (nightly jobs keep their time of day), and the first OTLP export after a re-seed comes one minute after the newest seeded agent heartbeat. |
| `teardown` | Deletes the tenant (everything tenant-scoped cascades), its audit rows and the seeded users. |

Flags such as `-db`, `-api`, `-mock-port`, `-raw-hours` and `-history-days`
are listed with `go run ./scripts/seed-demo seed -h`.

## What gets created

- **151 monitors and 11 groups across 9 teams.** The teams are storefront,
  checkout, payments, search, logistics, identity, data, platform and voice.
  They double as the dashboard group tags.
  - Monitor types: http (keyword, JSON, display fields, third-party HTTPS
    status APIs), synthetic_api, tcp, dns, ping, websocket, grpc, postgres,
    redis, rabbitmq, sip, push, agent and group.
  - mongodb, mysql and synthetic_browser monitors exist but are paused (no
    local server, no Chrome). Like the other paused monitors, they were paused
    15–26 days ago and have history up to the pause.
- **Live status mix:**
  - 3 hard-down monitors with open alerts: one acknowledged, and one with an
    open incident published on the public status page.
  - 1 monitor down inside an active maintenance window, so it has no alert.
  - 3 genuinely *degraded* multi-location monitors.
  - 6 slow monitors, 4 flaky monitors and 5 paused monitors.
  - Push and agent monitors use the UI's default 10 s timeout. The scheduler
    gives every job, push and agent included, a deadline of twice the
    timeout. With a timeout of 0, every job expired and wrote a "Job expired
    before processing" error.
  - Everything else is up.
- **History:**
  - Gapless state intervals from 110 days back (before the last closed
    quarter, so 90-day and quarterly views use the interval method).
  - Hourly and daily rollups for the whole period.
  - Raw `check_results` for the last 48 h. Each paused monitor also gets raw
    rows for its last 24 h before the pause, so the monitors list shows when
    it last checked and its trace. Pauses stay under 27 days, inside the
    30-day raw retention.
  - Latency has structure, all of it deterministic:
    - UK traffic load: a lunchtime peak and an evening shopping peak on
      weekdays, quieter weekends. Slow and customer-facing services feel
      it most.
    - A slow random walk per monitor (a few-day and a two-week wander) and
      a small fleet-wide drift.
    - Six deploy regressions that step a service's latency up 30–70% for
      2–5 days until the fix ships. Among them: catalog API, search API,
      checkout API and identity API.
    - A four-day autumn sale about a month ago that loads everything.
    - Per-location network time: Frankfurt +18 ms, Virginia +85 ms and
      Singapore +160 ms to the London origin.
  - Flaky monitors fail at a different rate each day: about a quarter of
    days are clean, most sit around 2%, and a few reach 8%.
  - A few stretches of elevated errors on otherwise healthy services
    (carrier APIs, PayPal, search during its regression, CDN during the
    sale).
  - A 38-minute Virginia outage 11 days ago: every check from us-east-1
    timed out, so the global monitors went degraded, not down.
  - Random short outages, one-check blips, and scripted events:
    - an orders-DB failover 3 weeks ago that cascades into checkout
    - search, SSO, carrier and SIP-trunk incidents
    - a payments outage sized to leave the payments SLA *at risk*
    - planned outages inside past maintenance windows
- **Alerts and incidents:**
  - ~70 resolved alerts that match the outages. Dependents of the DB
    failover carry `root_cause_monitor_id`.
  - Resolved alerts of the other kinds: latency anomaly, host metric, TLS
    expiry and mesh edge.
  - 6 resolved incidents and 1 open incident, with timelines, status-page
    publications and one AI analysis.
- **Status pages, maintenance and SLAs:**
  - 3 status pages: `northwind-status`, `northwind-partners` and
    `northwind-internal`.
  - 8 maintenance windows: past, active and upcoming.
  - 4 SLAs, with frozen reports for last month and last quarter:
    - checkout is breached for September
    - payments is at risk this month
    - storefront and voice are healthy
- **Locations:**
  - Frankfurt, Virginia and Singapore run real workers and form a live mesh.
  - The mock knows which location runs each check, so it adds that
    location's network time live, as the history does. A check carries
    nothing that names its location: there is no header, and the HTTP checker
    ignores proxy settings. But the location workers are children of
    `targets`, so the mock asks `lsof` which worker owns the connecting
    socket. One scan, about 10–20 ms, serves every request that arrived
    before it started, and it only runs for hosts that a multi-location
    monitor checks. This works for HTTP, websocket and SIP. A TCP check
    measures only the kernel's connect, which the mock cannot delay, so the
    SIP reachability monitor uses SIP OPTIONS instead.
  - Without `lsof`, only Singapore is told apart. Its strict egress policy
    (`HTTP_ALLOWED_CIDRS=127.0.0.0/8`) makes it dial over IPv4, while the
    other workers use `::1`. In that case Frankfurt and Virginia get no extra
    live latency, and their history no longer matches.
  - The 3 degraded monitors check the CDN-fronted hosts `www` and `track`.
    From Singapore, the mock plays a struggling CDN PoP that answers with a
    504 after about 3–4.5 s. Those checks fail from Singapore only, with an
    ordinary `status_code: got 504` reason.
  - Each location's mesh endpoint (`fra.mesh.northwind.localtest.me:19184`
    and so on) is a mock proxy in front of that worker's echo listener. The
    proxy holds the reply for the inter-region round trip: FRA–IAD about
    89 ms, FRA–SIN 158 ms, IAD–SIN 214 ms. The 6 h of seeded mesh history
    uses the same numbers.
  - São Paulo is disconnected and Tokyo is disabled.
- **Tenant setup:**
  - 5 users (admin, editors, viewers, one disabled) and 5 API keys (one
    expired, one revoked).
  - 4 alert channels. The tenant defaults are webhooks to private IPs, which
    the egress guard refuses quietly. The data and voice email channels are
    inactive, so some monitors show routing warnings.
  - 24 h of host metrics, generated by the same functions the live OTLP loop
    uses:
    - CPU follows the traffic load, with noise and a short spike every
      few hours. The DB host also runs a nightly 02:00 backup, which adds
      CPU, disk reads, network out and I/O wait.
    - Load averages are the 1, 5 and 15 minute means of that CPU times the
      core count.
    - Memory has small GC sawtooths and grows between deploys, then drops
      on restart.
    - Disks fill slowly and are cleaned up. The orders DB data volume moves
      between 80.6% and 84.3%, just under its 85% alert rule.
    - Network and disk I/O counters are monotonic, with a diurnal and
      weekday shape and bursts.
  - About 480 back-dated audit entries. About 50 of them fall in the last
    4 h; the rest are spread over 45 days, denser towards the present.
  - The audit rows written by seed's own API calls are moved,
    in order, to the tenant's onboarding day.

Users log in with password `Northwind-Demo-2026!`. As `admin` (a superadmin),
pick *Northwind* in the tenant switcher.

## Ports and processes

| port | what |
|---|---|
| 19100 | HTTP and websocket mock on `127.0.0.1` and `[::1]`, routed by path; the Host only matters for the CDN-fronted `www`/`track` checked from Singapore |
| 19102 | gRPC health server |
| 19110–19119 | raw TCP listeners |
| 19160–19163 | SIP OPTIONS responders (UDP) |
| 19181–19183 / 19191–19193 | location workers: mesh echo / metrics |
| 19184–19186 | mesh endpoints: the mock's WAN-delay proxies in front of the echo listeners |
| 19199 | never listened on (the "connection refused" target) |
| 6379, 5672 | `probara-seed-redis` and `probara-seed-rabbitmq` containers, started by `targets` (`-docker=false` skips them) |

Logs:
- `/tmp/probara-seed-targets.log`
- `/tmp/probara-seed-worker-<fra|iad|sin>.log`

PID file: `/tmp/probara-seed-targets.pid`. The OTLP API key is in
`/tmp/probara-seed-demo.otlp-key`.

## How the history stays consistent with the live system

The same profile table (`catalog.go`) and the same latency, load, drift
and flaky-rate functions (`realism.go`, `hosts.go`) drive the mock targets
and the history. The history runs every check through `monitorstate.Apply` and
`AggregateLocations`. As a result, the intervals, rollups, raw rows,
downtime and the final monitor row agree by construction, and live checks
continue the seeded curve without a seam.

The writer respects the invariants the services rely on:

- **Intervals:** exactly one open interval, and its state equals
  `current_state`.
- **Raw rows:** never in the future, and only inside the raw window. For a
  paused monitor, raw rows also cover the hour-aligned 24 h before its pause.
- **Rollups:**
  - Hourly rollups match the rebuild exactly wherever raw rows exist.
  - Hours with no raw rows are synthetic and are never marked.
  - Daily rollups equal SUM(hourly).
- **Ownership:** monitor and group structure goes through the API, so the
  `monitor_groups` junction, secret encryption and audit behave as they do
  for a user.

Writing about 110 days takes about a minute, so `seed` writes in two passes.
The second pass re-simulates up to the real clock just before the commit.
Without it, the newest evidence would already be stale when the monitors
are enabled. Each monitor's first live dispatch is due one interval after its newest
seeded check, so the live checks continue the seeded cadence. Dispatching
all of them at the commit put an extra check seconds after the last seeded
one, which tripped latency-anomaly alerts on fast monitors.

## Known gaps

- **Prometheus monitors are not seeded.** The local database's
  `monitors_timeout_check` constraint is missing `prometheus`: migration
  000088 is recorded but its constraint is not in effect, so create fails
  with a 500.
- **OTLP exports are audited.** Each export is a non-GET request through the
  audit middleware, so the host loop adds an `otlp.metrics` audit row every
  60 s. Real collectors do the same. Right after a seed, page 1 of the audit
  log shows the recent human activity. After about 50 minutes of `targets`,
  it holds only `otlp.metrics` rows. Use the Action filter to see the rest.
- **Live latency-anomaly alerts fire on local hiccups.** The tenant uses
  the default 120 s breach window, so a monitor checked every 30–40 s has
  enough samples for one stall of the machine (a browser or a Next.js
  compile) or one cold DNS lookup to open a latency-anomaly alert, which
  resolves a minute or two later. A 300 s window is worse: every 60 s
  monitor then has five samples and a single DNS spike on a fast TCP or
  ping check trips it.
- **Agent `system.uptime` is synthetic.** It is computed from a fixed boot
  date.
- **The scripted past is history-only.** The regressions, the sale, the
  elevated-error stretches and the Virginia outage all end at least a day
  before the seed, so live checks never repeat them. The load curve, the
  drift and the flaky day rates are functions of the monitor and the time,
  and live checks follow them.
- **TCP checks take 1–2 ms.** The TCP checker times only the name lookup
  and the connect, which the kernel finishes before the mock sees the
  connection, so the mock cannot add latency to them.
- **Name resolution shows up as spikes.** `*.localtest.me` has a 300 s TTL,
  so a few percent of live checks resolve over the network and take
  20–110 ms longer. The history adds the same spikes to about 7% of
  mock-backed checks, but how slow the lookups really are depends on the
  local network.
- **Internet targets depend on your network.** Ping, DNS and the
  third-party status pages run against the real internet. Their seeded
  latencies are what a London host sees (about 10–20 ms to the anycast
  resolvers), so a slower connection shows a step at the seed time.
- **One OTLP export carries all four hosts.** Their agent heartbeats share
  a timestamp and a latency, while the seeded ones are spread out. One
  collector per host would add four audit rows a minute.
- **Admin logins are platform-wide audit rows.** `seed` logs in through the
  API, and the login is recorded with no tenant, so the superadmin sees it
  (`auth.login`, IP `[::1]:port`) in Northwind's audit log at the time of
  every seed. `seed` leaves rows outside its tenant alone.
- **Targets need the local stack.** A worker in Docker (`make start-all`)
  resolves `*.localtest.me` to its own container's loopback, so every
  mock-backed check fails there. Use `make start-all-local`.
- **Dial errors in the history name `[::1]`.** That is the address the
  local worker tries first. Singapore dials IPv4, so its real errors would
  name `127.0.0.1`; its seeded failures are timeouts and 504s, which name
  no address.
