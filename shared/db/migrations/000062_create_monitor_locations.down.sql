-- 000062_create_monitor_locations.down.sql
ALTER TABLE monitors DROP COLUMN IF EXISTS location_quorum;
DROP TABLE IF EXISTS monitor_locations;
