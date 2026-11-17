-- Automatic publication of monitor outages to status pages (the page's
-- auto_publish_incidents setting) and visitor push for incident events.

-- A publication made by the API runner rather than an operator. Such an
-- incident is resolved automatically once its monitors recover.
ALTER TABLE incident_status_page_publications
    ADD COLUMN auto_published BOOLEAN NOT NULL DEFAULT FALSE;

-- Visitor push for incident publications, public updates and resolutions,
-- driven by the notification outbox (000122). Such a delivery names the
-- incident instead of a monitor, and its interval_id holds the outbox row's
-- ID: the existing (status_page_id, interval_id) unique index then keeps it
-- exactly once per page and event, like a state transition.
ALTER TABLE status_page_push_deliveries
    ALTER COLUMN monitor_id DROP NOT NULL,
    ADD COLUMN incident_id UUID REFERENCES incidents(id) ON DELETE CASCADE,
    DROP CONSTRAINT status_page_push_deliveries_kind_check,
    ADD CONSTRAINT status_page_push_deliveries_kind_check CHECK (kind IN (
        'down', 'recovered', 'incident_published', 'incident_updated', 'incident_resolved')),
    ADD CONSTRAINT status_page_push_deliveries_subject CHECK (
        (kind IN ('down', 'recovered') AND monitor_id IS NOT NULL AND incident_id IS NULL)
        OR (kind LIKE 'incident\_%' AND incident_id IS NOT NULL AND monitor_id IS NULL));
