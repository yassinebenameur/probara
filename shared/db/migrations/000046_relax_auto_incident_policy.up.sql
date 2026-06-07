-- Lifecycle alerts carry no alert policy; auto-incidents only require a monitor.
ALTER TABLE incidents DROP CONSTRAINT IF EXISTS incidents_auto_fields_check;
ALTER TABLE incidents ADD CONSTRAINT incidents_auto_fields_check
    CHECK ((NOT is_auto_created) OR (auto_monitor_id IS NOT NULL));
