CREATE TABLE check_results (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    monitor_id UUID NOT NULL REFERENCES monitors(id) ON DELETE CASCADE,
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    job_id UUID NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('success', 'failure', 'error')),
    http_status INTEGER,
    latency_ms INTEGER,
    error_message TEXT,
    matched_body_substring BOOLEAN NOT NULL DEFAULT false,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    started_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    completed_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_check_results_monitor_id_created_at ON check_results(monitor_id, created_at DESC);
CREATE INDEX idx_check_results_tenant_id_created_at ON check_results(tenant_id, created_at DESC);
CREATE INDEX idx_check_results_job_id ON check_results(job_id);

