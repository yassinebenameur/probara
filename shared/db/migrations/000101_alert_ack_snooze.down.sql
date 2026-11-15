ALTER TABLE alerts
    DROP COLUMN IF EXISTS snoozed_until,
    DROP COLUMN IF EXISTS unacknowledged_at,
    DROP COLUMN IF EXISTS acknowledged_by_user_id;
