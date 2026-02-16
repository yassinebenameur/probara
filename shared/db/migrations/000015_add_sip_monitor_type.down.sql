-- Remove SIP from the timeout check constraint

-- Drop the constraint with SIP
ALTER TABLE monitors DROP CONSTRAINT IF EXISTS monitors_timeout_check;

-- Recreate without SIP
ALTER TABLE monitors ADD CONSTRAINT monitors_timeout_check CHECK (
    (type = ANY (ARRAY['http'::text, 'ping'::text])) AND timeout_seconds > 0 AND timeout_seconds < interval_seconds
    OR (type = ANY (ARRAY['agent'::text, 'group'::text, 'push'::text]))
);
