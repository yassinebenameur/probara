-- IdP group → role mapping for OIDC SSO. Zero rows means the feature is off
-- (JIT defaults apply and roles are never re-synced); any rows make the IdP
-- the source of truth for SSO users' roles on every login.
CREATE TABLE oidc_group_mappings (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    group_name TEXT NOT NULL CHECK (group_name <> ''),
    -- NULL tenant_id = platform-level mapping; its only valid role is
    -- 'superadmin'. Tenant mappings carry tenant roles.
    tenant_id UUID REFERENCES tenants(id) ON DELETE CASCADE,
    role TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CHECK (
        (tenant_id IS NULL AND role = 'superadmin') OR
        (tenant_id IS NOT NULL AND role IN ('admin', 'editor', 'viewer'))
    )
);

-- One role per (group, tenant) and one platform mapping per group. Two
-- partial indexes because UNIQUE NULLS NOT DISTINCT needs PG15+.
CREATE UNIQUE INDEX idx_oidc_group_mappings_tenant
    ON oidc_group_mappings (group_name, tenant_id) WHERE tenant_id IS NOT NULL;
CREATE UNIQUE INDEX idx_oidc_group_mappings_platform
    ON oidc_group_mappings (group_name) WHERE tenant_id IS NULL;
