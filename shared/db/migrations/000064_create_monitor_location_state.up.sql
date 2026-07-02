-- 000064_create_monitor_location_state.up.sql
-- Per-(monitor, location) temporal state: the same consecutive-failures
-- machine monitors run globally, tracked per vantage point. The ingest
-- consumer applies it per result, then aggregates across locations into
-- monitors.current_state via the quorum rule.
CREATE TABLE monitor_location_state (
    monitor_id           UUID NOT NULL REFERENCES monitors(id) ON DELETE CASCADE,
    location_id          UUID NOT NULL REFERENCES locations(id) ON DELETE CASCADE,
    tenant_id            UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    current_state        TEXT NOT NULL DEFAULT 'unknown'
        CHECK (current_state IN ('unknown', 'up', 'suspect', 'down')),
    consecutive_failures INTEGER NOT NULL DEFAULT 0,
    last_latency_ms      BIGINT,
    last_check_at        TIMESTAMPTZ,
    last_state_change_at TIMESTAMPTZ,
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (monitor_id, location_id)
);
CREATE INDEX idx_monitor_location_state_location ON monitor_location_state(location_id);
