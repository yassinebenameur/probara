-- Incident push deliveries have no monitor; they cannot survive the
-- restored NOT NULL. They are a short-lived ledger (7 days), so dropping
-- them loses nothing a visitor still waits for.
DELETE FROM status_page_push_deliveries WHERE incident_id IS NOT NULL;

ALTER TABLE status_page_push_deliveries
    DROP CONSTRAINT status_page_push_deliveries_subject,
    DROP CONSTRAINT status_page_push_deliveries_kind_check,
    ADD CONSTRAINT status_page_push_deliveries_kind_check CHECK (kind IN ('down', 'recovered')),
    DROP COLUMN incident_id,
    ALTER COLUMN monitor_id SET NOT NULL;

ALTER TABLE incident_status_page_publications
    DROP COLUMN public_summary,
    DROP COLUMN public_title,
    DROP COLUMN auto_published;
