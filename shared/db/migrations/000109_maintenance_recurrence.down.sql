DROP TABLE IF EXISTS maintenance_window_past_targets;

ALTER TABLE maintenance_window_monitors DROP COLUMN IF EXISTS linked_at;

DROP TABLE IF EXISTS maintenance_window_occurrences;

ALTER TABLE maintenance_windows
    DROP CONSTRAINT IF EXISTS maintenance_windows_cron_check,
    DROP CONSTRAINT IF EXISTS maintenance_windows_monthday_check,
    DROP CONSTRAINT IF EXISTS maintenance_windows_interval_check,
    DROP CONSTRAINT IF EXISTS maintenance_windows_recurrence_check,
    DROP COLUMN IF EXISTS recurrence_until,
    DROP COLUMN IF EXISTS timezone,
    DROP COLUMN IF EXISTS cron_expr,
    DROP COLUMN IF EXISTS by_monthday,
    DROP COLUMN IF EXISTS by_weekday,
    DROP COLUMN IF EXISTS interval_n,
    DROP COLUMN IF EXISTS recurrence;
