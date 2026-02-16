CREATE TABLE alert_policies (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    description TEXT,
    failure_threshold INTEGER NOT NULL CHECK (failure_threshold > 0),
    failure_window_seconds INTEGER NOT NULL CHECK (failure_window_seconds > 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_alert_policies_tenant_id ON alert_policies(tenant_id);

