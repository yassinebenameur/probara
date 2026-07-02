-- 000066_add_alert_failing_locations.up.sql
-- Which locations were failing when a multi-location monitor's alert opened
-- (refreshed each alerter tick while open). JSON array of
-- {"id","name","down_since"}. NULL for location-less monitors.
ALTER TABLE alerts ADD COLUMN failing_locations JSONB;
