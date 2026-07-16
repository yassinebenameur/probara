-- 000060_check_results_ingest_dedupe.down.sql
CREATE INDEX IF NOT EXISTS idx_check_results_job_id ON check_results(job_id);
DROP INDEX IF EXISTS idx_check_results_job_source;
