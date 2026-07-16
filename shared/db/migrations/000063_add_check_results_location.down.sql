-- 000063_add_check_results_location.down.sql
DROP INDEX IF EXISTS idx_check_results_monitor_location_created;
ALTER TABLE check_results DROP COLUMN IF EXISTS location_id;
