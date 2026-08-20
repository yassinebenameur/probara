# Correctness Notes — Analytics, Rollups, and Caches

Concise reference for the intentional semantics (and known limits) of the
uptime/SLA numbers the product displays. Each section points at the code that
implements the behavior.

State *transition* semantics (up/suspect/down/degraded/unknown, quorum
aggregation, pause/maintenance handling) are specified normatively in
`state-semantics.md`; this file covers the analytics/rollup layer built on
top of them.

## SLA aggregation semantics (post-D2)

Headline SLA/uptime for any scope (monitor, group, tenant) is a
**monitor-weighted mean of whole-window per-monitor rates**:

1. Per monitor: `sum(success_checks) / sum(total_checks)` over **all** checks
   in the window — every check weighs equally, regardless of which day or hour
   it landed in. Per-monitor latency is likewise
   `sum(latency_success_sum_ms) / sum(latency_success_count)`.
2. Across monitors: the unweighted average of those per-monitor values — each
   monitor contributes one data point, so a monitor checked every 30s does not
   drown out one checked every 5min.

This applies to both data paths:

- **24h (raw/stitched)**: `computeMonitorWeightedUptime` /
  `computeMonitorWeightedLatency` in
  `api/internal/services/dashboard/service.go`.
- **7d/30d/90d/365d (daily rollups)**: the summary loop in
  `buildRollupResult`, `shared/analytics/repository.go`. Before D2 this path
  averaged each monitor's *per-day* rates, so a day with 1 check weighed the
  same as a day with 1000; it now pools checks across the window, matching the
  24h math. Displayed 7d+ numbers shifted accordingly.

**Chart points are intentionally different.** Series buckets (hourly/daily
trend points) are *bucket-scoped* rates — each point answers "what was uptime
during this bucket", pooled across the scope's monitors for that bucket. They
are not required to average back to the headline stat (see the Decision D4
comment in `getTrend`, `api/internal/services/dashboard/service.go`, near the
"pooled (sum-of-success / sum-of-total) bucket uptime" note). Do not "fix"
charts to match the scalar, or vice versa.

## Time-based availability (method: interval vs sampled)

`GetScopeAnalytics` headlines now prefer **time integration over
`monitor_state_intervals`** (`shared/analytics/interval_availability.go`,
spec S-U1–S-U5): availability = available time ÷ (available + unplanned down
time), with unknown/paused time excluded from the denominator and reported as
`coverage_pct`, and down∩maintenance excluded as planned (S-M3). The summary
labels the math via `method`: `"interval"` when every monitor's timeline
reaches the window start, `"sampled"` (the D2 count-based math below)
otherwise — never silently mixed. Currently interval-based: the monitor
analytics headline and the dashboard long-range `overall_uptime`. Still
sampled: batch/status-page surfaces, the 24h/1h stitched dashboard scalars,
per-bucket series (deliberately bucket-scoped sample rates), and any scope
containing a group monitor (no timeline).

## Percentiles are raw-range only

Daily/hourly rollups store only sums and counts
(`total_checks`, `success_checks`, `latency_success_sum_ms`,
`latency_success_count`); per-check latency samples are gone after rollup.
Percentiles (p50/p95) therefore exist only on raw ranges (1h/6h/24h, computed
in `buildRawResult`) and are **impossible** for 7d+ rollup ranges —
`buildRollupResult` deliberately leaves `MedianLatencyMS`/`P95LatencyMS` nil,
and tests assert that. Any future 7d+ percentile would require sketches
(t-digest etc.) in the rollup tables, not arithmetic on existing columns.

## Rollup buckets are UTC

Daily rollup rows are keyed by UTC `bucket_day` and windows are resolved in
UTC (`newDailyWindow` / `ResolveWindow` in `shared/analytics/types.go`;
hourly buckets in `scheduler/internal/scheduler/rollups.go` are UTC
clock-hours). Tenants in other timezones see day buckets that span UTC
midnights, not local ones — a local-evening outage may straddle two displayed
days.

## Paused monitors and no-data windows

"Paused" means `enabled = false`: the scheduler only schedules
`WHERE enabled = true`, so a paused monitor produces **no checks** while
paused. There is no synthetic fill — a monitor paused for half the window has
its uptime computed over only the checked half. Consequences:

- A monitor that was down, then paused, keeps reflecting the pre-pause checks
  for as long as they remain in the window.
- Dashboard aggregate queries additionally filter `m.enabled = TRUE`, so
  currently-paused monitors drop out of tenant-level stats entirely
  (`api/internal/services/dashboard/service.go`, `groups.go`).
- **Zero-data convention (S-D1, `state-semantics.md`)**: no checks in the
  window is *no data*, never a percentage. The analytics summary carries
  `has_data` (`shared/analytics`); dashboard `overall_uptime`, group/member
  uptime, and sparkline buckets are `null` when nothing was checked
  (`api/internal/services/dashboard`); the UI renders "—" or a gap. The old
  convention — group members and sparkline buckets defaulting to **100%** on
  zero checks, and empty analytics summaries reading 0% (a red "SLA Breach")
  — is retired. Group uptime is now the mean over members *with* data.

## Rollup dirty-bucket ledger

Every monitor-source `check_results` insert marks its `(monitor_id,
bucket_hour)` in `rollup_dirty` **in the same transaction**
(`shared/monitorstate/record.go`, migration 000083), and rollup maintenance
(`scheduler/internal/scheduler/rollups_dirty.go`) consumes the ledger by
rebuilding each marked hourly bucket **wholesale** from raw rows (REPLACE
semantics, the same math as `scripts/backfill_hourly_rollups.sql`), then
re-deriving the affected daily buckets from the hourly table. Whenever a row
commits — late, redelivered, behind any clock — its bucket is marked, which
is exact by construction: the retired incremental cursor scanned worker-clock
`created_at` and permanently skipped late-visible rows (the ~0.1–0.6%
undercount), and its poison-skip replay path could drop rows outright. A
failed batch now simply leaves its marks for the next run. `rollup_job_state`
survives as a **completeness watermark** for the 24h stitchers: it advances
to the newest monitor-source row only after a run that drains the ledger (see
the updated contract in `shared/analytics/rollup_cursor.go`). Downtime tables
are now a projection of `monitor_state_intervals` (closed `down` intervals →
periods, open `down` interval → `monitor_downtime_open`); pre-timeline
downtime rows are preserved, and group monitors (no timeline yet) keep their
frozen rows until their state derivation is unified. The backfill script
remains the repair tool for pre-ledger history.

## Rollup-era failure vs error split

`error_checks` columns were added to both rollup tables in migration
`shared/db/migrations/000047_add_rollup_error_checks.up.sql` and are exact for
every hour rolled up **after** that migration (`scheduler/internal/scheduler/
rollups.go` writes them). Hours rolled up before it have `error_checks = 0`,
so their bad checks all surface as failures (`FailureChecks =
total − success − error`; see the `MonitorRolling24hTotals` doc comment in
`api/internal/services/dashboard/hourly_rollup.go`). To make history exact,
re-aggregate the affected range with `scripts/backfill_hourly_rollups.sql`
(REPLACE semantics, idempotent) — provided the raw `check_results` rows are
still within retention.

## Cached aggregates and staleness

`generated_at` on a response reflects when the response was assembled, not
when the underlying numbers were computed. Bounded staleness windows:

- **Dashboard rolling-24h aggregates**: cached per (tenant, tags) for up to
  30s (`rolling24hCacheTTL`,
  `api/internal/services/dashboard/rolling24h_cache.go`).
- **Status page HTML**: render cache holds a page for up to 10s by default
  (`defaultRenderCacheTTL`, `status-page/internal/statuspage/cache.go`,
  override via `STATUS_PAGE_CACHE_TTL`); SSE-driven invalidation usually
  refreshes sooner, the TTL is the no-event worst case.

Numbers shown can therefore lag reality by up to the relevant TTL; this is a
deliberate trade for collapsing request bursts into one computation.

## Deploy ordering

The helm migrations job is a **post-install/post-upgrade hook**
(`helm/monitoring-platform/templates/job-migrations.yaml`): on upgrade the new
pods are rolled out first, and migrations run after. New code must therefore
tolerate running against the **old schema** for the rollout window.
Concretely:

- **Scheduler rollup job**: schema-lag errors (Postgres SQLSTATE class 42,
  e.g. `42703` undefined_column, `42P01` undefined_table) are classified as
  *transient* in `isTransientRollupError`
  (`scheduler/internal/scheduler/rollups.go`). The run aborts without
  advancing the cursor and stalls until the migration lands — it must never
  enter the poison-skip replay path, which would silently drop the backlog.
- **Dashboard 24h endpoints**: queries reading columns added by a pending
  migration (e.g. `error_checks`) return errors during the window; this
  self-heals once the migration job completes.

For zero-error rollouts, when convenient, run the migration job manually
before `helm upgrade` so the new schema is already in place when the new pods
start.
