-- Restore original CHECK that required both auto_monitor_id and auto_alert_policy_id.
ALTER TABLE incidents DROP CONSTRAINT IF EXISTS incidents_auto_fields_check;
ALTER TABLE incidents ADD CONSTRAINT incidents_auto_fields_check
    CHECK ((NOT is_auto_created) OR (auto_monitor_id IS NOT NULL AND auto_alert_policy_id IS NOT NULL));
