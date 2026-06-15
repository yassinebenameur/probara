package ai

import "github.com/google/uuid"

// AnalysisJobVersion is the current AnalysisJob schema version.
const AnalysisJobVersion = 1

// AnalysisJob is the NATS payload that asks the worker to run an analysis for
// an already-created (pending) incident_ai_analyses row. It lives in shared/ai
// because both the API (publisher) and the worker (consumer) depend on it.
type AnalysisJob struct {
	V          int       `json:"v"`
	AnalysisID uuid.UUID `json:"analysis_id"`
	IncidentID uuid.UUID `json:"incident_id"`
	TenantID   uuid.UUID `json:"tenant_id"`
}
