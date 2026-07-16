-- Roll back Incident MVP schema.

DROP INDEX IF EXISTS idx_alert_policies_create_incident_on_fire;
ALTER TABLE alert_policies
    DROP COLUMN IF EXISTS create_incident_on_fire;

DROP TABLE IF EXISTS incident_status_page_monitors;
DROP TABLE IF EXISTS incident_status_page_publications;
DROP TABLE IF EXISTS incident_timeline_entries;
DROP TABLE IF EXISTS incident_monitors;
DROP TABLE IF EXISTS incident_alerts;
DROP TABLE IF EXISTS incidents;

DROP TYPE IF EXISTS incident_timeline_entry_type;
DROP TYPE IF EXISTS incident_state;

