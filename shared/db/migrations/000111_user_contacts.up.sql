-- Verified per-user contacts (people layer). A contact is one address a user
-- can be paged at: an email address or an SMS number. The address is a
-- secrets envelope (PROBARA_SECRETS_KEY); the verification code is stored
-- only as a hash, with an expiry and a guess counter. Contacts belong to the
-- user, not to a workspace.
CREATE TABLE user_contacts (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    user_id UUID NOT NULL REFERENCES admin_users(id) ON DELETE CASCADE,
    kind TEXT NOT NULL CHECK (kind IN ('email', 'sms')),
    address TEXT NOT NULL,
    label TEXT NOT NULL DEFAULT '',
    verified_at TIMESTAMPTZ,
    code_hash TEXT,
    code_expires_at TIMESTAMPTZ,
    code_sent_at TIMESTAMPTZ,
    attempts INT NOT NULL DEFAULT 0,
    last_test_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_user_contacts_user ON user_contacts(user_id);

-- Every verification code and test message a user triggers, for the hourly
-- per-user send cap (rows older than a day are pruned on the next send).
CREATE TABLE user_contact_sends (
    user_id UUID NOT NULL REFERENCES admin_users(id) ON DELETE CASCADE,
    sent_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_user_contact_sends_user ON user_contact_sends(user_id, sent_at);

-- The workspace's channel for texting people directly (verification codes
-- now, on-call pages later). It must be a channel whose plugin can address a
-- single recipient (Twilio SMS).
ALTER TABLE tenants
    ADD COLUMN personal_sms_channel_id UUID REFERENCES alert_channels(id) ON DELETE SET NULL;
