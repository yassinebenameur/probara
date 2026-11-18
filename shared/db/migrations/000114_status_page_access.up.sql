-- Who may view a status page (shared/pageaccess holds the same list):
--   public    anyone (the previous behavior, and the default)
--   unlisted  anyone with the link; every response says noindex
--   password  the visitor password, or a kiosk token
--   private   kiosk tokens only; anyone else sees "not found"
-- Columns rather than settings JSONB: the status-page gate reads them before
-- anything else, and the CHECK keeps an unknown value from opening a page.
ALTER TABLE status_pages
    ADD COLUMN visibility TEXT NOT NULL DEFAULT 'public'
        CONSTRAINT status_pages_visibility_check
        CHECK (visibility IN ('public', 'unlisted', 'password', 'private')),
    -- bcrypt hash of the visitor password; NULL when none is set. Written by
    -- the API only; the status-page service reads it.
    ADD COLUMN password_hash TEXT,
    -- Bumped on every password set or clear. Password sessions are signed
    -- over it, so changing the password signs those visitors out.
    ADD COLUMN password_version INTEGER NOT NULL DEFAULT 0;

-- Kiosk tokens: long-lived read credentials for wall displays. Only a SHA-256
-- of the token is stored; the token is shown once, at creation. There is no
-- last_used_at: the status-page service that checks tokens is read-only.
CREATE TABLE status_page_access_tokens (
    id                    UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    status_page_id        UUID NOT NULL REFERENCES status_pages(id) ON DELETE CASCADE,
    label                 TEXT NOT NULL,
    token_hash            TEXT NOT NULL UNIQUE,
    created_by_user_id    UUID REFERENCES admin_users(id) ON DELETE SET NULL,
    created_by_api_key_id UUID REFERENCES api_keys(id) ON DELETE SET NULL,
    created_at            TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    revoked_at            TIMESTAMPTZ
);

CREATE INDEX idx_status_page_access_tokens_page
    ON status_page_access_tokens (status_page_id, created_at DESC);
