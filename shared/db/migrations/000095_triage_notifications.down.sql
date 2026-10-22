ALTER TABLE ai_settings
    DROP COLUMN IF EXISTS triage_notifications_enabled_at,
    DROP COLUMN IF EXISTS triage_notifications_enabled;
ALTER TABLE alert_notification_states DROP COLUMN IF EXISTS suspected_cause_sent_at;
