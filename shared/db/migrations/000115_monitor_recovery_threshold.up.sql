-- Recovery confirmation (docs/state-semantics.md S-T7): a down stream needs
-- recovery_threshold passing checks in a row before it is up again. The
-- default of 1 is single-sample recovery, so every existing monitor keeps
-- today's behaviour. consecutive_successes counts the passes toward that
-- confirmation, per stream: on monitors for location-less monitors (and the
-- display-only aggregate progress of location-bound ones), and per location
-- on monitor_location_state. It is non-zero only while the stream is down.
ALTER TABLE monitors
    ADD COLUMN recovery_threshold INTEGER NOT NULL DEFAULT 1
        CONSTRAINT monitors_recovery_threshold_check CHECK (recovery_threshold BETWEEN 1 AND 10),
    ADD COLUMN consecutive_successes INTEGER NOT NULL DEFAULT 0;

ALTER TABLE monitor_location_state
    ADD COLUMN consecutive_successes INTEGER NOT NULL DEFAULT 0;
