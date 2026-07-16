-- 000044_add_monitor_state.down.sql
DROP INDEX IF EXISTS idx_monitors_current_state;
ALTER TABLE monitors
    DROP COLUMN IF EXISTS current_state,
    DROP COLUMN IF EXISTS consecutive_failures,
    DROP COLUMN IF EXISTS last_state_change_at,
    DROP COLUMN IF EXISTS consecutive_failures_threshold;
