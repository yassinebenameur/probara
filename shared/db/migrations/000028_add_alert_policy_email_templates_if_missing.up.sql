ALTER TABLE alert_policies
    ADD COLUMN IF NOT EXISTS email_subject_template TEXT,
    ADD COLUMN IF NOT EXISTS email_body_template TEXT;
