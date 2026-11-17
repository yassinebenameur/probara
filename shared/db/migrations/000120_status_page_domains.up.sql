-- Custom domains for status pages (shared/pagedomains holds the contract).
-- The API is the only writer: operators add and remove domains, and its
-- runner verifies them (a TXT record holding verification_token, and a
-- CNAME to STATUS_PAGE_CNAME_TARGET). The status-page service reads the
-- verified ones to serve a page on its own hostname.
--
-- No tenant_id: a domain belongs to its page, and every tenant-scoped query
-- joins through status_pages, so a deleted page (or a deleted workspace's
-- pages) takes its domains with it.
CREATE TABLE status_page_domains (
    id                   UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    status_page_id       UUID NOT NULL REFERENCES status_pages(id) ON DELETE CASCADE,
    -- Lower-case ASCII (punycode for internationalized names), no trailing
    -- dot: the form the status-page service matches the Host header in.
    hostname             TEXT NOT NULL
        CONSTRAINT status_page_domains_hostname_check
        CHECK (hostname = lower(hostname) AND char_length(hostname) BETWEEN 3 AND 253 AND hostname NOT LIKE '%.'),
    verification_token   TEXT NOT NULL,
    status               TEXT NOT NULL DEFAULT 'pending'
        CONSTRAINT status_page_domains_status_check
        CHECK (status IN ('pending', 'verified', 'failed')),
    -- Outcome of the last check, per record (NULL until checked), and why it
    -- failed, so the UI can say which record is missing.
    txt_ok               BOOLEAN,
    cname_ok             BOOLEAN,
    last_error           TEXT,
    last_checked_at      TIMESTAMPTZ,
    -- When the domain last turned verified.
    verified_at          TIMESTAMPTZ,
    -- First failed check of a verified domain's current failing streak; NULL
    -- while its checks pass. The domain turns failed only once the streak is
    -- long enough, so one bad lookup never takes a working page down.
    failing_since        TIMESTAMPTZ,
    consecutive_failures INTEGER NOT NULL DEFAULT 0,
    -- When the runner checks it next; claimed with FOR UPDATE SKIP LOCKED.
    next_check_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_at           TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- One hostname per installation, whatever its status or workspace.
CREATE UNIQUE INDEX idx_status_page_domains_hostname ON status_page_domains (hostname);
-- A page's domains, oldest first (shared/pagedomains.PrimaryHostSQL).
CREATE INDEX idx_status_page_domains_page ON status_page_domains (status_page_id, created_at);
CREATE INDEX idx_status_page_domains_due ON status_page_domains (next_check_at);

-- The host a visitor subscribed from. A browser keeps one push subscription
-- per origin, so a subscription made on a custom domain is not the one made
-- on the platform host, and its notifications open the page where it was
-- made. NULL: the platform host.
ALTER TABLE status_page_push_subscriptions ADD COLUMN origin_host TEXT;
