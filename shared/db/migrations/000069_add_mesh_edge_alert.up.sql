-- Mesh-edge alerts: a directed location pair (source → target) is the alert
-- subject, not a monitor. monitor_id becomes nullable for this kind only;
-- every other kind keeps requiring a monitor via alerts_subject_check.
ALTER TABLE alerts
    ALTER COLUMN monitor_id DROP NOT NULL;

ALTER TABLE alerts
    DROP CONSTRAINT IF EXISTS alerts_kind_check;

ALTER TABLE alerts
    ADD CONSTRAINT alerts_kind_check
        CHECK (kind IN ('availability', 'latency_anomaly', 'host_metric', 'mesh_edge'));

ALTER TABLE alerts
    ADD COLUMN source_location_id UUID REFERENCES locations(id) ON DELETE CASCADE,
    ADD COLUMN target_location_id UUID REFERENCES locations(id) ON DELETE CASCADE;

ALTER TABLE alerts
    ADD CONSTRAINT alerts_subject_check CHECK (
        (kind = 'mesh_edge'
            AND monitor_id IS NULL
            AND source_location_id IS NOT NULL
            AND target_location_id IS NOT NULL)
        OR (kind <> 'mesh_edge' AND monitor_id IS NOT NULL)
    );

-- One open alert per directed edge. Deliberately a SEPARATE partial index:
-- idx_alerts_one_open_per_monitor_kind must keep its exact expression — the
-- ON CONFLICT clauses in the alerter infer against it, and NULL monitor_id
-- rows would never collide in it anyway.
CREATE UNIQUE INDEX idx_alerts_one_open_mesh_edge
    ON alerts(source_location_id, target_location_id)
    WHERE kind = 'mesh_edge' AND status IN ('active', 'acknowledged');
