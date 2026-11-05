-- location_offline alerts (rule S-F6): a check location that was seen at
-- least once and has sent no heartbeat for the tenant's threshold. The
-- subject is the location (source_location_id), like a mesh edge's source;
-- there is no monitor and no target location. alerts_kind_check already
-- allows the kind (000100); this opens the subject shape.
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

-- One open alert per location; the alerter's ON CONFLICT infers against it,
-- so replicas racing to open the same alert collide here.
CREATE UNIQUE INDEX idx_alerts_one_open_location_offline
    ON alerts(source_location_id)
    WHERE kind = 'location_offline' AND status IN ('active', 'acknowledged');

-- On by default, 3 minutes: workers heartbeat every ~15s, and the UI's
-- connected badge already drops after one minute. Floor 60s.
ALTER TABLE tenants
    ADD COLUMN location_offline_alerts_enabled BOOLEAN NOT NULL DEFAULT TRUE,
    ADD COLUMN location_offline_after_seconds INTEGER NOT NULL DEFAULT 180
        CONSTRAINT tenants_location_offline_after_seconds_check CHECK (location_offline_after_seconds BETWEEN 60 AND 86400);
