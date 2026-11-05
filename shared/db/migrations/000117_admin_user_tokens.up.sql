-- One-time account tokens: invites, password resets and email-change
-- confirmations (adminauth/tokens.go). Only the SHA-256 of a token is
-- stored; it is single use (used_at), expires, and issuing a new token of
-- the same purpose supersedes the user's unused ones.
CREATE TABLE admin_user_tokens (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    admin_user_id UUID NOT NULL REFERENCES admin_users(id) ON DELETE CASCADE,
    purpose TEXT NOT NULL,
    token_hash TEXT NOT NULL,
    new_email TEXT,
    expires_at TIMESTAMPTZ NOT NULL,
    used_at TIMESTAMPTZ,
    created_by UUID REFERENCES admin_users(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT admin_user_tokens_purpose_check
        CHECK (purpose IN ('invite', 'password_reset', 'email_change')),
    CONSTRAINT admin_user_tokens_new_email_check
        CHECK ((purpose = 'email_change') = (new_email IS NOT NULL)),
    CONSTRAINT admin_user_tokens_token_hash_key UNIQUE (token_hash)
);

CREATE INDEX idx_admin_user_tokens_open
    ON admin_user_tokens(admin_user_id, purpose) WHERE used_at IS NULL;
