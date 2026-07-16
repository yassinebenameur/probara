-- Tenant membership with fixed roles. Superadmins bypass membership entirely,
-- so no backfill is needed (all pre-000073 users were backfilled to superadmin).
CREATE TABLE tenant_memberships (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    admin_user_id UUID NOT NULL REFERENCES admin_users(id) ON DELETE CASCADE,
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    role TEXT NOT NULL CHECK (role IN ('admin', 'editor', 'viewer')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (admin_user_id, tenant_id)
);

CREATE INDEX idx_tenant_memberships_tenant ON tenant_memberships(tenant_id);
