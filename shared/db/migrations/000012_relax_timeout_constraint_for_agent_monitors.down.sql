-- Drop the new constraint
ALTER TABLE monitors DROP CONSTRAINT IF EXISTS monitors_timeout_check;

-- Restore the original constraint
ALTER TABLE monitors ADD CONSTRAINT monitors_check 
CHECK (timeout_seconds > 0 AND timeout_seconds < interval_seconds);

