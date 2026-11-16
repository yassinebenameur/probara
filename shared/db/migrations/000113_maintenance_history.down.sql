-- Without deleted_at a kept-for-history window would come back as live, so
-- deleted windows go for good (their occurrences cascade).
DELETE FROM maintenance_windows WHERE deleted_at IS NOT NULL;

ALTER TABLE maintenance_windows DROP COLUMN IF EXISTS deleted_at;

DROP TABLE IF EXISTS maintenance_window_past_targets;

ALTER TABLE maintenance_window_monitors DROP COLUMN IF EXISTS linked_at;
