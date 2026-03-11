CREATE TABLE status_page_sections (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    status_page_id UUID NOT NULL REFERENCES status_pages(id) ON DELETE CASCADE,
    title TEXT NOT NULL,
    position INTEGER NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_status_page_sections_status_page_id_position
ON status_page_sections(status_page_id, position);

CREATE TABLE status_page_section_monitors (
    section_id UUID NOT NULL REFERENCES status_page_sections(id) ON DELETE CASCADE,
    monitor_id UUID NOT NULL REFERENCES monitors(id) ON DELETE CASCADE,
    position INTEGER NOT NULL DEFAULT 0,
    display_name TEXT,
    PRIMARY KEY (section_id, monitor_id)
);

CREATE INDEX idx_status_page_section_monitors_section_id_position
ON status_page_section_monitors(section_id, position);

CREATE INDEX idx_status_page_section_monitors_monitor_id
ON status_page_section_monitors(monitor_id);

WITH created_sections AS (
    INSERT INTO status_page_sections (status_page_id, title, position, created_at, updated_at)
    SELECT sp.id, 'Services', 0, NOW(), NOW()
    FROM status_pages sp
    WHERE EXISTS (
        SELECT 1
        FROM status_page_monitors spm
        WHERE spm.status_page_id = sp.id
    )
    RETURNING id, status_page_id
)
INSERT INTO status_page_section_monitors (section_id, monitor_id, position, display_name)
SELECT cs.id, spm.monitor_id, spm.position, spm.display_name
FROM created_sections cs
JOIN status_page_monitors spm ON spm.status_page_id = cs.status_page_id
ORDER BY cs.status_page_id, spm.position, spm.monitor_id;
