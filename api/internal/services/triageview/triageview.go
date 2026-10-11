// Package triageview is the one projection of alert_triage onto alert reads.
// The alerts service (list, get, recent, ack/resolve responses) and the
// incident detail's alert summaries append Columns and Join to their alert
// query and scan through Scan, so every surface reports triage identically.
//
// Triage never changes paging: nothing here feeds routing, suppression,
// severity or the page events. The alerter's opt-in suspected-cause
// follow-up (alerter dispatchSuspectedCauses) is the only other reader of
// alert_triage.
package triageview

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/api/internal/models"
	"github.com/yassinebenameur/probara/api/internal/services/ailabels"
	"github.com/yassinebenameur/probara/shared/ai"
)

// Join attaches the alert's triage row. It expects the alerts alias a and
// introduces tr (te is taken by tenants in the alerts projection). The
// tenant predicate keeps a row of another tenant from ever joining.
const Join = ` LEFT JOIN alert_triage tr ON tr.alert_id = a.id AND tr.tenant_id = a.tenant_id `

// Columns are the triage fields Scan reads, in order. labelable needs the
// CASE guard: SQL does not short-circuit AND, and jsonb_array_length raises
// on a non-array, so a malformed snapshot must become NULL before it.
const Columns = `tr.id, tr.status, tr.hypothesis, tr.confidence, tr.summary, tr.skip_reason, tr.error_message,
	tr.created_at, tr.completed_at,
	EXISTS(SELECT 1 FROM ai_labels l WHERE l.tenant_id = tr.tenant_id
		AND l.subject_type = '` + models.AILabelSubjectAlertTriage + `' AND l.subject_key = tr.id::text),
	(tr.status = '` + models.AlertTriageStatusReady + `' AND COALESCE(jsonb_array_length(CASE WHEN jsonb_typeof(tr.decision_snapshot->'decisions') = 'array'
		THEN tr.decision_snapshot->'decisions' END), 0) > 0)`

// Scan holds the nullable targets of one Columns projection.
type Scan struct {
	id           uuid.NullUUID
	status       sql.NullString
	hypothesis   sql.NullString
	confidence   sql.NullString
	summary      sql.NullString
	skipReason   sql.NullString
	errorMessage sql.NullString
	createdAt    sql.NullTime
	completedAt  sql.NullTime
	labelled     sql.NullBool
	labelable    sql.NullBool
}

// Dest returns the scan destinations, in Columns order.
func (s *Scan) Dest() []any {
	return []any{&s.id, &s.status, &s.hypothesis, &s.confidence, &s.summary, &s.skipReason, &s.errorMessage,
		&s.createdAt, &s.completedAt, &s.labelled, &s.labelable}
}

// Build returns the compact triage, or nil when the alert has no triage row.
func (s *Scan) Build() *models.AlertTriage {
	if !s.id.Valid {
		return nil
	}
	t := &models.AlertTriage{
		ID:           s.id.UUID,
		Status:       s.status.String,
		Hypothesis:   nullable(s.hypothesis),
		Confidence:   nullable(s.confidence),
		Summary:      nullable(s.summary),
		SkipReason:   nullable(s.skipReason),
		ErrorMessage: nullable(s.errorMessage),
		CreatedAt:    s.createdAt.Time,
		Labelled:     s.labelled.Bool,
		Labelable:    s.labelable.Bool,
	}
	if s.completedAt.Valid {
		at := s.completedAt.Time
		t.CompletedAt = &at
	}
	if t.Hypothesis != nil {
		title := ai.ChoiceTitle("rca", *t.Hypothesis)
		t.Title = &title
	}
	return t
}

// LoadLabelContext builds the label control context of a tenant's triage
// from its stored decision snapshot. It returns nil when the triage is gone,
// belongs to another tenant, or has no labelable decision.
func LoadLabelContext(ctx context.Context, q ailabels.Querier, tenantID, triageID uuid.UUID) (*models.AILabelContext, error) {
	var snapshot []byte
	err := q.QueryRowContext(ctx,
		`SELECT decision_snapshot FROM alert_triage WHERE id = $1 AND tenant_id = $2`, triageID, tenantID).Scan(&snapshot)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("load triage snapshot: %w", err)
	}
	return ailabels.LoadLabelContext(ctx, q, tenantID, models.AILabelSubjectAlertTriage, triageID, json.RawMessage(snapshot))
}

func nullable(v sql.NullString) *string {
	if !v.Valid {
		return nil
	}
	s := v.String
	return &s
}
