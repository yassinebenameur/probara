-- SLA objects: a named monitor set with a target, a calendar period in a
-- timezone and a composite rule (docs/state-semantics.md S-U6, S-U7).
-- Membership is explicit monitors (sla_monitors) plus any monitor carrying
-- one of the tags, resolved when a report runs.
CREATE TABLE slas (
    id                      UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id               UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    name                    TEXT NOT NULL,
    description             TEXT NOT NULL DEFAULT '',
    target_pct              NUMERIC(7,4) NOT NULL CHECK (target_pct > 0 AND target_pct < 100),
    aggregation             TEXT NOT NULL DEFAULT 'serial' CHECK (aggregation IN ('serial', 'mean')),
    period                  TEXT NOT NULL DEFAULT 'monthly' CHECK (period IN ('weekly', 'monthly', 'quarterly')),
    timezone                TEXT NOT NULL DEFAULT 'UTC',
    degraded_counts_as_down BOOLEAN NOT NULL DEFAULT FALSE,
    tags                    TEXT[] NOT NULL DEFAULT '{}',
    created_at              TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at              TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (tenant_id, name)
);

CREATE TABLE sla_monitors (
    sla_id     UUID NOT NULL REFERENCES slas(id) ON DELETE CASCADE,
    monitor_id UUID NOT NULL REFERENCES monitors(id) ON DELETE CASCADE,
    PRIMARY KEY (sla_id, monitor_id)
);

CREATE INDEX idx_sla_monitors_monitor ON sla_monitors (monitor_id);

-- Issued reports are immutable snapshots of a closed period: maintenance
-- windows are hard-deleted and history can be wiped, so a live report of a
-- past period can change; an issued one never does. No FK to monitors —
-- the snapshot must outlive a purged monitor.
CREATE TABLE sla_reports (
    id                   UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id            UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    sla_id               UUID NOT NULL REFERENCES slas(id) ON DELETE CASCADE,
    period_key           TEXT NOT NULL,
    period_start         TIMESTAMPTZ NOT NULL,
    period_end           TIMESTAMPTZ NOT NULL,
    availability_pct     DOUBLE PRECISION,
    target_pct           NUMERIC(7,4) NOT NULL,
    met                  BOOLEAN,
    issued_at            TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    issued_by_admin_id   UUID REFERENCES admin_users(id) ON DELETE SET NULL,
    issued_by_api_key_id UUID,
    data                 JSONB NOT NULL,
    UNIQUE (sla_id, period_start)
);

CREATE INDEX idx_sla_reports_tenant ON sla_reports (tenant_id, sla_id, period_start DESC);
