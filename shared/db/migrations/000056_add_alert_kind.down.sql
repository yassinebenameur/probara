DROP INDEX IF EXISTS idx_alerts_one_open_per_monitor_kind;

CREATE UNIQUE INDEX idx_alerts_one_open_per_monitor
    ON alerts(monitor_id)
    WHERE status IN ('active', 'acknowledged');

ALTER TABLE alerts
    DROP COLUMN IF EXISTS kind,
    DROP COLUMN IF EXISTS baseline_latency_ms,
    DROP COLUMN IF EXISTS observed_latency_ms,
    DROP COLUMN IF EXISTS anomaly_score;
