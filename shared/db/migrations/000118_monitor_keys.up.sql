-- Config-as-code (POST /monitors/apply): a monitor may carry a stable key
-- chosen by the operator, which a monitors document names it by across
-- renames. NULL is an unmanaged monitor, which apply never touches. A key is
-- unique among a workspace's live monitors only, so deleting a managed
-- monitor frees its key. Its format (lowercase slug, at most 64 characters)
-- is validated by the API (api/internal/validation/monitor_key.go).
ALTER TABLE monitors ADD COLUMN key TEXT;

CREATE UNIQUE INDEX idx_monitors_tenant_key
    ON monitors (tenant_id, key)
    WHERE key IS NOT NULL AND deleted_at IS NULL;

-- Configuration versions written by apply carry their own source.
ALTER TABLE monitor_config_versions DROP CONSTRAINT monitor_config_versions_source_check;
ALTER TABLE monitor_config_versions ADD CONSTRAINT monitor_config_versions_source_check
    CHECK (source IN ('ui', 'api', 'mcp', 'import', 'discovery', 'restore', 'baseline', 'system', 'apply'));
