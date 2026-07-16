ALTER TABLE locations
    ADD COLUMN worker_credential TEXT;

COMMENT ON COLUMN locations.worker_credential IS
    'Per-location worker credential, encrypted with the platform secrets key';
