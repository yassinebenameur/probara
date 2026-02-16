ALTER TABLE status_page_monitors
ADD COLUMN position INTEGER NOT NULL DEFAULT 0;

CREATE INDEX idx_status_page_monitors_status_page_id_position
ON status_page_monitors(status_page_id, position);

