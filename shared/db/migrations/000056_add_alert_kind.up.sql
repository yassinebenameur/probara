-- Latency anomaly detection surfaces as a new alert kind, orthogonal to the
-- availability (up/down) state machine. A monitor can be 'up' yet 'degraded'.
ALTER TABLE alerts
    ADD COLUMN kind TEXT NOT NULL DEFAULT 'availability'
        CHECK (kind IN ('availability', 'latency_anomaly')),
    ADD COLUMN baseline_latency_ms DOUBLE PRECISION,
    ADD COLUMN observed_latency_ms DOUBLE PRECISION,
    ADD COLUMN anomaly_score DOUBLE PRECISION;

-- Allow one open availability alert AND one open latency_anomaly alert per
-- monitor at the same time. Replaces the per-monitor index from migration 45.
DROP INDEX IF EXISTS idx_alerts_one_open_per_monitor;

CREATE UNIQUE INDEX idx_alerts_one_open_per_monitor_kind
    ON alerts(monitor_id, kind)
    WHERE status IN ('active', 'acknowledged');
