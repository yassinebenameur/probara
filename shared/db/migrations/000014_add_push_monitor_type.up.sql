-- Add push_token column to monitors table for push monitor identification
ALTER TABLE monitors ADD COLUMN push_token TEXT UNIQUE;

-- Add index on push_token for fast lookups
CREATE INDEX idx_monitors_push_token ON monitors(push_token) WHERE push_token IS NOT NULL;

-- Update timeout constraint to allow push monitors (like agent/group, push doesn't need timeout validation)
ALTER TABLE monitors DROP CONSTRAINT IF EXISTS monitors_timeout_check;
ALTER TABLE monitors ADD CONSTRAINT monitors_timeout_check 
CHECK (
    (type IN ('http', 'ping') AND timeout_seconds > 0 AND timeout_seconds < interval_seconds)
    OR (type IN ('agent', 'group', 'push'))
);

-- Note: Monitor type is stored as TEXT, so 'push' type is implicitly supported
-- No enum constraint exists, so no ALTER TYPE needed
