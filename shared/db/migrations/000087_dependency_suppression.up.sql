-- Dependency-aware alerting: root-cause-only paging.
--
-- The dependency graph (000050) and the per-alert root-cause annotation
-- (000051) already exist. This migration adds the opt-in policy that turns the
-- annotation into notification suppression: a downstream alert whose monitor
-- has a down upstream dependency still opens, still resolves and still shows in
-- the UI, but dispatches no notification while the root cause is down.
--
-- Policy resolution: monitors.dependency_suppression overrides the tenant
-- default when it is 'on' or 'off'; 'inherit' (the default) defers to
-- tenants.dependency_suppression_enabled, which is FALSE for every existing
-- tenant so nothing changes on upgrade.
--
-- alerts.root_cause_cleared_at is the grace clock: the alerter stamps it when
-- an alert's root cause clears (the upstream recovered) and the downstream
-- stays suppressed for dependency_suppression_grace_seconds more, so a
-- downstream that recovers one check later never pages. It is reset to NULL
-- whenever a root cause is set again.
ALTER TABLE tenants
    ADD COLUMN dependency_suppression_enabled BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN dependency_suppression_grace_seconds INTEGER NOT NULL DEFAULT 120
        CHECK (dependency_suppression_grace_seconds >= 0);

ALTER TABLE monitors
    ADD COLUMN dependency_suppression TEXT NOT NULL DEFAULT 'inherit'
        CHECK (dependency_suppression IN ('inherit', 'on', 'off'));

ALTER TABLE alerts
    ADD COLUMN root_cause_cleared_at TIMESTAMPTZ;
