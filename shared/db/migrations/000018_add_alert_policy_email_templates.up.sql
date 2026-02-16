ALTER TABLE alert_policies
    ADD COLUMN email_subject_template TEXT,
    ADD COLUMN email_body_template TEXT;
