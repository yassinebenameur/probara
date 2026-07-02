-- 000061_create_locations.up.sql
-- Private locations: remote worker deployments (NATS-only connectivity) that
-- run checks from inside a customer network. Tenant-scoped like every other
-- entity. NATS subjects key on the location UUID, so slugs are display-only.
CREATE TABLE locations (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id    UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    name         TEXT NOT NULL,
    slug         TEXT NOT NULL,
    description  TEXT,
    enabled      BOOLEAN NOT NULL DEFAULT TRUE,
    -- Updated by the ingest consumer from worker heartbeats; the UI derives
    -- connected/disconnected from its freshness.
    last_seen_at TIMESTAMPTZ,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at   TIMESTAMPTZ
);

CREATE UNIQUE INDEX idx_locations_tenant_name
    ON locations(tenant_id, lower(name)) WHERE deleted_at IS NULL;
CREATE INDEX idx_locations_tenant
    ON locations(tenant_id) WHERE deleted_at IS NULL;
