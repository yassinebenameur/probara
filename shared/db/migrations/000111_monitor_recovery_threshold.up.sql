-- Recovery confirmation (docs/state-semantics.md S-T7): a down stream needs
-- recovery_threshold passing checks in a row before it is up again. The
-- default of 1 is single-sample recovery, so every existing monitor keeps
-- today's behaviour. consecutive_successes counts the passes toward that
-- confirmation, per stream: on monitors for location-less monitors (and the
-- display-only aggregate progress of location-bound ones), and per location
-- on monitor_location_state. It is non-zero only while the stream is down.
-- recovery_pending remembers that a stream went silent (or a location was
-- added) while down, so it still owes the confirmation when results resume.
ALTER TABLE monitors
    ADD COLUMN recovery_threshold INTEGER NOT NULL DEFAULT 1
        CONSTRAINT monitors_recovery_threshold_check CHECK (recovery_threshold BETWEEN 1 AND 10),
    ADD COLUMN consecutive_successes INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN recovery_pending BOOLEAN NOT NULL DEFAULT FALSE;

ALTER TABLE monitor_location_state
    ADD COLUMN consecutive_successes INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN recovery_pending BOOLEAN NOT NULL DEFAULT FALSE;
