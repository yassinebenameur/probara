-- Recurring maintenance windows (docs/state-semantics.md S-M5). A window is
-- a rule; what covers monitors is its materialized occurrences. starts_at /
-- ends_at stay the template occurrence: its start anchors the rule and
-- ends_at - starts_at is every occurrence's duration.
ALTER TABLE maintenance_windows
    ADD COLUMN recurrence       TEXT        NOT NULL DEFAULT 'none',
    ADD COLUMN interval_n       INT         NOT NULL DEFAULT 1,
    ADD COLUMN by_weekday       INT[]       NOT NULL DEFAULT '{}',
    ADD COLUMN by_monthday      INT,
    ADD COLUMN cron_expr        TEXT,
    ADD COLUMN timezone         TEXT        NOT NULL DEFAULT 'UTC',
    ADD COLUMN recurrence_until TIMESTAMPTZ,
    ADD CONSTRAINT maintenance_windows_recurrence_check
        CHECK (recurrence IN ('none', 'daily', 'weekly', 'monthly', 'cron')),
    ADD CONSTRAINT maintenance_windows_interval_check CHECK (interval_n >= 1),
    ADD CONSTRAINT maintenance_windows_monthday_check
        CHECK (by_monthday IS NULL OR by_monthday BETWEEN 1 AND 31),
    ADD CONSTRAINT maintenance_windows_cron_check
        CHECK ((recurrence = 'cron') = (cron_expr IS NOT NULL));

-- Occurrences are history once they end: SLA reports and the interval
-- engine read them back as far as a period reaches, and editing a rule only
-- replaces the ones that have not started (shared/maintenance/store.go).
CREATE TABLE maintenance_window_occurrences (
    window_id UUID        NOT NULL REFERENCES maintenance_windows(id) ON DELETE CASCADE,
    tenant_id UUID        NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    starts_at TIMESTAMPTZ NOT NULL,
    ends_at   TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (window_id, starts_at),
    CONSTRAINT maintenance_window_occurrences_time_order CHECK (ends_at > starts_at)
);

CREATE INDEX idx_maintenance_window_occurrences_tenant_time
    ON maintenance_window_occurrences (tenant_id, starts_at, ends_at);
CREATE INDEX idx_maintenance_window_occurrences_window_end
    ON maintenance_window_occurrences (window_id, ends_at);

-- Every existing window is a one-off: exactly one occurrence.
INSERT INTO maintenance_window_occurrences (window_id, tenant_id, starts_at, ends_at)
SELECT id, tenant_id, starts_at, ends_at FROM maintenance_windows;

-- Targets follow the same edit rule (S-M5, shared/maintenance Retarget): a
-- link covers occurrences only from linked_at on ('-infinity' for links made
-- with the window), and a removed link moves here with the span it was in
-- force, so changing a window's monitors never rewrites what an ended
-- occurrence covered.
ALTER TABLE maintenance_window_monitors
    ADD COLUMN linked_at TIMESTAMPTZ NOT NULL DEFAULT '-infinity';

CREATE TABLE maintenance_window_past_targets (
    maintenance_window_id UUID        NOT NULL REFERENCES maintenance_windows(id) ON DELETE CASCADE,
    monitor_id            UUID        NOT NULL REFERENCES monitors(id) ON DELETE CASCADE,
    linked_at             TIMESTAMPTZ NOT NULL,
    unlinked_at           TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (maintenance_window_id, monitor_id, unlinked_at),
    CONSTRAINT maintenance_window_past_targets_span CHECK (unlinked_at > linked_at)
);

CREATE INDEX idx_maintenance_window_past_targets_monitor
    ON maintenance_window_past_targets (monitor_id);
