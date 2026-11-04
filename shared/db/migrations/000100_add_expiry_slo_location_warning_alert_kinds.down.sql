-- Remove alerts of the new kinds before restoring 000078's narrower CHECK.
DELETE FROM alerts WHERE kind IN ('domain_expiry', 'slo_burn', 'location_offline', 'check_warning');

ALTER TABLE alerts
    DROP CONSTRAINT IF EXISTS alerts_kind_check;

ALTER TABLE alerts
    ADD CONSTRAINT alerts_kind_check
        CHECK (kind IN ('availability', 'latency_anomaly', 'host_metric', 'mesh_edge', 'tls_expiry'));
