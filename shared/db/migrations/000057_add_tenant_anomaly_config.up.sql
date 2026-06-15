-- Workspace-level configuration for latency anomaly detection. Lives on
-- tenants alongside alert_reminder_seconds / auto_create_incident, surfaced via
-- /notification-settings. Disabled by default; applies to all of a tenant's
-- monitors when enabled.
ALTER TABLE tenants
    ADD COLUMN latency_anomaly_enabled BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN latency_baseline_window_hours INTEGER NOT NULL DEFAULT 168
        CHECK (latency_baseline_window_hours > 0),
    ADD COLUMN latency_anomaly_sensitivity DOUBLE PRECISION NOT NULL DEFAULT 3.5
        CHECK (latency_anomaly_sensitivity > 0),
    ADD COLUMN latency_anomaly_min_breach_seconds INTEGER NOT NULL DEFAULT 120
        CHECK (latency_anomaly_min_breach_seconds >= 0),
    ADD COLUMN latency_anomaly_min_delta_pct DOUBLE PRECISION NOT NULL DEFAULT 20
        CHECK (latency_anomaly_min_delta_pct >= 0);
