DROP INDEX IF EXISTS idx_alerts_root_cause_monitor;

ALTER TABLE alerts
    DROP COLUMN IF EXISTS root_cause_monitor_id,
    DROP COLUMN IF EXISTS root_cause_down_since;
