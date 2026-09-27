-- 000083_create_rollup_dirty.up.sql
-- Dirty-bucket ledger for rollup maintenance. The result insert marks its
-- (monitor, hour) bucket dirty IN THE SAME TRANSACTION, so whenever a row
-- commits — late, redelivered, or behind any clock — its bucket is marked.
-- This is exact by construction, unlike the retired cursor over the
-- worker-clock created_at, which permanently skipped rows that became
-- visible behind it (the ~0.1–0.6% rollup undercount).
--
-- The rollup job consumes marks by REBUILDING each bucket wholesale
-- (REPLACE semantics, as scripts/backfill_hourly_rollups.sql does), then
-- re-deriving the affected daily buckets — idempotent and self-healing.
CREATE TABLE rollup_dirty (
    monitor_id  UUID NOT NULL REFERENCES monitors(id) ON DELETE CASCADE,
    bucket_hour TIMESTAMPTZ NOT NULL,
    -- Refreshed (clock_timestamp) whenever a row re-marks an existing bucket;
    -- the consumer's delete is conditional on the value it read, so a mark
    -- touched mid-rebuild survives for the next run.
    marked_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (monitor_id, bucket_hour)
);
CREATE INDEX idx_rollup_dirty_bucket ON rollup_dirty(bucket_hour);

-- Upgrade seeding: rows already committed past the old incremental cursor
-- were never folded and would otherwise be skipped forever — the new
-- consumer starts with an empty ledger, considers it drained, and advances
-- the completeness watermark past them. Mark every bucket in the cursor
-- tail (all monitor-source history when no cursor row exists, e.g. rollups
-- never ran).
INSERT INTO rollup_dirty (monitor_id, bucket_hour)
SELECT DISTINCT cr.monitor_id, date_trunc('hour', cr.created_at)
FROM check_results cr
WHERE cr.result_source = 'monitor'
  AND cr.created_at > COALESCE(
      (SELECT last_created_at FROM rollup_job_state WHERE job_name = 'monitor_daily_rollups'),
      '-infinity'::timestamptz)
ON CONFLICT DO NOTHING;
