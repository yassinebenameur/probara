-- Keep all historical text and obsolete provider settings for rollback.
ALTER TABLE incident_ai_analyses ADD COLUMN metadata JSONB;
UPDATE incident_ai_analyses SET metadata='{"engine":"legacy-llm","outcome":"legacy","evidence_references":[],"incomplete_evidence":[]}'::jsonb;
-- Workspace enable switches are preserved. Absent settings default enabled in
-- shared/ai; newly created rows use the same default at the database boundary.
ALTER TABLE ai_settings ALTER COLUMN enabled SET DEFAULT TRUE;

-- Worker stale sweep (worker/internal/airca/sweep.go, sweepStaleSQL): pending
-- rows only, so the per-minute sweep costs O(pending) instead of scanning the
-- never-pruned analysis history.
CREATE INDEX idx_incident_ai_analyses_pending ON incident_ai_analyses (created_at) WHERE status = 'pending';
