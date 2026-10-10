ALTER TABLE monitor_location_state DROP COLUMN IF EXISTS last_result_started_at;
ALTER TABLE monitors
    DROP COLUMN IF EXISTS last_result_started_at,
    DROP COLUMN IF EXISTS last_result_at;
ALTER TABLE check_results DROP COLUMN IF EXISTS ingested_at;
DROP TABLE IF EXISTS monitor_state_intervals;
