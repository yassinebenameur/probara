package airca

import (
	"context"
	"encoding/json"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/shared/ai"
)

// Local mirror of the incident_ai_analyses.status values. Defined here (rather
// than imported from api/internal/models, which the worker may not import) to
// keep the worker self-contained.
type analysisStatus string

const (
	pendingStatus analysisStatus = "pending"
	readyStatus   analysisStatus = "ready"
	failedStatus  analysisStatus = "failed"
)

func (c *Consumer) loadStatus(ctx context.Context, id uuid.UUID) (string, error) {
	var status string
	err := c.db.QueryRowContext(ctx, `
		SELECT status FROM incident_ai_analyses WHERE id = $1`, id).Scan(&status)
	return status, err
}

// markReady transitions a pending row to ready with the analysis result. The
// status='pending' guard keeps the write idempotent against redelivery.
func (c *Consumer) markReady(ctx context.Context, id uuid.UUID, result ai.AnalysisResult) error {
	factors, err := json.Marshal(emptyIfNil(result.ContributingFactors))
	if err != nil {
		return err
	}
	actions, err := json.Marshal(emptyIfNil(result.RecommendedActions))
	if err != nil {
		return err
	}
	var evidence []byte
	if len(result.Evidence) > 0 {
		evidence = result.Evidence
	}

	_, err = c.db.ExecContext(ctx, `
		UPDATE incident_ai_analyses
		SET status = $2,
		    model = $3,
		    summary = $4,
		    probable_root_cause = $5,
		    contributing_factors = $6,
		    recommended_actions = $7,
		    confidence = $8,
		    evidence = $9,
		    error_message = NULL,
		    completed_at = NOW()
		WHERE id = $1 AND status = $10`,
		id, string(readyStatus), result.Model, result.Summary, result.ProbableRootCause,
		factors, actions, result.Confidence, nullableJSON(evidence), string(pendingStatus))
	return err
}

func (c *Consumer) markFailed(ctx context.Context, id uuid.UUID, message string) error {
	_, err := c.db.ExecContext(ctx, `
		UPDATE incident_ai_analyses
		SET status = $2, error_message = $3, completed_at = NOW()
		WHERE id = $1 AND status = $4`,
		id, string(failedStatus), message, string(pendingStatus))
	return err
}

func emptyIfNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// nullableJSON returns nil (SQL NULL) for empty JSON so the column stays NULL
// rather than storing an empty value.
func nullableJSON(b []byte) interface{} {
	if len(b) == 0 {
		return nil
	}
	return []byte(b)
}
