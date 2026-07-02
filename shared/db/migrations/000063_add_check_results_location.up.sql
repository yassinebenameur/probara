-- 000063_add_check_results_location.up.sql
-- Which vantage point produced a result. NULL = default platform fleet (and
-- all pre-existing rows). ON DELETE SET NULL keeps history when a location is
-- removed. The partial index keeps the location-less majority out.
ALTER TABLE check_results
    ADD COLUMN location_id UUID REFERENCES locations(id) ON DELETE SET NULL;

CREATE INDEX idx_check_results_monitor_location_created
    ON check_results(monitor_id, location_id, created_at DESC)
    WHERE location_id IS NOT NULL;
