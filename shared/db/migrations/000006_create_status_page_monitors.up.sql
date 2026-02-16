CREATE TABLE status_page_monitors (
    status_page_id UUID NOT NULL REFERENCES status_pages(id) ON DELETE CASCADE,
    monitor_id UUID NOT NULL REFERENCES monitors(id) ON DELETE CASCADE,
    PRIMARY KEY (status_page_id, monitor_id)
);

CREATE INDEX idx_status_page_monitors_status_page_id ON status_page_monitors(status_page_id);
CREATE INDEX idx_status_page_monitors_monitor_id ON status_page_monitors(monitor_id);

