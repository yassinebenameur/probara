ALTER TABLE ai_settings ALTER COLUMN enabled SET DEFAULT FALSE;
ALTER TABLE incident_ai_analyses DROP COLUMN metadata;
