DROP TABLE IF EXISTS admin_user_recovery_codes;

ALTER TABLE admin_users
    DROP COLUMN IF EXISTS totp_last_step,
    DROP COLUMN IF EXISTS totp_enabled_at,
    DROP COLUMN IF EXISTS totp_secret_enc;
