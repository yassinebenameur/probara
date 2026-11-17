-- Email subscribers of a status page (api/internal/services/pagesubscribers).
-- Visitors subscribe through the page (the status-page service forwards the
-- form to the API, which alone writes this table) and confirm by a signed
-- link (double opt-in). Only confirmed rows are mailed. Unsubscribing, or an
-- operator removing an address, deletes the row: nothing about a former
-- subscriber is kept.
CREATE TABLE status_page_subscribers (
    id                   UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id            UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    status_page_id       UUID NOT NULL REFERENCES status_pages(id) ON DELETE CASCADE,
    -- Bare, lower-cased dot-atom address (shared/mail.ParseRecipient), kept
    -- because mail has to be sent to it.
    email                TEXT NOT NULL CHECK (char_length(email) BETWEEN 3 AND 254),
    -- SHA-256 of the address, hex: the lookup key, and what audit events
    -- record instead of the address.
    email_hash           TEXT NOT NULL,
    state                TEXT NOT NULL DEFAULT 'pending' CHECK (state IN ('pending', 'confirmed')),
    -- The last time the visitor asked to subscribe. A pending row is pruned
    -- 7 days after it.
    requested_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    confirmed_at         TIMESTAMPTZ,
    -- A confirmation mail is owed from this time; NULL when none is. The
    -- runner reserves it (claim columns) and clears it, committed, right
    -- before the send, so a crash never mails it twice.
    confirm_due_at       TIMESTAMPTZ,
    confirm_claim_id     UUID,
    confirm_claimed_at   TIMESTAMPTZ,
    confirm_attempts     INTEGER NOT NULL DEFAULT 0,
    -- Confirmation mails asked for in the day starting confirm_window_start:
    -- at most three per address and page, whatever a requester repeats.
    confirm_window_start TIMESTAMPTZ,
    confirm_window_sends INTEGER NOT NULL DEFAULT 0,
    last_confirm_sent_at TIMESTAMPTZ,
    last_confirm_error   TEXT,
    created_at           TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT status_page_subscribers_page_email UNIQUE (status_page_id, email_hash)
);

CREATE INDEX idx_status_page_subscribers_tenant ON status_page_subscribers (tenant_id);
CREATE INDEX idx_status_page_subscribers_confirmed
    ON status_page_subscribers (status_page_id) WHERE state = 'confirmed';
CREATE INDEX idx_status_page_subscribers_confirm_due
    ON status_page_subscribers (confirm_due_at) WHERE confirm_due_at IS NOT NULL;
CREATE INDEX idx_status_page_subscribers_pending
    ON status_page_subscribers (status_page_id, created_at) WHERE state = 'pending';
