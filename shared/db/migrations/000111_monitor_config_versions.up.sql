-- Monitor configuration history. One row per recorded save of a monitor,
-- written by the API (api/internal/services/monitors/versions.go) after every
-- create, update, import, discovery approval and restore. The snapshot is the
-- masked configuration: a secret reads "***" when set and is never stored,
-- in plain text or ciphertext; changed_secret_fields only names the secrets a
-- save set, replaced or cleared. A save that changes nothing is not recorded.
-- Each monitor keeps its newest 100 versions (older ones are pruned when a new
-- one is written), and rows go with the monitor and with the workspace.
-- There is no backfill: a monitor's first save after this migration records
-- the configuration it had before as a baseline version.
CREATE TABLE monitor_config_versions (
    id BIGSERIAL PRIMARY KEY,
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    monitor_id UUID NOT NULL REFERENCES monitors(id) ON DELETE CASCADE,
    version INTEGER NOT NULL CHECK (version > 0),
    snapshot JSONB NOT NULL,
    -- Snapshot paths that differ from the previous version (config.url,
    -- tags, ...); empty for a monitor's first version.
    changed_fields TEXT[] NOT NULL DEFAULT '{}',
    changed_secret_fields TEXT[] NOT NULL DEFAULT '{}',
    actor_type TEXT NOT NULL CHECK (actor_type IN ('user', 'api_key', 'system')),
    -- No foreign key: history outlives the user or key that made the change,
    -- whose name at the time is kept in actor_label.
    actor_id UUID,
    actor_label TEXT NOT NULL DEFAULT '',
    -- baseline: the configuration a monitor had before its first recorded
    -- save; system: a change made outside a save (a deleted location,
    -- channel or member monitor).
    source TEXT NOT NULL CHECK (source IN ('ui', 'api', 'mcp', 'import', 'discovery', 'restore', 'baseline', 'system')),
    -- The version a restore re-applied; NULL for every other source.
    restored_from INTEGER,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (monitor_id, version)
);

CREATE INDEX idx_monitor_config_versions_tenant ON monitor_config_versions (tenant_id);
