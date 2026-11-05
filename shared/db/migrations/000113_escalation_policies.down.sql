DELETE FROM alert_notification_deliveries WHERE channel_id IS NULL;
ALTER TABLE alert_notification_deliveries
    DROP CONSTRAINT IF EXISTS alert_notification_deliveries_target_check,
    DROP COLUMN IF EXISTS recipient,
    DROP COLUMN IF EXISTS contact_id,
    DROP COLUMN IF EXISTS user_id,
    ALTER COLUMN channel_id SET NOT NULL;

DROP TABLE IF EXISTS alert_escalation_pages;
DROP TABLE IF EXISTS alert_escalation_states;

ALTER TABLE tenants DROP COLUMN IF EXISTS default_escalation_policy_id;

DROP TRIGGER IF EXISTS trg_monitors_clear_escalation_policy ON monitors;
DROP FUNCTION IF EXISTS monitors_clear_escalation_policy();
UPDATE monitors SET notification_mode = 'default' WHERE notification_mode = 'policy';
ALTER TABLE monitors
    DROP CONSTRAINT IF EXISTS monitors_escalation_policy_mode_check,
    DROP CONSTRAINT IF EXISTS monitors_notification_mode_check;
ALTER TABLE monitors DROP COLUMN IF EXISTS escalation_policy_id;
ALTER TABLE monitors
    ADD CONSTRAINT monitors_notification_mode_check CHECK (notification_mode IN ('default', 'custom'));

DROP TABLE IF EXISTS escalation_policy_targets;
DROP TABLE IF EXISTS escalation_policy_steps;
DROP TABLE IF EXISTS escalation_policies;
