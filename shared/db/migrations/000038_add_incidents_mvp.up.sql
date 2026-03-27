-- Incident MVP schema.

CREATE TYPE incident_state AS ENUM ('investigating', 'identified', 'monitoring', 'resolved');
CREATE TYPE incident_timeline_entry_type AS ENUM ('system', 'internal_note', 'public_update');

CREATE TABLE incidents (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    title TEXT NOT NULL,
    summary TEXT,
    state incident_state NOT NULL DEFAULT 'investigating',
    resolved_at TIMESTAMPTZ,
    is_auto_created BOOLEAN NOT NULL DEFAULT FALSE,
    auto_monitor_id UUID REFERENCES monitors(id) ON DELETE SET NULL,
    auto_alert_policy_id UUID REFERENCES alert_policies(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT incidents_auto_fields_check CHECK (
        (NOT is_auto_created) OR (auto_monitor_id IS NOT NULL AND auto_alert_policy_id IS NOT NULL)
    )
);

CREATE INDEX idx_incidents_tenant_id ON incidents(tenant_id);
CREATE INDEX idx_incidents_tenant_state ON incidents(tenant_id, state);
CREATE INDEX idx_incidents_auto_open_key ON incidents(tenant_id, auto_monitor_id, auto_alert_policy_id)
    WHERE is_auto_created AND state <> 'resolved';

CREATE TABLE incident_alerts (
    incident_id UUID NOT NULL REFERENCES incidents(id) ON DELETE CASCADE,
    alert_id UUID NOT NULL REFERENCES alerts(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (incident_id, alert_id)
);

CREATE INDEX idx_incident_alerts_alert_id ON incident_alerts(alert_id);

CREATE TABLE incident_monitors (
    incident_id UUID NOT NULL REFERENCES incidents(id) ON DELETE CASCADE,
    monitor_id UUID NOT NULL REFERENCES monitors(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (incident_id, monitor_id)
);

CREATE INDEX idx_incident_monitors_monitor_id ON incident_monitors(monitor_id);

CREATE TABLE incident_timeline_entries (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    incident_id UUID NOT NULL REFERENCES incidents(id) ON DELETE CASCADE,
    entry_type incident_timeline_entry_type NOT NULL,
    message TEXT NOT NULL,
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_incident_timeline_incident_id ON incident_timeline_entries(incident_id, created_at DESC);

CREATE TABLE incident_status_page_publications (
    incident_id UUID NOT NULL REFERENCES incidents(id) ON DELETE CASCADE,
    status_page_id UUID NOT NULL REFERENCES status_pages(id) ON DELETE CASCADE,
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    published_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    unpublished_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (incident_id, status_page_id)
);

CREATE INDEX idx_incident_status_page_publications_status_page_id ON incident_status_page_publications(status_page_id);
CREATE INDEX idx_incident_status_page_publications_tenant_id ON incident_status_page_publications(tenant_id);

CREATE TABLE incident_status_page_monitors (
    incident_id UUID NOT NULL,
    status_page_id UUID NOT NULL,
    monitor_id UUID NOT NULL REFERENCES monitors(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (incident_id, status_page_id, monitor_id),
    FOREIGN KEY (incident_id, status_page_id)
        REFERENCES incident_status_page_publications(incident_id, status_page_id)
        ON DELETE CASCADE
);

-- Alert policy opt-in for incident creation.
ALTER TABLE alert_policies
    ADD COLUMN create_incident_on_fire BOOLEAN NOT NULL DEFAULT FALSE;

CREATE INDEX idx_alert_policies_create_incident_on_fire ON alert_policies(create_incident_on_fire);

