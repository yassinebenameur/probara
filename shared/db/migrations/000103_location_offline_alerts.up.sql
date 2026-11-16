-- location_offline alerts (rule S-F6 in docs/state-semantics.md): a check
-- location that reported before and has been silent for 180 seconds. The
-- location is the subject, not a monitor, so like mesh_edge the alert has no
-- monitor_id; it names its location in source_location_id, which the alert
-- read path already joins for its name, and has no target.
ALTER TABLE alerts
    DROP CONSTRAINT IF EXISTS alerts_subject_check;

ALTER TABLE alerts
    ADD CONSTRAINT alerts_subject_check CHECK (
        (kind = 'mesh_edge'
            AND monitor_id IS NULL
            AND source_location_id IS NOT NULL
            AND target_location_id IS NOT NULL)
        OR (kind = 'location_offline'
            AND monitor_id IS NULL
            AND source_location_id IS NOT NULL
            AND target_location_id IS NULL)
        OR (kind NOT IN ('mesh_edge', 'location_offline') AND monitor_id IS NOT NULL)
    );

-- One open alert per location. A separate partial index, like
-- idx_alerts_one_open_mesh_edge: the alerter's ON CONFLICT infers against
-- it, so replicas racing to open the same alert collide here.
CREATE UNIQUE INDEX idx_alerts_one_open_location_offline
    ON alerts(source_location_id)
    WHERE kind = 'location_offline' AND status IN ('active', 'acknowledged');

-- Why the alerter closed an alert when that is not its condition clearing:
-- a location_offline alert also closes when its location is disabled or
-- deleted, and its notification must not claim the location is back. NULL
-- for every other resolve.
ALTER TABLE alerts
    ADD COLUMN resolve_reason TEXT;
