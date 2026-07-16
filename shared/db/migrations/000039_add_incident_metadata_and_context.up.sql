-- Incident metadata and owner context.

ALTER TABLE incidents
    ADD COLUMN severity TEXT NOT NULL DEFAULT 'high',
    ADD COLUMN owner_user_id UUID REFERENCES admin_users(id) ON DELETE SET NULL;

ALTER TABLE incidents
    ADD CONSTRAINT incidents_severity_check
    CHECK (severity IN ('critical', 'high', 'medium', 'low'));

CREATE INDEX idx_incidents_owner_user_id ON incidents(owner_user_id);
CREATE INDEX idx_incidents_tenant_severity_updated_at ON incidents(tenant_id, severity, updated_at DESC);
