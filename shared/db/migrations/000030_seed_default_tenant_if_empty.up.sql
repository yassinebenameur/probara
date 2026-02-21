INSERT INTO tenants (id, name, created_at, updated_at)
SELECT
  '00000000-0000-0000-0000-000000000001'::uuid,
  'Default',
  NOW(),
  NOW()
WHERE NOT EXISTS (
  SELECT 1 FROM tenants
);
