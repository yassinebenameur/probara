-- Emailed SLA reports (api/internal/services/reportschedules). One schedule
-- per SLA. When a period closes the API's runner issues its report and then
-- claims it (one transaction, the schedule row locked, lock_timeout set),
-- committing one delivery row per recipient before mailing anyone. A
-- delivery is reserved ('claimed', safe to hand back) and becomes 'sending'
-- only immediately before its message goes out, so 'uncertain' (may have
-- arrived, never resent) only ever follows a send that started: a crash or
-- a second replica never mails one period twice to one address.
CREATE TABLE sla_report_schedules (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id       UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    sla_id          UUID NOT NULL UNIQUE REFERENCES slas(id) ON DELETE CASCADE,
    enabled         BOOLEAN NOT NULL DEFAULT TRUE,
    -- Bare, lower-cased addresses; one message per recipient.
    recipients      TEXT[] NOT NULL CHECK (cardinality(recipients) BETWEEN 1 AND 50),
    -- The PDF is always attached; the CSV on request.
    include_csv     BOOLEAN NOT NULL DEFAULT FALSE,
    -- Periods that ended at or before this are never sent: set on create,
    -- on re-enable and when the SLA's period or timezone changes.
    sends_after     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    -- When the runner next looks at the schedule: the running period's
    -- close plus the grace hour, or sooner while closed periods wait.
    next_run_at     TIMESTAMPTZ NOT NULL,
    last_period_key   TEXT,
    last_period_start TIMESTAMPTZ,
    last_period_end   TIMESTAMPTZ,
    last_run_at     TIMESTAMPTZ,
    -- Why the last period could not be issued; delivery problems are
    -- summarized from sla_report_deliveries when read.
    last_error      TEXT,
    -- A period of the SLA's previous definition that closed within the
    -- grace hour before its period or timezone changed: still sent once.
    carry_period_kind TEXT,
    carry_timezone    TEXT,
    carry_period_key  TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_sla_report_schedules_due ON sla_report_schedules (next_run_at) WHERE enabled;
CREATE INDEX idx_sla_report_schedules_tenant ON sla_report_schedules (tenant_id);

-- One recipient's copy of one period's report, identified by the period's
-- start (two definitions can share a key). address_key is a truncated
-- SHA-256 of the schedule ID and the address, the same key the unsubscribe
-- link carries. Finished rows of other periods are pruned when a period is
-- claimed.
CREATE TABLE sla_report_deliveries (
    schedule_id     UUID NOT NULL REFERENCES sla_report_schedules(id) ON DELETE CASCADE,
    tenant_id       UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    period_start    TIMESTAMPTZ NOT NULL,
    period_key      TEXT NOT NULL,
    address_key     TEXT NOT NULL,
    address         TEXT NOT NULL,
    status          TEXT NOT NULL DEFAULT 'pending'
                    CHECK (status IN ('pending', 'claimed', 'sending', 'sent', 'failed', 'uncertain', 'skipped', 'cancelled')),
    -- Started sends; a claim handed back unsent does not count.
    attempts        INTEGER NOT NULL DEFAULT 0,
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    -- The runner batch that holds the row while claimed or sending.
    claim_id        UUID,
    claimed_at      TIMESTAMPTZ,
    sent_at         TIMESTAMPTZ,
    last_error      TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (schedule_id, period_start, address_key)
);

CREATE INDEX idx_sla_report_deliveries_due ON sla_report_deliveries (next_attempt_at) WHERE status = 'pending';
CREATE INDEX idx_sla_report_deliveries_held ON sla_report_deliveries (claimed_at) WHERE status IN ('claimed', 'sending');

-- Recipients who followed their unsubscribe link, by address key, so the
-- opt-out survives edits of the recipient list without keeping the address.
-- Re-adding the address to the schedule deletes the row.
CREATE TABLE sla_report_unsubscribes (
    schedule_id     UUID NOT NULL REFERENCES sla_report_schedules(id) ON DELETE CASCADE,
    address_key     TEXT NOT NULL,
    unsubscribed_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (schedule_id, address_key)
);
