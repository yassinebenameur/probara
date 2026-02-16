ALTER TABLE status_pages
ADD COLUMN settings JSONB NOT NULL DEFAULT '{}'::jsonb;

