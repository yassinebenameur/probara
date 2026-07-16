ALTER TABLE tenants
    DROP COLUMN IF EXISTS latency_anomaly_enabled,
    DROP COLUMN IF EXISTS latency_baseline_window_hours,
    DROP COLUMN IF EXISTS latency_anomaly_sensitivity,
    DROP COLUMN IF EXISTS latency_anomaly_min_breach_seconds,
    DROP COLUMN IF EXISTS latency_anomaly_min_delta_pct;
