-- Drop metrics_data index
DROP INDEX IF EXISTS idx_check_results_metrics_data;

-- Drop metrics_data column from check_results
ALTER TABLE check_results DROP COLUMN IF EXISTS metrics_data;

-- Drop agent_id index
DROP INDEX IF EXISTS idx_monitors_agent_id;

-- Drop agent_id column from monitors
ALTER TABLE monitors DROP COLUMN IF EXISTS agent_id;

