-- Host-metric threshold alerts surface as a new alert kind, orthogonal to the
-- availability (up/down) state machine and to latency_anomaly. An agent monitor
-- can be 'up' yet breach a CPU/memory/disk/swap threshold.
ALTER TABLE alerts
    DROP CONSTRAINT IF EXISTS alerts_kind_check;

ALTER TABLE alerts
    ADD CONSTRAINT alerts_kind_check
        CHECK (kind IN ('availability', 'latency_anomaly', 'host_metric'));

-- host_metric annotations: which metric breached, its observed value, and the
-- configured threshold. NULL for availability/latency_anomaly alerts.
ALTER TABLE alerts
    ADD COLUMN metric_name TEXT,
    ADD COLUMN metric_value DOUBLE PRECISION,
    ADD COLUMN threshold_value DOUBLE PRECISION;

-- Allow one open alert per (monitor, kind, metric) so a monitor can hold an
-- availability alert, a latency_anomaly alert, AND a host_metric alert per
-- breaching metric (e.g. CPU and disk) at the same time. COALESCE keeps the
-- pre-existing one-per-(monitor,kind) behaviour for the NULL-metric kinds.
DROP INDEX IF EXISTS idx_alerts_one_open_per_monitor_kind;

CREATE UNIQUE INDEX idx_alerts_one_open_per_monitor_kind
    ON alerts(monitor_id, kind, (COALESCE(metric_name, '')))
    WHERE status IN ('active', 'acknowledged');
