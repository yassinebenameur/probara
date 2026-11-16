-- A relapse notice is a page: an older alerter reads the route as paged.
UPDATE alert_notification_states SET last_event_type = 'created' WHERE last_event_type = 'relapsed';
ALTER TABLE alert_notification_states
    DROP CONSTRAINT IF EXISTS alert_notification_states_last_event_type_check;
-- NOT VALID, like the up: every row satisfies it after the UPDATE above, and
-- it is enforced on every write.
ALTER TABLE alert_notification_states
    ADD CONSTRAINT alert_notification_states_last_event_type_check
        CHECK (last_event_type IN ('created', 'resolved', 'reminder')) NOT VALID;
ALTER TABLE alert_notification_states
    DROP COLUMN IF EXISTS paged_relapse;

DROP TABLE IF EXISTS alert_relapses;

ALTER TABLE alerts
    DROP CONSTRAINT IF EXISTS alerts_relapse_count_check,
    DROP COLUMN IF EXISTS first_ack_episode_started_at,
    DROP COLUMN IF EXISTS auto_resolved,
    DROP COLUMN IF EXISTS reopened_at,
    DROP COLUMN IF EXISTS relapse_count;

ALTER TABLE tenants
    DROP CONSTRAINT IF EXISTS tenants_alert_reopen_cooldown_minutes_check,
    DROP COLUMN IF EXISTS alert_reopen_cooldown_minutes;
