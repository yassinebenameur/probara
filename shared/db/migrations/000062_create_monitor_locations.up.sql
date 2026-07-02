-- 000062_create_monitor_locations.up.sql
-- Which locations a monitor's checks fan out to. No rows = default platform
-- fleet (existing behavior). location_quorum is the spatial down-rule: the
-- monitor is down when at least this many selected locations are down;
-- fewer (but >0) down locations = degraded.
CREATE TABLE monitor_locations (
    monitor_id  UUID NOT NULL REFERENCES monitors(id) ON DELETE CASCADE,
    location_id UUID NOT NULL REFERENCES locations(id) ON DELETE CASCADE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (monitor_id, location_id)
);
CREATE INDEX idx_monitor_locations_location ON monitor_locations(location_id);

ALTER TABLE monitors
    ADD COLUMN location_quorum INTEGER NOT NULL DEFAULT 1
        CHECK (location_quorum >= 1);
