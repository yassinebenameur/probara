package models

import (
	"time"

	"github.com/google/uuid"
)

// Alert triage statuses (alert_triage.status).
const (
	AlertTriageStatusPending = "pending"
	AlertTriageStatusReady   = "ready"
	AlertTriageStatusFailed  = "failed"
	AlertTriageStatusSkipped = "skipped"
)

// AlertTriage is the compact, read-only view of an alert's automatic triage
// (alert_triage, written by the worker's relay and triage consumer). It never
// influences routing, suppression, severity or the page events; the alerter
// reads alert_triage only to send the opt-in suspected_cause follow-up.
// Nullable fields are
// emitted as null, not omitted; the whole object is absent when the alert has
// no triage row (and on SSE events, which are built from NATS and never carry
// it).
type AlertTriage struct {
	ID           uuid.UUID       `json:"id"`
	Status       string          `json:"status"`        // pending|ready|failed|skipped
	Hypothesis   *string         `json:"hypothesis"`    // catalogue rca key; ready only
	Title        *string         `json:"title"`         // ai.ChoiceTitle("rca", hypothesis); ready only
	Confidence   *string         `json:"confidence"`    // low|medium|high; ready only
	Summary      *string         `json:"summary"`       // ready only
	SkipReason   *string         `json:"skip_reason"`   // resolved|cooldown|not_modeled|disabled; skipped only
	ErrorMessage *string         `json:"error_message"` // failed only (fixed strings)
	CreatedAt    time.Time       `json:"created_at"`
	CompletedAt  *time.Time      `json:"completed_at"`
	Labelled     bool            `json:"labelled"`
	Labelable    bool            `json:"labelable"`               // ready AND the snapshot has at least one decision
	LabelContext *AILabelContext `json:"label_context,omitempty"` // GET /alerts/{id} only, when Labelable
}

// AlertTriageLabelRequest is the body of POST /alerts/{id}/triage/label.
// Label is the alert-level catalogue key: required for "corrected", and when
// set with "confirmed" it must equal the triage hypothesis. Answers are
// per-decision overrides keyed by the snapshot's opaque decision id.
type AlertTriageLabelRequest struct {
	Verdict string            `json:"verdict"`
	Label   string            `json:"label,omitempty"`
	Answers map[string]string `json:"answers,omitempty"`
}
