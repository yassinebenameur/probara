-- Recurring maintenance windows (docs/state-semantics.md S-M5). A window is
-- either a one-off (starts_at..ends_at, every recurrence_* column NULL) or a
-- weekly rule: on the listed weekdays (0 = Sunday) at recurrence_start_time
-- local time in recurrence_timezone, for recurrence_duration_minutes, with no
-- occurrence on a local date after recurrence_until. For a repeating window
-- starts_at/ends_at hold the first occurrence of its current schedule; what
-- covers monitors is always the materialized occurrences below, never these
-- columns.
ALTER TABLE maintenance_windows
    ADD COLUMN recurrence_weekdays         SMALLINT[],
    ADD COLUMN recurrence_start_time       TIME,
    ADD COLUMN recurrence_duration_minutes INT,
    ADD COLUMN recurrence_timezone         TEXT,
    ADD COLUMN recurrence_until            DATE,
    ADD CONSTRAINT maintenance_windows_recurrence_check CHECK (
        (recurrence_weekdays IS NULL
            AND recurrence_start_time IS NULL
            AND recurrence_duration_minutes IS NULL
            AND recurrence_timezone IS NULL
            AND recurrence_until IS NULL)
        OR (cardinality(recurrence_weekdays) BETWEEN 1 AND 7
            AND recurrence_weekdays <@ '{0,1,2,3,4,5,6}'::SMALLINT[]
            AND recurrence_start_time IS NOT NULL
            AND recurrence_duration_minutes > 0
            AND recurrence_timezone IS NOT NULL));

-- Occurrences are history once they start: the interval engine, SLA reports
-- and the alert predicates read them back as far as a query reaches, and an
-- edit only replaces the ones that have not started (shared/maintenance).
-- A skipped occurrence stays as a row so the horizon roller never
-- re-creates it; coverage ignores it.
CREATE TABLE maintenance_window_occurrences (
    window_id UUID        NOT NULL REFERENCES maintenance_windows(id) ON DELETE CASCADE,
    tenant_id UUID        NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    starts_at TIMESTAMPTZ NOT NULL,
    ends_at   TIMESTAMPTZ NOT NULL,
    skipped   BOOLEAN     NOT NULL DEFAULT false,
    PRIMARY KEY (window_id, starts_at),
    CONSTRAINT maintenance_window_occurrences_time_order CHECK (ends_at > starts_at)
);

CREATE INDEX idx_maintenance_window_occurrences_window_end
    ON maintenance_window_occurrences (window_id, ends_at);
CREATE INDEX idx_maintenance_window_occurrences_tenant_time
    ON maintenance_window_occurrences (tenant_id, starts_at, ends_at);

-- Every existing window is a one-off: exactly one occurrence, its own span.
INSERT INTO maintenance_window_occurrences (window_id, tenant_id, starts_at, ends_at)
SELECT id, tenant_id, starts_at, ends_at FROM maintenance_windows;
