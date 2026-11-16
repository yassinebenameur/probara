-- Emailed SLA reports (api/internal/services/reportschedules). One schedule
-- per SLA. When its period closes the API's runner claims the schedule
-- (FOR UPDATE SKIP LOCKED), issues the report and commits one delivery row
-- per recipient before mailing anyone; each delivery is then claimed
-- ('sending') and committed before its message goes out. A crash or a second
-- replica therefore never mails one period twice to one address: a delivery
-- left 'sending' is closed as 'uncertain', never sent again.
CREATE TABLE sla_report_schedules (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id       UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    sla_id          UUID NOT NULL UNIQUE REFERENCES slas(id) ON DELETE CASCADE,
    enabled         BOOLEAN NOT NULL DEFAULT TRUE,
    -- Bare, lower-cased addresses; one message per recipient.
    recipients      TEXT[] NOT NULL CHECK (cardinality(recipients) BETWEEN 1 AND 50),
    -- The PDF is always attached; the CSV on request.
    include_csv     BOOLEAN NOT NULL DEFAULT FALSE,
    -- The running period's close plus the runner's grace; re-planned on
    -- (re-)enable, after every run and when the SLA's period or timezone
    -- changes.
    next_run_at     TIMESTAMPTZ NOT NULL,
    last_period_key TEXT,
    last_run_at     TIMESTAMPTZ,
    last_error      TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_sla_report_schedules_due ON sla_report_schedules (next_run_at) WHERE enabled;
CREATE INDEX idx_sla_report_schedules_tenant ON sla_report_schedules (tenant_id);

-- One recipient's copy of one period's report. address_key is a truncated
-- SHA-256 of the schedule ID and the address, the same key the unsubscribe
-- link carries. Rows of finished periods are pruned when the next period is
-- claimed, so the table holds at most two periods per schedule.
CREATE TABLE sla_report_deliveries (
    schedule_id     UUID NOT NULL REFERENCES sla_report_schedules(id) ON DELETE CASCADE,
    tenant_id       UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    period_key      TEXT NOT NULL,
    address_key     TEXT NOT NULL,
    address         TEXT NOT NULL,
    status          TEXT NOT NULL DEFAULT 'pending'
                    CHECK (status IN ('pending', 'sending', 'sent', 'failed', 'uncertain', 'skipped', 'cancelled')),
    attempts        INTEGER NOT NULL DEFAULT 0,
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    claimed_at      TIMESTAMPTZ,
    sent_at         TIMESTAMPTZ,
    last_error      TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (schedule_id, period_key, address_key)
);

CREATE INDEX idx_sla_report_deliveries_due ON sla_report_deliveries (next_attempt_at) WHERE status = 'pending';
CREATE INDEX idx_sla_report_deliveries_sending ON sla_report_deliveries (claimed_at) WHERE status = 'sending';

-- Recipients who followed their unsubscribe link, by address key, so the
-- opt-out survives edits of the recipient list without keeping the address.
-- Re-adding the address to the schedule deletes the row.
CREATE TABLE sla_report_unsubscribes (
    schedule_id     UUID NOT NULL REFERENCES sla_report_schedules(id) ON DELETE CASCADE,
    address_key     TEXT NOT NULL,
    unsubscribed_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (schedule_id, address_key)
);
