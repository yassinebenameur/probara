ALTER TABLE alerts
    DROP COLUMN IF EXISTS snoozed_by_user_id,
    DROP COLUMN IF EXISTS snoozed_until,
    DROP COLUMN IF EXISTS unacknowledged_at,
    DROP COLUMN IF EXISTS acknowledged_via,
    DROP COLUMN IF EXISTS acknowledged_by_user_id;

ALTER TABLE tenant_default_channels
    DROP COLUMN IF EXISTS notify_after_ack;

ALTER TABLE monitor_channels
    DROP COLUMN IF EXISTS notify_after_ack;

ALTER TABLE tenants
    DROP COLUMN IF EXISTS ack_stops_notifications;
