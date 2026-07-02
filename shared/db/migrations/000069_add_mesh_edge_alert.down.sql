-- Remove mesh alerts before restoring the NOT NULL and narrower kind CHECK.
DELETE FROM alerts WHERE kind = 'mesh_edge';

DROP INDEX IF EXISTS idx_alerts_one_open_mesh_edge;

ALTER TABLE alerts
    DROP CONSTRAINT IF EXISTS alerts_subject_check;

ALTER TABLE alerts
    DROP COLUMN IF EXISTS source_location_id,
    DROP COLUMN IF EXISTS target_location_id;

ALTER TABLE alerts
    DROP CONSTRAINT IF EXISTS alerts_kind_check;

ALTER TABLE alerts
    ADD CONSTRAINT alerts_kind_check
        CHECK (kind IN ('availability', 'latency_anomaly', 'host_metric'));

ALTER TABLE alerts
    ALTER COLUMN monitor_id SET NOT NULL;
