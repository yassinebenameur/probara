-- Add agent_id column to monitors table for agent identification
ALTER TABLE monitors ADD COLUMN agent_id TEXT UNIQUE;

-- Add index on agent_id for fast lookups
CREATE INDEX idx_monitors_agent_id ON monitors(agent_id) WHERE agent_id IS NOT NULL;

-- Add metrics_data column to check_results for storing agent metrics
ALTER TABLE check_results ADD COLUMN metrics_data JSONB;

-- Add index on metrics_data for better query performance
CREATE INDEX idx_check_results_metrics_data ON check_results USING GIN(metrics_data) WHERE metrics_data IS NOT NULL;

-- Note: Monitor type is stored as TEXT, so 'agent' type is implicitly supported
-- No enum constraint exists, so no ALTER TYPE needed

