ALTER TABLE alert_policies
    DROP COLUMN IF EXISTS email_subject_template,
    DROP COLUMN IF EXISTS email_body_template;
