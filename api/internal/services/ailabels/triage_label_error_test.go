package ailabels

import (
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/shared/ai"
)

// Every LabelValidationError branch of ResolveIncidentAnswers, rewritten for
// the triage endpoint, must name its body field (label) and never the
// incident endpoint's incident_label or "analysis".
func TestTriageLabelErrorNamesTriageField(t *testing.T) {
	snap := testSnapshot(uuid.New(), uuid.New())
	for name, c := range map[string]struct {
		verdict, label string
		answers        map[string]string
		wantField      bool
	}{
		"corrected without label": {verdict: "corrected", wantField: true},
		"label not a choice":      {verdict: "corrected", label: "made_up", wantField: true},
		"confirm mismatched":      {verdict: "confirmed", label: "tls_problem", wantField: true},
		"unknown decision":        {verdict: "corrected", label: "dns_failure", answers: map[string]string{"x": "dns_failure"}},
	} {
		_, _, err := ai.ResolveIncidentAnswers(snap, c.verdict, c.label, c.answers)
		var ve *ai.LabelValidationError
		if !errors.As(triageLabelError(err), &ve) {
			t.Fatalf("%s: expected LabelValidationError, got %v", name, err)
		}
		if strings.Contains(ve.Msg, "incident_label") || strings.Contains(ve.Msg, "analysis") {
			t.Errorf("%s: incident wording leaked to triage: %q", name, ve.Msg)
		}
		if c.wantField && !strings.HasPrefix(ve.Msg, "label ") {
			t.Errorf("%s: message does not name the label field: %q", name, ve.Msg)
		}
	}

	other := errors.New("boom")
	if triageLabelError(other) != other {
		t.Fatal("non-validation errors must pass through unchanged")
	}
}
