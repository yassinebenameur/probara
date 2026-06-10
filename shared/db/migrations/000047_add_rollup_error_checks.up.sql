ALTER TABLE monitor_hourly_rollups ADD COLUMN error_checks INTEGER NOT NULL DEFAULT 0;
ALTER TABLE monitor_daily_rollups ADD COLUMN error_checks INTEGER NOT NULL DEFAULT 0;
