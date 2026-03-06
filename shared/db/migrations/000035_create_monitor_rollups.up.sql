CREATE TABLE monitor_daily_rollups (
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    monitor_id UUID NOT NULL REFERENCES monitors(id) ON DELETE CASCADE,
    bucket_day DATE NOT NULL,
    total_checks INTEGER NOT NULL DEFAULT 0,
    success_checks INTEGER NOT NULL DEFAULT 0,
    latency_success_sum_ms DOUBLE PRECISION NOT NULL DEFAULT 0,
    latency_success_count INTEGER NOT NULL DEFAULT 0,
    latest_status TEXT,
    latest_check_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (monitor_id, bucket_day)
);

CREATE INDEX idx_monitor_daily_rollups_tenant_day
    ON monitor_daily_rollups(tenant_id, bucket_day DESC, monitor_id);

CREATE TABLE monitor_downtime_periods (
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    monitor_id UUID NOT NULL REFERENCES monitors(id) ON DELETE CASCADE,
    start_time TIMESTAMPTZ NOT NULL,
    end_time TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (monitor_id, start_time)
);

CREATE INDEX idx_monitor_downtime_periods_tenant_window
    ON monitor_downtime_periods(tenant_id, start_time DESC, end_time DESC, monitor_id);

CREATE TABLE monitor_downtime_open (
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    monitor_id UUID NOT NULL REFERENCES monitors(id) ON DELETE CASCADE,
    started_at TIMESTAMPTZ NOT NULL,
    last_seen_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (monitor_id)
);

CREATE INDEX idx_monitor_downtime_open_tenant_monitor
    ON monitor_downtime_open(tenant_id, monitor_id);

CREATE TABLE rollup_job_state (
    job_name TEXT PRIMARY KEY,
    last_created_at TIMESTAMPTZ,
    last_check_result_id UUID,
    last_run_at TIMESTAMPTZ,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
