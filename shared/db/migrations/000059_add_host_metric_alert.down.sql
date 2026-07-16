DROP INDEX IF EXISTS idx_alerts_one_open_per_monitor_kind;

-- Drop any host_metric alerts before tightening the CHECK constraint back.
DELETE FROM alerts WHERE kind = 'host_metric';

ALTER TABLE alerts
    DROP COLUMN IF EXISTS metric_name,
    DROP COLUMN IF EXISTS metric_value,
    DROP COLUMN IF EXISTS threshold_value;

ALTER TABLE alerts
    DROP CONSTRAINT IF EXISTS alerts_kind_check;

ALTER TABLE alerts
    ADD CONSTRAINT alerts_kind_check
        CHECK (kind IN ('availability', 'latency_anomaly'));

CREATE UNIQUE INDEX idx_alerts_one_open_per_monitor_kind
    ON alerts(monitor_id, kind)
    WHERE status IN ('active', 'acknowledged');
