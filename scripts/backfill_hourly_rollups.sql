-- backfill_hourly_rollups.sql
--
-- Idempotent, REPLACE-semantics backfill that rebuilds monitor_hourly_rollups
-- (and monitor_daily_rollups) from raw check_results for a given time range.
--
-- WHEN TO USE
--   1. Repair gaps left by the original 48h-only hourly backfill shipped in
--      migration 000041_create_monitor_hourly_rollups (hours older than 48h at
--      migration time were never aggregated into monitor_hourly_rollups).
--   2. Recover check_results rows skipped by the rollup job's poisoned-row
--      handling (scheduler logs "Skipping poisoned rollup row" and increments
--      rollup_rows_skipped_total; the raw row stays in check_results).
--
-- USAGE
--   psql "$DATABASE_URL" \
--     -v ON_ERROR_STOP=1 \
--     -v start="2026-05-01 00:00:00+00" \
--     -v end="2026-05-02 00:00:00+00" \
--     -f scripts/backfill_hourly_rollups.sql
--
--   or interactively:
--     \set start '2026-05-01 00:00:00+00'
--     \set end   '2026-05-02 00:00:00+00'
--     \i scripts/backfill_hourly_rollups.sql
--
--   NOTE: in a long-lived interactive session, an error before COMMIT leaves
--   the advisory lock below held (stalling the incremental rollup job) until
--   the session ends. One-shot `psql -f` is safe: session exit releases it.
--
--   CHUNKING: keep each run to <= 1 day of raw data. check_results can hold
--   millions of rows per day; one giant range bloats the transaction, holds
--   the rollup advisory lock for a long time (stalling the incremental job),
--   and risks statement timeouts. Loop over consecutive [start, end) days.
--
--   WARNING — RETENTION: never run this over ranges at or older than any
--   tenant's raw retention horizon (tenants.data_retention_days). Raw rows
--   there have been pruned, so the replace-semantics rebuild would UNDERCOUNT
--   any bucket that straddles the horizon (rebuilt from only the surviving
--   raws). Rollups for fully-pruned history must be left untouched.
--
-- SEMANTICS
--   * Buckets are REBUILT from raw rows and REPLACED wholesale
--     (ON CONFLICT ... DO UPDATE SET <col> = EXCLUDED.<col>), not added to.
--     Re-running the script over the same range is therefore idempotent.
--   * Any hour/day bucket that *intersects* [start, end) is recomputed from
--     ALL of its raw rows (the range is effectively snapped outward to bucket
--     boundaries), so partial-bucket ranges cannot lose data.
--   * Buckets with a rollup row but no surviving raw rows (raws pruned by
--     retention) are left untouched: we cannot recompute them from nothing.
--
-- SAFETY / CONCURRENCY
--   * Takes the same advisory lock as the scheduler's rollup maintenance job
--     (rollupMaintenanceAdvisoryLock = 901337402 in
--     scheduler/internal/scheduler/rollups.go). pg_advisory_lock BLOCKS until
--     any in-flight incremental run finishes; the incremental job uses
--     pg_try_advisory_lock and simply skips its cycle while we hold the lock.
--   * HOURLY rows are additionally hard-capped to hour buckets strictly
--     before date_trunc('hour', cursor), where cursor is
--     rollup_job_state.last_created_at for job 'monitor_daily_rollups'.
--     The incremental writer owns the cursor hour and everything after it
--     (it adds rows there incrementally), so replacing those buckets would
--     race/clobber it. Hours strictly before the cursor hour are settled.
--   * DAILY rows are capped to FULL days strictly before
--     date_trunc('day', cursor). This is deliberately stricter than the
--     hourly cap: a day that is only partially before the cursor already
--     contains post-cursor-hour partial data written by the incremental job,
--     and replace semantics on that day would LOSE it. To repair the current
--     (cursor) day's hourly buckets, run this script for the hours you need;
--     the daily row for that day is maintained by the incremental job alone.
--   * If rollup_job_state has no row (or a NULL cursor) for
--     'monitor_daily_rollups', the script is a NO-OP: without a cursor we
--     cannot tell which buckets the incremental writer owns.
--
-- OUT OF SCOPE
--   monitor_downtime_periods / monitor_downtime_open are NOT backfilled.
--   Downtime tracking is stateful (open/close transitions in raw-row order)
--   and cannot be rebuilt by a grouped aggregate; skipped/poisoned rows may
--   therefore leave small downtime inaccuracies that this script does not fix.

SELECT pg_advisory_lock(901337402);

BEGIN;

-- The Go rollup job buckets in UTC (rollups.go computes day/hour with
-- time.UTC); date_trunc on timestamptz follows the session time zone, so pin
-- it for this transaction.
SET LOCAL TIME ZONE 'UTC';

-- ---------------------------------------------------------------------------
-- Hourly: rebuild every hour bucket intersecting [:start, :end) that lies
-- strictly before the incremental cursor's hour.
-- ---------------------------------------------------------------------------
WITH cursor_cap AS (
    SELECT date_trunc('hour', last_created_at) AS hour_cap
    FROM rollup_job_state
    WHERE job_name = 'monitor_daily_rollups'
      AND last_created_at IS NOT NULL
)
INSERT INTO monitor_hourly_rollups (
    tenant_id, monitor_id, bucket_hour, total_checks, success_checks, error_checks,
    latency_success_sum_ms, latency_success_count, latest_status, latest_check_at,
    created_at, updated_at
)
SELECT
    cr.tenant_id,
    cr.monitor_id,
    date_trunc('hour', cr.created_at) AS bucket_hour,
    COUNT(*) AS total_checks,
    COUNT(*) FILTER (WHERE cr.status = 'success') AS success_checks,
    COUNT(*) FILTER (WHERE cr.status = 'error') AS error_checks,
    COALESCE(SUM(cr.latency_ms) FILTER (WHERE cr.status = 'success' AND cr.latency_ms IS NOT NULL), 0)::DOUBLE PRECISION AS latency_success_sum_ms,
    COUNT(cr.latency_ms) FILTER (WHERE cr.status = 'success') AS latency_success_count,
    (ARRAY_AGG(cr.status ORDER BY cr.created_at DESC, cr.id DESC))[1] AS latest_status,
    MAX(cr.created_at) AS latest_check_at,
    NOW(),
    NOW()
FROM check_results cr
CROSS JOIN cursor_cap
WHERE cr.result_source = 'monitor'
  AND date_trunc('hour', cr.created_at) >= date_trunc('hour', :'start'::timestamptz)
  AND date_trunc('hour', cr.created_at) < :'end'::timestamptz
  AND date_trunc('hour', cr.created_at) < cursor_cap.hour_cap
  -- Redundant with the bucket predicates above (a strict superset of the
  -- bucket-snapped range); present only to enable index range scans on
  -- created_at instead of full table scans.
  AND cr.created_at >= date_trunc('hour', :'start'::timestamptz)
  AND cr.created_at < :'end'::timestamptz + interval '1 hour'
GROUP BY cr.tenant_id, cr.monitor_id, date_trunc('hour', cr.created_at)
ON CONFLICT (monitor_id, bucket_hour) DO UPDATE SET
    tenant_id = EXCLUDED.tenant_id,
    total_checks = EXCLUDED.total_checks,
    success_checks = EXCLUDED.success_checks,
    error_checks = EXCLUDED.error_checks,
    latency_success_sum_ms = EXCLUDED.latency_success_sum_ms,
    latency_success_count = EXCLUDED.latency_success_count,
    latest_status = EXCLUDED.latest_status,
    latest_check_at = EXCLUDED.latest_check_at,
    updated_at = NOW();

-- ---------------------------------------------------------------------------
-- Daily: rebuild every day bucket intersecting [:start, :end) that lies
-- strictly before the incremental cursor's DAY (full settled days only — see
-- the SAFETY note above for why this cap is stricter than the hourly one).
-- ---------------------------------------------------------------------------
WITH cursor_cap AS (
    SELECT date_trunc('day', last_created_at) AS day_cap
    FROM rollup_job_state
    WHERE job_name = 'monitor_daily_rollups'
      AND last_created_at IS NOT NULL
)
INSERT INTO monitor_daily_rollups (
    tenant_id, monitor_id, bucket_day, total_checks, success_checks, error_checks,
    latency_success_sum_ms, latency_success_count, latest_status, latest_check_at,
    created_at, updated_at
)
SELECT
    cr.tenant_id,
    cr.monitor_id,
    date_trunc('day', cr.created_at)::date AS bucket_day,
    COUNT(*) AS total_checks,
    COUNT(*) FILTER (WHERE cr.status = 'success') AS success_checks,
    COUNT(*) FILTER (WHERE cr.status = 'error') AS error_checks,
    COALESCE(SUM(cr.latency_ms) FILTER (WHERE cr.status = 'success' AND cr.latency_ms IS NOT NULL), 0)::DOUBLE PRECISION AS latency_success_sum_ms,
    COUNT(cr.latency_ms) FILTER (WHERE cr.status = 'success') AS latency_success_count,
    (ARRAY_AGG(cr.status ORDER BY cr.created_at DESC, cr.id DESC))[1] AS latest_status,
    MAX(cr.created_at) AS latest_check_at,
    NOW(),
    NOW()
FROM check_results cr
CROSS JOIN cursor_cap
WHERE cr.result_source = 'monitor'
  AND date_trunc('day', cr.created_at) >= date_trunc('day', :'start'::timestamptz)
  AND date_trunc('day', cr.created_at) < :'end'::timestamptz
  AND date_trunc('day', cr.created_at) < cursor_cap.day_cap
  -- Redundant with the bucket predicates above (a strict superset of the
  -- bucket-snapped range); present only to enable index range scans on
  -- created_at instead of full table scans.
  AND cr.created_at >= date_trunc('day', :'start'::timestamptz)
  AND cr.created_at < :'end'::timestamptz + interval '1 day'
GROUP BY cr.tenant_id, cr.monitor_id, date_trunc('day', cr.created_at)
ON CONFLICT (monitor_id, bucket_day) DO UPDATE SET
    tenant_id = EXCLUDED.tenant_id,
    total_checks = EXCLUDED.total_checks,
    success_checks = EXCLUDED.success_checks,
    error_checks = EXCLUDED.error_checks,
    latency_success_sum_ms = EXCLUDED.latency_success_sum_ms,
    latency_success_count = EXCLUDED.latency_success_count,
    latest_status = EXCLUDED.latest_status,
    latest_check_at = EXCLUDED.latest_check_at,
    updated_at = NOW();

COMMIT;

SELECT pg_advisory_unlock(901337402);
