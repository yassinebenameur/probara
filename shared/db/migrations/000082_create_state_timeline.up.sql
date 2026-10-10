-- 000082_create_state_timeline.up.sql
-- State-interval timeline (docs/state-semantics.md, S-U4): one row per
-- contiguous monitor-level observed-state interval, written in the same
-- transaction as the transition it records. This is the substrate for
-- time-based availability (S-U1/S-U2); until that lands the analytics layer
-- still counts samples, and windows predating the seed use sampled math
-- (S-U5).
CREATE TABLE monitor_state_intervals (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id  UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    monitor_id UUID NOT NULL REFERENCES monitors(id) ON DELETE CASCADE,
    state      TEXT NOT NULL CHECK (state IN ('unknown', 'up', 'suspect', 'down', 'degraded')),
    -- What opened this interval: a check result, the absence watchdog
    -- (Stage 5), an enabled toggle, a location-set change, monitor creation,
    -- or the one-time seed below.
    reason     TEXT NOT NULL CHECK (reason IN ('result', 'watchdog_stale', 'pause', 'resume', 'location_change', 'created', 'seed')),
    started_at TIMESTAMPTZ NOT NULL,
    ended_at   TIMESTAMPTZ, -- NULL = the monitor's current interval
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CHECK (ended_at IS NULL OR ended_at >= started_at)
);

-- Exactly one open interval per monitor. Writers hold the monitor row lock
-- first (S-O1), so close-then-open can never race itself.
CREATE UNIQUE INDEX idx_monitor_state_intervals_open
    ON monitor_state_intervals(monitor_id) WHERE ended_at IS NULL;
CREATE INDEX idx_monitor_state_intervals_window
    ON monitor_state_intervals(monitor_id, started_at);
CREATE INDEX idx_monitor_state_intervals_tenant
    ON monitor_state_intervals(tenant_id);

-- Seed one open interval per live monitor from its current state, backdated
-- to the last recorded transition (truthful for the state it is in now).
-- Monitors already paused keep their frozen state here until the next
-- enabled toggle resets them — S-P2 applies from now on, not retroactively.
-- Group monitors are excluded: their state is derived by the alerter's SQL
-- roll-up, which records no transitions; they get a timeline when that
-- derivation is unified into the state machine.
INSERT INTO monitor_state_intervals (tenant_id, monitor_id, state, reason, started_at)
SELECT m.tenant_id, m.id, m.current_state, 'seed',
       COALESCE(m.last_state_change_at, m.created_at, NOW())
FROM monitors m
WHERE m.deleted_at IS NULL
  AND m.type <> 'group';

-- Ingest receipt time (server clock). check_results.created_at is the
-- WORKER's clock (bound to StartedAt at insert), never receipt time — the
-- root cause of the rollup-cursor skip. NULL = row predates this column.
-- Added WITHOUT a default first: ADD COLUMN with a default would fast-default
-- every existing row to the migration timestamp — a plausible-but-wrong
-- receipt time. NULL is the honest value for pre-migration rows.
ALTER TABLE check_results ADD COLUMN ingested_at TIMESTAMPTZ;
ALTER TABLE check_results ALTER COLUMN ingested_at SET DEFAULT NOW();

-- Evidence bookkeeping.
--   last_result_at         : receipt time (server clock) of the newest APPLIED
--                            monitor-source evidence — the S-F1 freshness
--                            anchor for the Stage 5 watchdog.
--   last_result_started_at : evidence time (worker clock) of the newest
--                            applied evidence on the GLOBAL stream — the S-O2
--                            late-result guard. Location streams keep their
--                            own guard on monitor_location_state.
ALTER TABLE monitors
    ADD COLUMN last_result_at TIMESTAMPTZ,
    ADD COLUMN last_result_started_at TIMESTAMPTZ;
ALTER TABLE monitor_location_state
    ADD COLUMN last_result_started_at TIMESTAMPTZ;

-- Best-effort backfill from the newest monitor-source row. created_at is the
-- worker's check-start clock, which seeds both watermarks closely enough.
UPDATE monitors m
SET last_result_at         = l.latest,
    last_result_started_at = l.latest
FROM (
    SELECT monitor_id, MAX(created_at) AS latest
    FROM check_results
    WHERE result_source = 'monitor'
    GROUP BY monitor_id
) l
WHERE l.monitor_id = m.id;
