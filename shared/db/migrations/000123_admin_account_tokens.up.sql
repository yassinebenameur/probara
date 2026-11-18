-- Single-use account tokens (api/internal/services/adminauth/tokens.go):
-- invite and password-reset links, email-change confirmations, and the
-- second step of a two-factor sign-in. Only the SHA-256 of a token is stored;
-- the plain value exists once, in a link or a sign-in response. A token works
-- once (used_at), until expires_at, and only for its purpose; revoked_at
-- retires it early (a newer link of the same purpose, or a credential reset,
-- which retires every open token of the user).
CREATE TABLE admin_account_tokens (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    admin_user_id UUID NOT NULL REFERENCES admin_users(id) ON DELETE CASCADE,
    purpose TEXT NOT NULL,
    token_hash TEXT NOT NULL,
    -- The address an email-change token confirms; NULL for every other purpose.
    new_email TEXT,
    expires_at TIMESTAMPTZ NOT NULL,
    used_at TIMESTAMPTZ,
    revoked_at TIMESTAMPTZ,
    -- The superadmin who issued an invite or reset link; NULL when the user
    -- asked themselves (forgot password, email change, sign-in).
    created_by UUID REFERENCES admin_users(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT admin_account_tokens_purpose_check
        CHECK (purpose IN ('invite', 'password_reset', 'email_change', 'mfa_challenge')),
    CONSTRAINT admin_account_tokens_new_email_check
        CHECK ((purpose = 'email_change') = (new_email IS NOT NULL)),
    CONSTRAINT admin_account_tokens_token_hash_key UNIQUE (token_hash)
);

-- Open tokens of a user, by purpose: retiring them and the forgot-password
-- mail cap read this.
CREATE INDEX idx_admin_account_tokens_user_purpose
    ON admin_account_tokens (admin_user_id, purpose, created_at);

-- The session pruner deletes long-expired rows.
CREATE INDEX idx_admin_account_tokens_expires_at ON admin_account_tokens (expires_at);
