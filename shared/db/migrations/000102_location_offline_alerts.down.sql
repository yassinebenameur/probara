ALTER TABLE tenants
    DROP COLUMN IF EXISTS location_offline_after_seconds,
    DROP COLUMN IF EXISTS location_offline_alerts_enabled;

DROP INDEX IF EXISTS idx_alerts_one_open_location_offline;

ALTER TABLE alerts
    DROP COLUMN IF EXISTS resolve_reason;

-- The restored subject check would reject them.
DELETE FROM alerts WHERE kind = 'location_offline';

ALTER TABLE alerts
    DROP CONSTRAINT IF EXISTS alerts_subject_check;

ALTER TABLE alerts
    ADD CONSTRAINT alerts_subject_check CHECK (
        (kind = 'mesh_edge'
            AND monitor_id IS NULL
            AND source_location_id IS NOT NULL
            AND target_location_id IS NOT NULL)
        OR (kind <> 'mesh_edge' AND monitor_id IS NOT NULL)
    );
