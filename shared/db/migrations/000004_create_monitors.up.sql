CREATE TABLE monitors (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    type TEXT NOT NULL DEFAULT 'http',
    url TEXT NOT NULL,
    method TEXT NOT NULL DEFAULT 'GET',
    headers JSONB,
    body TEXT,
    interval_seconds INTEGER NOT NULL CHECK (interval_seconds >= 10 AND interval_seconds <= 86400),
    timeout_seconds INTEGER NOT NULL CHECK (timeout_seconds > 0 AND timeout_seconds < interval_seconds),
    expected_status INTEGER,
    expected_body_substring TEXT,
    alert_policy_id UUID REFERENCES alert_policies(id) ON DELETE SET NULL,
    enabled BOOLEAN NOT NULL DEFAULT true,
    tags TEXT[],
    next_run_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_monitors_tenant_id ON monitors(tenant_id);
CREATE INDEX idx_monitors_enabled ON monitors(enabled);
CREATE INDEX idx_monitors_next_run_at ON monitors(next_run_at) WHERE enabled = true;
CREATE INDEX idx_monitors_tags ON monitors USING GIN(tags);
CREATE INDEX idx_monitors_alert_policy_id ON monitors(alert_policy_id);

