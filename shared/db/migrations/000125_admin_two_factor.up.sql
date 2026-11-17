-- Optional TOTP second factor for password accounts
-- (api/internal/services/adminauth/twofactor.go).
-- totp_secret_enc is the base32 secret sealed with the PROBARA_SECRETS_KEY
-- encryptor (never stored in plaintext: enrolment is refused without a key).
-- It is set with totp_enabled_at NULL while an enrolment waits for its
-- confirming code. totp_last_step is the last accepted RFC 6238 time step: a
-- code is accepted only for a later step, so no code works twice.
ALTER TABLE admin_users
    ADD COLUMN totp_secret_enc TEXT,
    ADD COLUMN totp_enabled_at TIMESTAMPTZ,
    ADD COLUMN totp_last_step BIGINT;

-- Single-use recovery codes, stored as SHA-256 hashes; regenerating replaces
-- the whole set.
CREATE TABLE admin_user_recovery_codes (
    id BIGSERIAL PRIMARY KEY,
    admin_user_id UUID NOT NULL REFERENCES admin_users(id) ON DELETE CASCADE,
    code_hash TEXT NOT NULL,
    used_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT admin_user_recovery_codes_unique UNIQUE (admin_user_id, code_hash)
);

-- How a sign-in passed its second step, shown in the session list: 'totp',
-- 'recovery_code', or NULL for a sign-in without one (password alone, SSO).
ALTER TABLE admin_session_families
    ADD COLUMN second_factor TEXT,
    ADD CONSTRAINT admin_session_families_second_factor_check
        CHECK (second_factor IS NULL OR second_factor IN ('totp', 'recovery_code'));
