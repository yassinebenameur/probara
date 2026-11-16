DROP TABLE IF EXISTS status_page_access_tokens;

ALTER TABLE status_pages
    DROP COLUMN IF EXISTS password_version,
    DROP COLUMN IF EXISTS password_hash,
    DROP COLUMN IF EXISTS visibility;
