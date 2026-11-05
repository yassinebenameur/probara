-- Recovery confirmation (rule S-T7, docs/state-semantics.md): a down stream
-- needs recovery_threshold consecutive passing results before it is up.
-- consecutive_successes counts the passes seen so far while still down; it
-- is 0 on every stream that is not mid-recovery. The default of 1 keeps
-- every existing monitor on single-sample recovery.
ALTER TABLE monitors
    ADD COLUMN recovery_threshold INTEGER NOT NULL DEFAULT 1
        CHECK (recovery_threshold BETWEEN 1 AND 10),
    ADD COLUMN consecutive_successes INTEGER NOT NULL DEFAULT 0;

ALTER TABLE monitor_location_state
    ADD COLUMN consecutive_successes INTEGER NOT NULL DEFAULT 0;
