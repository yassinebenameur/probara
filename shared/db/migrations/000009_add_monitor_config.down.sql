-- Restore HTTP-specific columns
ALTER TABLE monitors ADD COLUMN url TEXT;
ALTER TABLE monitors ADD COLUMN method TEXT;
ALTER TABLE monitors ADD COLUMN headers JSONB;
ALTER TABLE monitors ADD COLUMN body TEXT;
ALTER TABLE monitors ADD COLUMN expected_status INTEGER;
ALTER TABLE monitors ADD COLUMN expected_body_substring TEXT;

-- Migrate config data back to individual columns for HTTP monitors
UPDATE monitors SET
    url = config->>'url',
    method = config->>'method',
    headers = config->'headers',
    body = config->>'body',
    expected_status = (config->>'expected_status')::INTEGER,
    expected_body_substring = config->>'expected_body_substring'
WHERE type = 'http';

-- Set NOT NULL constraints on restored columns
ALTER TABLE monitors ALTER COLUMN url SET NOT NULL;
ALTER TABLE monitors ALTER COLUMN method SET NOT NULL;
ALTER TABLE monitors ALTER COLUMN method SET DEFAULT 'GET';

-- Drop config column
DROP INDEX IF EXISTS idx_monitors_config;
ALTER TABLE monitors DROP COLUMN config;

