DROP INDEX IF EXISTS idx_status_page_monitors_status_page_id_position;

ALTER TABLE status_page_monitors
DROP COLUMN IF EXISTS position;

