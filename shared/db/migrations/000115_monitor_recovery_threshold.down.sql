ALTER TABLE monitor_location_state
    DROP COLUMN IF EXISTS consecutive_successes;

ALTER TABLE monitors
    DROP COLUMN IF EXISTS consecutive_successes,
    DROP COLUMN IF EXISTS recovery_threshold;
