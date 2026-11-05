-- TOTP second factor for local accounts (adminauth/mfa.go).
-- totp_secret_enc is the base32 secret sealed with the PROBARA_SECRETS_KEY
-- encryptor; it is set but totp_enabled_at is NULL while an enrollment is
-- pending confirmation. totp_last_step is the last accepted RFC 6238 time
-- step: a code is accepted only for a later step, so a code is never
-- accepted twice.
ALTER TABLE admin_users
    ADD COLUMN totp_secret_enc TEXT,
    ADD COLUMN totp_enabled_at TIMESTAMPTZ,
    ADD COLUMN totp_last_step BIGINT;

-- Single-use recovery codes, stored as SHA-256 hashes.
CREATE TABLE admin_user_recovery_codes (
    id BIGSERIAL PRIMARY KEY,
    admin_user_id UUID NOT NULL REFERENCES admin_users(id) ON DELETE CASCADE,
    code_hash TEXT NOT NULL,
    used_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT admin_user_recovery_codes_unique UNIQUE (admin_user_id, code_hash)
);
