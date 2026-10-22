package airca

import (
	"context"
	"encoding/json"

	"github.com/yassinebenameur/probara/shared/ai"
)

// Every triage write is guarded by (id, tenant_id, alert_id, status='pending'),
// so redeliveries are idempotent, a forged job cannot touch another tenant's
// row, and a write that lost to another run or to the relay's timeout reports
// false (a lost race, not an error). Only alert_triage is ever written.

// markTriageReady records a completed triage.
func (c *TriageConsumer) markTriageReady(ctx context.Context, job TriageJob, result ai.AnalysisResult) (bool, error) {
	metadata, err := json.Marshal(result.Metadata)
	if err != nil {
		return false, err
	}
	// The exact model exchange, kept for operator labels; NULL when no
	// decision was sent (no affected monitor with evidence).
	var snapshot []byte
	if result.Snapshot != nil {
		if snapshot, err = json.Marshal(result.Snapshot); err != nil {
			return false, err
		}
	}
	res, err := c.db.ExecContext(ctx, `
		UPDATE alert_triage
		SET status = 'ready', hypothesis = $4, confidence = $5, summary = $6,
		    metadata = $7, decision_snapshot = $8, error_message = NULL, completed_at = NOW()
		WHERE id = $1 AND tenant_id = $2 AND alert_id = $3 AND status = 'pending'`,
		job.TriageID, job.TenantID, job.AlertID, nullableText(result.Metadata.Hypothesis),
		nullableText(result.Confidence), nullableText(result.Summary), metadata, nullableJSON(snapshot))
	return rowUpdated(res, err)
}

// markTriageStarted stamps the first delivery that found the row pending.
// Later deliveries keep the first stamp.
func (c *TriageConsumer) markTriageStarted(ctx context.Context, job TriageJob) error {
	_, err := c.db.ExecContext(ctx, `
		UPDATE alert_triage SET started_at = NOW()
		WHERE id = $1 AND tenant_id = $2 AND alert_id = $3 AND status = 'pending' AND started_at IS NULL`,
		job.TriageID, job.TenantID, job.AlertID)
	return err
}

// markTriageFailed records a terminal failure with a fixed operator message.
func (c *TriageConsumer) markTriageFailed(ctx context.Context, job TriageJob, message string) (bool, error) {
	res, err := c.db.ExecContext(ctx, `
		UPDATE alert_triage
		SET status = 'failed', error_message = $4, metadata = $5::jsonb, completed_at = NOW()
		WHERE id = $1 AND tenant_id = $2 AND alert_id = $3 AND status = 'pending'`,
		job.TriageID, job.TenantID, job.AlertID, message, failedAnalysisMetadata)
	return rowUpdated(res, err)
}

// markTriageSkipped records a triage that was deliberately not run.
func (c *TriageConsumer) markTriageSkipped(ctx context.Context, job TriageJob, reason string) (bool, error) {
	res, err := c.db.ExecContext(ctx, `
		UPDATE alert_triage
		SET status = 'skipped', skip_reason = $4, completed_at = NOW()
		WHERE id = $1 AND tenant_id = $2 AND alert_id = $3 AND status = 'pending'`,
		job.TriageID, job.TenantID, job.AlertID, reason)
	return rowUpdated(res, err)
}

// nullableText maps "" to SQL NULL.
func nullableText(s string) any {
	if s == "" {
		return nil
	}
	return s
}
