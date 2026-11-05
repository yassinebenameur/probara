-- Reserve the alert kinds the next features open, in one rewrite so their
-- lanes never race on alerts_kind_check: domain_expiry (domain registration
-- inside its expiry window), slo_burn (SLO error-budget burn rate),
-- location_offline (a check location stopped reporting) and check_warning
-- (a check passed but crossed a warning threshold). alerts_subject_check is
-- unchanged, so each still requires a monitor until its feature says
-- otherwise. shared/notifications/event.go (Kind*) mirrors this list.
ALTER TABLE alerts
    DROP CONSTRAINT IF EXISTS alerts_kind_check;

ALTER TABLE alerts
    ADD CONSTRAINT alerts_kind_check
        CHECK (kind IN ('availability', 'latency_anomaly', 'host_metric', 'mesh_edge', 'tls_expiry',
                        'domain_expiry', 'slo_burn', 'location_offline', 'check_warning'));
