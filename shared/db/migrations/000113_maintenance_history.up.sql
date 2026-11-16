-- Maintenance history survives edits and deletes (docs/state-semantics.md
-- S-M5).
--
-- Targets follow the same rule as schedules: a link covers occurrences only
-- from linked_at on ('-infinity' for links made with the window), and a
-- removed link moves to maintenance_window_past_targets with the span it was
-- in force, so changing a window's monitors never rewrites what an earlier
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

-- Deleting a window that has already covered time keeps the row (and its
-- targets and started occurrences) as history: deleted_at hides it from the
-- API and stops the horizon roller. A window that never started is removed.
ALTER TABLE maintenance_windows
    ADD COLUMN deleted_at TIMESTAMPTZ;
