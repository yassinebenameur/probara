ALTER TABLE alerts
    DROP COLUMN IF EXISTS resolve_reason;

DROP INDEX IF EXISTS idx_alerts_one_open_location_offline;

-- The restored subject check requires a monitor for this kind; these rows
-- have none.
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
