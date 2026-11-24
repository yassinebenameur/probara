ALTER TABLE admin_session_families
    DROP CONSTRAINT IF EXISTS admin_session_families_second_factor_check,
    DROP COLUMN IF EXISTS second_factor;

DROP TABLE IF EXISTS admin_user_recovery_codes;

ALTER TABLE admin_users
    DROP COLUMN IF EXISTS lockout_held,
    DROP COLUMN IF EXISTS second_factor_lockout_at,
    DROP COLUMN IF EXISTS second_factor_lockouts,
    DROP COLUMN IF EXISTS totp_last_step,
    DROP COLUMN IF EXISTS totp_enabled_at,
    DROP COLUMN IF EXISTS totp_secret_enc;
