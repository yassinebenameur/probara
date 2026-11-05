-- A reopened claim reads as a fresh page once the event type is gone.
UPDATE alert_notification_states SET last_event_type = 'created' WHERE last_event_type = 'reopened';
ALTER TABLE alert_notification_states
    DROP CONSTRAINT IF EXISTS alert_notification_states_last_event_type_check;
ALTER TABLE alert_notification_states
    ADD CONSTRAINT alert_notification_states_last_event_type_check
        CHECK (last_event_type IN ('created', 'resolved', 'reminder'));

ALTER TABLE alerts
    DROP COLUMN IF EXISTS first_ack_episode_started_at,
    DROP COLUMN IF EXISTS first_acknowledged_at,
    DROP COLUMN IF EXISTS resolved_via,
    DROP COLUMN IF EXISTS resolved_by_user_id,
    DROP COLUMN IF EXISTS reopened_at,
    DROP COLUMN IF EXISTS relapse_count;

ALTER TABLE tenants
    DROP COLUMN IF EXISTS alert_reopen_cooldown_seconds;
