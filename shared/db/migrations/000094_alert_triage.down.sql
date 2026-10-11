ALTER TABLE ai_settings
    DROP COLUMN IF EXISTS triage_max_per_hour,
    DROP COLUMN IF EXISTS triage_cooldown_seconds,
    DROP COLUMN IF EXISTS triage_delay_seconds,
    DROP COLUMN IF EXISTS auto_triage_enabled;
DROP TABLE IF EXISTS alert_triage;
-- ai_labels rows with subject_type 'alert_triage' are kept: 000093 already
-- allows the type and each label carries its own request copy.
