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
    -- SHA-256 of the inbox the address reaches: lower-cased, with any
    -- +tag removed from the local part. Confirmation mail is capped per
    -- inbox, across every page, so aliases cannot flood one mailbox.
    inbox_hash           TEXT NOT NULL,
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
CREATE INDEX idx_status_page_subscribers_created
    ON status_page_subscribers (status_page_id, created_at);
-- The runner's stale-claim hand-back reads only reserved rows.
CREATE INDEX idx_status_page_subscribers_confirm_claimed
    ON status_page_subscribers (confirm_claimed_at) WHERE confirm_claim_id IS NOT NULL;

-- Confirmation mails as sent, installation-wide: the per-inbox daily cap
-- and the hourly cap on all confirmation mail read it. Only the inbox hash
-- is kept, never an address; rows older than two days are pruned. It has no
-- tenant: a cap that a tenant purge resets would not hold.
CREATE TABLE status_page_confirmation_sends (
    id         BIGSERIAL PRIMARY KEY,
    inbox_hash TEXT NOT NULL,
    sent_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_status_page_confirmation_sends_inbox
    ON status_page_confirmation_sends (inbox_hash, sent_at);
CREATE INDEX idx_status_page_confirmation_sends_time
    ON status_page_confirmation_sends (sent_at);
