package incidents

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/api/internal/models"
)

// RequestAIAnalysis verifies the incident belongs to the tenant and inserts a
// pending incident_ai_analyses row. The caller is responsible for enqueuing the
// worker job; this only reserves the row the worker will fill in.
func (s *Service) RequestAIAnalysis(ctx context.Context, tenantID, incidentID uuid.UUID, requestedBy *uuid.UUID) (*models.IncidentAIAnalysis, error) {
	var exists bool
	if err := s.db.QueryRowContext(ctx,
		`SELECT EXISTS(SELECT 1 FROM incidents WHERE id = $1 AND tenant_id = $2)`,
		incidentID, tenantID).Scan(&exists); err != nil {
		return nil, fmt.Errorf("check incident exists: %w", err)
	}
	if !exists {
		return nil, fmt.Errorf("incident not found")
	}

	id := uuid.New()
	var createdAt = sql.NullTime{}
	if err := s.db.QueryRowContext(ctx, `
		INSERT INTO incident_ai_analyses (id, tenant_id, incident_id, status, requested_by)
		VALUES ($1, $2, $3, 'pending', $4)
		RETURNING created_at`,
		id, tenantID, incidentID, requestedBy).Scan(&createdAt.Time); err != nil {
		return nil, fmt.Errorf("insert ai analysis: %w", err)
	}

	return &models.IncidentAIAnalysis{
		ID:          id,
		TenantID:    tenantID,
		IncidentID:  incidentID,
		Status:      models.IncidentAIAnalysisStatusPending,
		RequestedBy: requestedBy,
		CreatedAt:   createdAt.Time,
	}, nil
}

// aiAnalysisFailedMetadata mirrors the metadata the worker writes on a failed
// run (worker/internal/airca/store.go) so the panel renders both the same way.
const aiAnalysisFailedMetadata = `{"engine":"laya-cpu","outcome":"failed","incomplete_evidence":[],"evidence_references":[]}`

// FailPendingAIAnalysis marks a still-pending analysis row failed with a
// user-facing message. It is guarded by status='pending' so it can never
// overwrite a result the worker already wrote, and it reports no error when
// the row has moved on (or belongs to another tenant) — there is nothing left
// to fail in either case.
func (s *Service) FailPendingAIAnalysis(ctx context.Context, tenantID, analysisID uuid.UUID, message string) error {
	if _, err := s.db.ExecContext(ctx, `
		UPDATE incident_ai_analyses
		SET status = 'failed', error_message = $3, completed_at = NOW(),
		    metadata = $4::jsonb,
		    recommended_actions = '["Retry the analysis."]'::jsonb
		WHERE id = $1 AND tenant_id = $2 AND status = 'pending'`,
		analysisID, tenantID, message, aiAnalysisFailedMetadata); err != nil {
		return fmt.Errorf("fail pending ai analysis: %w", err)
	}
	return nil
}

// GetLatestAIAnalysis returns the most recent analysis for an incident, or nil
// when none has been requested. The incident must belong to the tenant.
func (s *Service) GetLatestAIAnalysis(ctx context.Context, tenantID, incidentID uuid.UUID) (*models.IncidentAIAnalysis, error) {
	var exists bool
	if err := s.db.QueryRowContext(ctx,
		`SELECT EXISTS(SELECT 1 FROM incidents WHERE id = $1 AND tenant_id = $2)`,
		incidentID, tenantID).Scan(&exists); err != nil {
		return nil, fmt.Errorf("check incident exists: %w", err)
	}
	if !exists {
		return nil, fmt.Errorf("incident not found")
	}
	return s.loadLatestAIAnalysis(ctx, tenantID, incidentID)
}

// loadLatestAIAnalysis fetches the newest analysis row for an incident (no
// existence check — used internally by GetIncident). Returns nil when absent.
func (s *Service) loadLatestAIAnalysis(ctx context.Context, tenantID, incidentID uuid.UUID) (*models.IncidentAIAnalysis, error) {
	// Read-only on purpose: GET /incidents/{id} and the panel's poll both land
	// here, so a write would turn every read into an UPDATE. Pending rows whose
	// delivery was exhausted are failed by the worker-side sweep instead.
	row := s.db.QueryRowContext(ctx, `
		SELECT id, tenant_id, incident_id, status, model, summary, probable_root_cause,
		       contributing_factors, recommended_actions, confidence, evidence,
		       error_message, requested_by, created_at, completed_at, metadata
		FROM incident_ai_analyses
		WHERE incident_id = $1 AND tenant_id = $2
		ORDER BY created_at DESC
		LIMIT 1`, incidentID, tenantID)

	analysis, err := scanAIAnalysis(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("load ai analysis: %w", err)
	}
	return analysis, nil
}

type rowScanner interface {
	Scan(dest ...interface{}) error
}

func scanAIAnalysis(row rowScanner) (*models.IncidentAIAnalysis, error) {
	var (
		a                   models.IncidentAIAnalysis
		status              string
		model               sql.NullString
		summary             sql.NullString
		probableRootCause   sql.NullString
		contributingFactors []byte
		recommendedActions  []byte
		confidence          sql.NullString
		evidence            []byte
		errorMessage        sql.NullString
		requestedBy         uuid.NullUUID
		completedAt         sql.NullTime
		metadata            []byte
	)
	if err := row.Scan(
		&a.ID, &a.TenantID, &a.IncidentID, &status, &model, &summary, &probableRootCause,
		&contributingFactors, &recommendedActions, &confidence, &evidence,
		&errorMessage, &requestedBy, &a.CreatedAt, &completedAt, &metadata,
	); err != nil {
		return nil, err
	}

	a.Metadata = metadata
	a.Status = models.IncidentAIAnalysisStatus(status)
	a.Model = model.String
	a.Summary = summary.String
	a.ProbableRootCause = probableRootCause.String
	a.Confidence = confidence.String
	a.ErrorMessage = errorMessage.String
	if len(contributingFactors) > 0 {
		_ = json.Unmarshal(contributingFactors, &a.ContributingFactors)
	}
	if len(recommendedActions) > 0 {
		_ = json.Unmarshal(recommendedActions, &a.RecommendedActions)
	}
	if len(evidence) > 0 {
		a.Evidence = json.RawMessage(evidence)
	}
	if requestedBy.Valid {
		id := requestedBy.UUID
		a.RequestedBy = &id
	}
	if completedAt.Valid {
		t := completedAt.Time
		a.CompletedAt = &t
	}
	return &a, nil
}
