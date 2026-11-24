-- Two alert kinds for features that raise them: domain_expiry (a domain
-- registration inside its expiry window) and location_offline (a check
-- location stopped reporting). alerts_subject_check is unchanged, so each
-- still requires a monitor until the feature that opens it says otherwise.
-- shared/notifications/event.go (Kind*) mirrors this list.
ALTER TABLE alerts
    DROP CONSTRAINT IF EXISTS alerts_kind_check;

ALTER TABLE alerts
    ADD CONSTRAINT alerts_kind_check
        CHECK (kind IN ('availability', 'latency_anomaly', 'host_metric', 'mesh_edge', 'tls_expiry',
                        'domain_expiry', 'location_offline'));
