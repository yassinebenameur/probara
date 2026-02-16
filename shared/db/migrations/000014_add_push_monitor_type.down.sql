-- Remove push_token index
DROP INDEX IF EXISTS idx_monitors_push_token;

-- Remove push_token column from monitors table
ALTER TABLE monitors DROP COLUMN IF EXISTS push_token;

-- Revert timeout constraint to not include push
ALTER TABLE monitors DROP CONSTRAINT IF EXISTS monitors_timeout_check;
ALTER TABLE monitors ADD CONSTRAINT monitors_timeout_check 
CHECK (
    (type IN ('http', 'ping') AND timeout_seconds > 0 AND timeout_seconds < interval_seconds)
    OR (type IN ('agent', 'group'))
);
