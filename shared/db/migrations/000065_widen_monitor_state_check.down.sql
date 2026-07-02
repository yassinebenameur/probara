-- 000065_widen_monitor_state_check.down.sql
UPDATE monitors SET current_state = 'suspect' WHERE current_state = 'degraded';
ALTER TABLE monitors DROP CONSTRAINT IF EXISTS monitors_current_state_check;
ALTER TABLE monitors
    ADD CONSTRAINT monitors_current_state_check
        CHECK (current_state IN ('unknown', 'up', 'suspect', 'down'));
