ALTER TABLE alerts
    DROP COLUMN IF EXISTS snoozed_by_user_id,
    DROP COLUMN IF EXISTS snoozed_at,
    DROP COLUMN IF EXISTS first_acknowledged_at;
