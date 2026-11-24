-- A repeating window falls back to a one-off over its stored first
-- occurrence (starts_at..ends_at); its later occurrences are dropped.
DROP TABLE IF EXISTS maintenance_window_occurrences;

ALTER TABLE maintenance_windows
    DROP CONSTRAINT IF EXISTS maintenance_windows_recurrence_check,
    DROP COLUMN IF EXISTS recurrence_until,
    DROP COLUMN IF EXISTS recurrence_timezone,
    DROP COLUMN IF EXISTS recurrence_duration_minutes,
    DROP COLUMN IF EXISTS recurrence_start_time,
    DROP COLUMN IF EXISTS recurrence_weekdays;
