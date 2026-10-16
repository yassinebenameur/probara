// Package triageview is the one projection of alert_triage onto alert reads.
// The alerts service (list, get, recent, ack/resolve responses) and the
// incident detail's alert summaries append Columns and Join to their alert
// query and scan through Scan, so every surface reports triage identically.
//
// SLA reports read triage only through CauseJoin, CauseColumns,
// ClaimableExpr and EffectiveCause, which own the label-over-hypothesis rule
// for the suspected-cause breakdown.
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

// CauseJoin attaches the alert's triage and the operator's label on it
// (at most one: ai_labels_subject_unique). Same aliases as Join, plus trl.
const CauseJoin = Join + `LEFT JOIN ai_labels trl ON trl.tenant_id = tr.tenant_id
	AND trl.subject_type = '` + models.AILabelSubjectAlertTriage + `' AND trl.subject_key = tr.id::text `

// CauseColumns are the CauseInput fields, in order: triage status and
// hypothesis, label verdict and label key. Append ClaimableExpr for
// CauseInput.Claimable.
const CauseColumns = `tr.status, tr.hypothesis, trl.verdict, trl.incident_label`

// ClaimableExpr is true when the alert has no triage row yet but the relay
// may still claim it: open, triggered at or after lookbackParam (now -
// ai.TriageLookback), its monitor (if any) not deleted, and the workspace has
// AI and auto-triage on. It is the API twin of the eligibility clause of
// worker/internal/airca relayClaimSQL; change them together. The hourly cap,
// the platform backlog bound and the settle wait only delay a claim, so they
// are left out. The installation kill switches (AI_TRIAGE_ENABLED,
// LOCAL_AI_DISABLED) are not visible here, so under them an open alert still
// reads as claimable.
func ClaimableExpr(lookbackParam string) string {
	return `(tr.id IS NULL AND a.resolved_at IS NULL AND a.status IN ('active','acknowledged')
		AND a.triggered_at >= ` + lookbackParam + `
		AND (a.monitor_id IS NULL OR EXISTS (SELECT 1 FROM monitors clm WHERE clm.id = a.monitor_id
			AND clm.tenant_id = a.tenant_id AND clm.deleted_at IS NULL))
		AND EXISTS (SELECT 1 FROM ai_settings ais WHERE ais.tenant_id = a.tenant_id
			AND ais.enabled AND ais.auto_triage_enabled))`
}

// CauseInput is one alert's CauseColumns + ClaimableExpr scan.
type CauseInput struct {
	Status       sql.NullString
	Hypothesis   sql.NullString
	LabelVerdict sql.NullString
	LabelKey     sql.NullString
	Claimable    bool
}

// Dest returns the scan destinations, in CauseColumns + ClaimableExpr order.
func (in *CauseInput) Dest() []any {
	return []any{&in.Status, &in.Hypothesis, &in.LabelVerdict, &in.LabelKey, &in.Claimable}
}

// Cause is an alert's effective suspected cause.
type Cause struct {
	// Kind is one of models.SLACauseSuspected, SLACauseInconclusive,
	// SLACausePending or SLACauseUntriaged.
	Kind string
	// Key is the catalogue rca key for suspected causes, else Kind.
	Key string
	// Verdict is the operator label that decided the cause
	// (ai.LabelVerdictConfirmed or ai.LabelVerdictCorrected), else "".
	Verdict string
}

// EffectiveCause applies the label-over-hypothesis rule. A confirmed or
// corrected label wins over the model: it names a cause (ai.IsNamedCause),
// or it is inconclusive (insufficient_evidence, or a key the running
// catalogue cannot title). Otherwise a ready triage gives its hypothesis the
// same way. A pending triage, or no row yet on a claimable alert, is
// pending; anything else (no row, failed, skipped) is untriaged. Labels are
// only ever written on ready triages; a rejected verdict is ignored.
func EffectiveCause(in CauseInput) Cause {
	if v := in.LabelVerdict.String; (v == ai.LabelVerdictConfirmed || v == ai.LabelVerdictCorrected) &&
		in.LabelKey.Valid && in.LabelKey.String != "" {
		return namedOrInconclusive(in.LabelKey.String, v)
	}
	switch {
	case in.Status.String == models.AlertTriageStatusReady:
		return namedOrInconclusive(in.Hypothesis.String, "")
	case in.Status.String == models.AlertTriageStatusPending, !in.Status.Valid && in.Claimable:
		return Cause{Kind: models.SLACausePending, Key: models.SLACausePending}
	default:
		return Cause{Kind: models.SLACauseUntriaged, Key: models.SLACauseUntriaged}
	}
}

func namedOrInconclusive(key, verdict string) Cause {
	if ai.IsNamedCause(key) {
		return Cause{Kind: models.SLACauseSuspected, Key: key, Verdict: verdict}
	}
	return Cause{Kind: models.SLACauseInconclusive, Key: models.SLACauseInconclusive, Verdict: verdict}
}

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
