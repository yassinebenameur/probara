DROP INDEX IF EXISTS idx_api_keys_key_prefix;
ALTER TABLE api_keys DROP COLUMN IF EXISTS key_prefix;

