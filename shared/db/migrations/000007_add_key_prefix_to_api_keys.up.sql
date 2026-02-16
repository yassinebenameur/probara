-- Add key_prefix column for fast API key lookup
ALTER TABLE api_keys ADD COLUMN IF NOT EXISTS key_prefix TEXT;

-- Create index on key_prefix for fast lookups
CREATE INDEX IF NOT EXISTS idx_api_keys_key_prefix ON api_keys(key_prefix) WHERE revoked_at IS NULL;

-- Update existing rows to have key_prefix (will be NULL for existing keys, but that's okay)
-- New keys will have key_prefix populated

