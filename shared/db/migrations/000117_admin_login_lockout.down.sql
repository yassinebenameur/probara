ALTER TABLE admin_users
    DROP COLUMN IF EXISTS locked_until,
    DROP COLUMN IF EXISTS failed_login_count;
