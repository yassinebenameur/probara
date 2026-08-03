-- Remove tls_expiry alerts before restoring the narrower kind CHECK.
DELETE FROM alerts WHERE kind = 'tls_expiry';

ALTER TABLE alerts
    DROP CONSTRAINT IF EXISTS alerts_kind_check;

ALTER TABLE alerts
    ADD CONSTRAINT alerts_kind_check
        CHECK (kind IN ('availability', 'latency_anomaly', 'host_metric', 'mesh_edge'));
