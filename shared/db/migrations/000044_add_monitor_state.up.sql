-- 000044_add_monitor_state.up.sql
ALTER TABLE monitors
    ADD COLUMN current_state TEXT NOT NULL DEFAULT 'unknown'
        CHECK (current_state IN ('unknown', 'up', 'suspect', 'down')),
    ADD COLUMN consecutive_failures INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN last_state_change_at TIMESTAMPTZ,
    ADD COLUMN consecutive_failures_threshold INTEGER NOT NULL DEFAULT 2
        CHECK (consecutive_failures_threshold BETWEEN 1 AND 10);

-- Sensitivity backfill: monitors bound to a threshold=1 policy keep N=1,
-- everything else gets the new default N=2 (spec §8.2).
UPDATE monitors m
SET consecutive_failures_threshold = 1
FROM alert_policies ap
WHERE m.alert_policy_id = ap.id
  AND ap.failure_threshold = 1;

-- State seeding from each monitor's latest check result (spec §8.2).
WITH latest AS (
    SELECT DISTINCT ON (monitor_id) monitor_id, status, created_at
    FROM check_results
    ORDER BY monitor_id, created_at DESC
)
UPDATE monitors m
SET current_state = CASE WHEN l.status = 'success' THEN 'up' ELSE 'down' END,
    consecutive_failures = CASE WHEN l.status = 'success' THEN 0
                                ELSE m.consecutive_failures_threshold END,
    last_state_change_at = l.created_at
FROM latest l
WHERE l.monitor_id = m.id
  AND m.type <> 'group';

CREATE INDEX idx_monitors_current_state ON monitors(current_state)
    WHERE deleted_at IS NULL;
