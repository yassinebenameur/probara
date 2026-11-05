-- Status page access control: who may view a page.
--   public    anyone (the previous behavior, and the default)
--   unlisted  anyone with the link; responses carry noindex
--   password  a visitor password, or a kiosk token
--   private   kiosk tokens only
-- Columns rather than settings JSONB: the status-page gate reads them before
-- anything else, and a CHECK keeps an unknown value from opening a page.
ALTER TABLE status_pages
    ADD COLUMN visibility TEXT NOT NULL DEFAULT 'public'
        CONSTRAINT status_pages_visibility_check
        CHECK (visibility IN ('public', 'unlisted', 'password', 'private')),
    -- bcrypt hash of the visitor password; NULL when none is set.
    ADD COLUMN password_hash TEXT,
    -- Bumped on every password set or clear. Visitor sessions are signed over
    -- it, so changing the password signs every visitor out.
    ADD COLUMN password_version INTEGER NOT NULL DEFAULT 0;

-- Kiosk tokens: long-lived read credentials for wall displays and embeds.
-- Only a SHA-256 of the token is stored; it is shown once at creation.
-- No last_used_at: the status-page service that checks them is read-only.
CREATE TABLE status_page_access_tokens (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    status_page_id UUID NOT NULL REFERENCES status_pages(id) ON DELETE CASCADE,
    label TEXT NOT NULL,
    token_hash TEXT NOT NULL UNIQUE,
    created_by UUID REFERENCES admin_users(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    revoked_at TIMESTAMPTZ
);

CREATE INDEX idx_status_page_access_tokens_page
    ON status_page_access_tokens(status_page_id, created_at DESC);
