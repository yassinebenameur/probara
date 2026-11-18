-- Versions written by apply read as API saves once the source is gone.
UPDATE monitor_config_versions SET source = 'api' WHERE source = 'apply';
ALTER TABLE monitor_config_versions DROP CONSTRAINT monitor_config_versions_source_check;
ALTER TABLE monitor_config_versions ADD CONSTRAINT monitor_config_versions_source_check
    CHECK (source IN ('ui', 'api', 'mcp', 'import', 'discovery', 'restore', 'baseline', 'system'));

DROP INDEX IF EXISTS idx_monitors_tenant_key;
ALTER TABLE monitors DROP COLUMN IF EXISTS key;
