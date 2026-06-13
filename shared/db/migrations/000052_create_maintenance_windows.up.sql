CREATE TABLE maintenance_windows (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id   UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    title       TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    starts_at   TIMESTAMPTZ NOT NULL,
    ends_at     TIMESTAMPTZ NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT maintenance_windows_time_order CHECK (ends_at > starts_at)
);

CREATE INDEX idx_maintenance_windows_tenant_time
    ON maintenance_windows (tenant_id, starts_at, ends_at);

CREATE TABLE maintenance_window_monitors (
    maintenance_window_id UUID NOT NULL REFERENCES maintenance_windows(id) ON DELETE CASCADE,
    monitor_id            UUID NOT NULL REFERENCES monitors(id) ON DELETE CASCADE,
    PRIMARY KEY (maintenance_window_id, monitor_id)
);

CREATE INDEX idx_maintenance_window_monitors_monitor
    ON maintenance_window_monitors (monitor_id);
