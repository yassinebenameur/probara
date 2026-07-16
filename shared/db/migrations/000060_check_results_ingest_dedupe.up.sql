-- 000060_check_results_ingest_dedupe.up.sql
-- Check results now travel over NATS (at-least-once delivery) and are
-- persisted by the platform-side ingest consumer. Make the persist idempotent:
-- one row per (job_id, result_source). A job may legitimately produce both a
-- 'monitor' row (executed) and a 'platform' row (expired before processing),
-- but never two of the same source.

-- Remove historical duplicates from past JetStream redeliveries before the
-- unique index can build. Keeps the lowest-ctid row of each duplicate set.
DELETE FROM check_results a
USING check_results b
WHERE a.job_id = b.job_id
  AND a.result_source = b.result_source
  AND a.ctid > b.ctid;

CREATE UNIQUE INDEX idx_check_results_job_source
    ON check_results(job_id, result_source);

-- Covered by the leading column of the new unique index.
DROP INDEX IF EXISTS idx_check_results_job_id;
