ALTER TABLE admin_users
    DROP COLUMN IF EXISTS password_changed_at,
    DROP COLUMN IF EXISTS locked_until,
    DROP COLUMN IF EXISTS last_failed_login_at,
    DROP COLUMN IF EXISTS failed_login_count;
