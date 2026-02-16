-- Add config column to monitors table
ALTER TABLE monitors ADD COLUMN config JSONB;

-- Migrate existing HTTP monitor data to config column
UPDATE monitors SET config = jsonb_build_object(
    'url', url,
    'method', method,
    'headers', COALESCE(headers, '{}'::jsonb),
    'body', body,
    'expected_status', expected_status,
    'expected_body_substring', expected_body_substring
) WHERE type = 'http';

-- Make config column NOT NULL after migration
ALTER TABLE monitors ALTER COLUMN config SET NOT NULL;

-- Drop old HTTP-specific columns
ALTER TABLE monitors DROP COLUMN url;
ALTER TABLE monitors DROP COLUMN method;
ALTER TABLE monitors DROP COLUMN headers;
ALTER TABLE monitors DROP COLUMN body;
ALTER TABLE monitors DROP COLUMN expected_status;
ALTER TABLE monitors DROP COLUMN expected_body_substring;

-- Add index on config for better query performance
CREATE INDEX idx_monitors_config ON monitors USING GIN(config);

