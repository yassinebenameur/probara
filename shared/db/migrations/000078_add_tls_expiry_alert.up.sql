-- TLS-expiry alerts surface as their own alert kind, orthogonal to the
-- availability (up/down) state machine: a monitor whose endpoint is healthy
-- but whose certificate is inside the tls_min_days_valid window stays 'up'
-- and gets a tls_expiry alert instead of a fake outage. The existing
-- (monitor_id, kind, COALESCE(metric_name, '')) partial unique index already
-- guarantees one open tls_expiry alert per monitor.
ALTER TABLE alerts
    DROP CONSTRAINT IF EXISTS alerts_kind_check;

ALTER TABLE alerts
    ADD CONSTRAINT alerts_kind_check
        CHECK (kind IN ('availability', 'latency_anomaly', 'host_metric', 'mesh_edge', 'tls_expiry'));
