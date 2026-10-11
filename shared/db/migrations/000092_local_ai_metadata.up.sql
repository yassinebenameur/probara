-- Keep all historical text and obsolete provider settings for rollback.
ALTER TABLE incident_ai_analyses ADD COLUMN metadata JSONB;
UPDATE incident_ai_analyses SET metadata='{"engine":"legacy-llm","outcome":"legacy","evidence_references":[],"incomplete_evidence":[]}'::jsonb;
-- Workspace enable switches are preserved. Absent settings default enabled in
-- shared/ai; newly created rows use the same default at the database boundary.
ALTER TABLE ai_settings ALTER COLUMN enabled SET DEFAULT TRUE;
