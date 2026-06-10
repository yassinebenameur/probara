# Correctness Notes — Analytics, Rollups, and Caches

Concise reference for the intentional semantics (and known limits) of the
uptime/SLA numbers the product displays. Each section points at the code that
implements the behavior.

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
- Group aggregation convention (`api/internal/services/dashboard/groups.go`):
  a member monitor (or sparkline bucket) with **zero checks in the window
  defaults to 100% uptime** (`CASE WHEN total_checks > 0 ... ELSE 100.0` and
  the `TotalChecks == 0 → 100.0` branches). No data is treated as "not known
  to be down", which biases group uptime up when members are paused or new.

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
