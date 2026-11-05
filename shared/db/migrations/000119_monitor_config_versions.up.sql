-- Monitor config version history. One row per recorded change of a
-- monitor's configuration, written by the API's monitors service after every
-- create and update (api/internal/services/monitors/versions.go) and by
-- discovery approval. The snapshot is masked (secret fields are "***"), so it
-- never holds a secret; changed_secret_fields names the secret fields whose
-- stored envelope changed in that write. Only the last 100 versions per
-- monitor are kept (pruned on insert); there is no backfill, so a monitor's
-- history starts at its first change after this migration.
CREATE TABLE monitor_config_versions (
    id BIGSERIAL PRIMARY KEY,
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    monitor_id UUID NOT NULL REFERENCES monitors(id) ON DELETE CASCADE,
    version INTEGER NOT NULL CHECK (version > 0),
    snapshot JSONB NOT NULL,
    -- SHA-256 (hex) of the canonical snapshot; a write whose snapshot hashes
    -- like the latest version and changed no secret is not recorded.
    snapshot_hash TEXT NOT NULL,
    changed_secret_fields TEXT[] NOT NULL DEFAULT '{}',
    actor_type TEXT NOT NULL CHECK (actor_type IN ('user', 'api_key', 'system')),
    actor_id UUID,
    actor_label TEXT NOT NULL DEFAULT '',
    source TEXT NOT NULL CHECK (source IN ('api', 'ui', 'mcp', 'import', 'apply', 'restore', 'discovery')),
    -- The version a restore re-applied; NULL for every other source.
    restored_from INTEGER,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (monitor_id, version)
);

CREATE INDEX idx_monitor_config_versions_tenant ON monitor_config_versions (tenant_id);
