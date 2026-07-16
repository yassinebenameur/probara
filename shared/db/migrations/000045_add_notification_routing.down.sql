DROP INDEX IF EXISTS idx_alerts_one_open_per_monitor;
ALTER TABLE alerts ALTER COLUMN alert_policy_id SET NOT NULL;
ALTER TABLE tenants
    DROP COLUMN IF EXISTS alert_reminder_seconds,
    DROP COLUMN IF EXISTS auto_create_incident;
ALTER TABLE monitors DROP COLUMN IF EXISTS notification_mode;
DROP TABLE IF EXISTS monitor_channels;
DROP TABLE IF EXISTS tenant_default_channels;
