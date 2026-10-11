package ai

import (
	"encoding/json"
	"errors"
	"fmt"
)

// Label answer sources: who picked a decision's label.
const (
	LabelSourceOperator   = "operator"   // chosen explicitly for this decision
	LabelSourceConfirmed  = "confirmed"  // the model's final answer, confirmed
	LabelSourcePropagated = "propagated" // derived from the incident-level correction
)

// Label verdicts.
const (
	LabelVerdictConfirmed = "confirmed"
	LabelVerdictCorrected = "corrected"
	LabelVerdictRejected  = "rejected"
)

// LabelAnswer is the operator-backed answer to one snapshot decision.
type LabelAnswer struct {
	Label  string `json:"label"`
	Source string `json:"source"` // "operator" | "confirmed" | "propagated"
}

// LabelValidationError is a label request that contradicts the snapshot or
// the catalogue. Handlers map it to 400.
type LabelValidationError struct{ Msg string }

func (e *LabelValidationError) Error() string { return e.Msg }

func invalidLabel(format string, args ...any) error {
	return &LabelValidationError{Msg: fmt.Sprintf(format, args...)}
}

// ErrSnapshotNotLabelable means a stored snapshot carries no usable decision
// (empty, or written without a final answer).
var ErrSnapshotNotLabelable = errors.New("decision snapshot cannot be labelled")

// ResolveIncidentAnswers turns an operator verdict into one answer per snapshot
// decision. Validation is against the SNAPSHOT's choices (a catalogue-version
// mismatch with the running binary is allowed).
//
//	confirmed: incidentLabel must be "" or == snap.Hypothesis; result incident
//	           label = snap.Hypothesis; un-overridden decisions get
//	           {d.Final, "confirmed"}.
//	corrected: incidentLabel required, must be a key of every decision's
//	           choices; un-overridden decisions get {incidentLabel, "propagated"}
//	           when the decision's MonitorEvidence.Supports(incidentLabel), else
//	           {"insufficient_evidence", "propagated"} (the guard production and
//	           train.py apply, so propagated rows never teach an unsupported
//	           answer).
//	overrides: decision id -> key; unknown id or key not in that decision's
//	           choices -> *LabelValidationError. Source "operator".
func ResolveIncidentAnswers(snap DecisionSnapshot, verdict, incidentLabel string, overrides map[string]string) (map[string]LabelAnswer, string, error) {
	if len(snap.Decisions) == 0 {
		return nil, "", ErrSnapshotNotLabelable
	}
	for _, d := range snap.Decisions {
		if d.Task != "rca" || d.ID == "" || d.Final == "" {
			return nil, "", ErrSnapshotNotLabelable
		}
	}
	incident := ""
	switch verdict {
	case LabelVerdictConfirmed:
		if snap.Hypothesis == "" {
			return nil, "", ErrSnapshotNotLabelable
		}
		if incidentLabel != "" && incidentLabel != snap.Hypothesis {
			return nil, "", invalidLabel("incident_label must be empty or equal the predicted cause %q when confirming; use verdict \"corrected\" to change it", snap.Hypothesis)
		}
		incident = snap.Hypothesis
	case LabelVerdictCorrected:
		if incidentLabel == "" {
			return nil, "", invalidLabel("incident_label is required when correcting an analysis")
		}
		for _, d := range snap.Decisions {
			if _, ok := d.Choices[incidentLabel]; !ok {
				return nil, "", invalidLabel("incident_label %q is not a choice of this analysis", incidentLabel)
			}
		}
		incident = incidentLabel
	default:
		return nil, "", invalidLabel("verdict must be \"confirmed\" or \"corrected\"")
	}

	byID := map[string]SnapshotDecision{}
	for _, d := range snap.Decisions {
		byID[d.ID] = d
	}
	for id, key := range overrides {
		d, ok := byID[id]
		if !ok {
			return nil, "", invalidLabel("answers: unknown decision id %q", id)
		}
		if _, ok := d.Choices[key]; !ok {
			return nil, "", invalidLabel("answers: %q is not a choice of decision %q", key, id)
		}
	}

	answers := make(map[string]LabelAnswer, len(snap.Decisions))
	for _, d := range snap.Decisions {
		if key, ok := overrides[d.ID]; ok {
			answers[d.ID] = LabelAnswer{Label: key, Source: LabelSourceOperator}
			continue
		}
		if verdict == LabelVerdictConfirmed {
			answers[d.ID] = LabelAnswer{Label: d.Final, Source: LabelSourceConfirmed}
			continue
		}
		var e MonitorEvidence
		if err := json.Unmarshal(d.Evidence, &e); err != nil {
			return nil, "", fmt.Errorf("decode snapshot evidence: %w", err)
		}
		label := incidentLabel
		if !e.Supports(label) {
			label = "insufficient_evidence"
		}
		answers[d.ID] = LabelAnswer{Label: label, Source: LabelSourcePropagated}
	}
	return answers, incident, nil
}

// Dependency label corrections accepted with a "rejected" verdict.
const (
	DependencyCorrectionReversed             = "reversed"
	DependencyCorrectionSharedUpstream       = "shared_upstream"
	DependencyCorrectionNoDependency         = "no_dependency"
	DependencyCorrectionInsufficientEvidence = "insufficient_evidence"
)

// ValidateDependencyVerdict checks a pair verdict/correction combination
// before any evidence is computed.
func ValidateDependencyVerdict(verdict, corrected string) error {
	switch verdict {
	case LabelVerdictConfirmed:
		if corrected != "" {
			return invalidLabel("corrected is only allowed with verdict \"rejected\"")
		}
	case LabelVerdictRejected:
		switch corrected {
		case "", DependencyCorrectionNoDependency, DependencyCorrectionReversed, DependencyCorrectionSharedUpstream, DependencyCorrectionInsufficientEvidence:
		default:
			return invalidLabel("corrected must be one of reversed, shared_upstream, no_dependency, insufficient_evidence")
		}
	default:
		return invalidLabel("verdict must be \"confirmed\" or \"rejected\"")
	}
	return nil
}

// DependencyAnswer maps a pair verdict onto the decision's orientation.
// monitorID is the monitor the suggestion said depends on the other.
//
//	confirmed (corrected must be ""): monitorID == Pair.A -> "a_depends_b"; == Pair.B -> "b_depends_a".
//	rejected: corrected "" | "no_dependency" -> "no_dependency";
//	          "reversed" -> the opposite direction key;
//	          "shared_upstream" | "insufficient_evidence" -> that key.
//
// Source is always "operator".
func DependencyAnswer(d SnapshotDecision, monitorID, verdict, corrected string) (LabelAnswer, error) {
	if err := ValidateDependencyVerdict(verdict, corrected); err != nil {
		return LabelAnswer{}, err
	}
	if d.Task != "dependency" || d.Pair == nil {
		return LabelAnswer{}, errors.New("not a dependency decision")
	}
	var forward, backward string
	switch monitorID {
	case d.Pair.A:
		forward, backward = "a_depends_b", "b_depends_a"
	case d.Pair.B:
		forward, backward = "b_depends_a", "a_depends_b"
	default:
		return LabelAnswer{}, invalidLabel("monitor %q is not part of this pair", monitorID)
	}
	label := forward
	if verdict == LabelVerdictRejected {
		switch corrected {
		case "", DependencyCorrectionNoDependency:
			label = "no_dependency"
		case DependencyCorrectionReversed:
			label = backward
		default:
			label = corrected
		}
	}
	if _, ok := d.Choices[label]; !ok {
		return LabelAnswer{}, invalidLabel("%q is not a choice of this decision", label)
	}
	return LabelAnswer{Label: label, Source: LabelSourceOperator}, nil
}

// LabelPredictions is the stored "predicted" half of an rca label
// (ai_labels.predicted): what the model answered, keyed by decision id.
type LabelPredictions struct {
	Hypothesis string                        `json:"hypothesis,omitempty"`
	Score      float64                       `json:"calibrated_score,omitempty"`
	Thresholds map[string]float64            `json:"thresholds,omitempty"`
	Decisions  map[string]DecisionPrediction `json:"decisions"`
}

// DecisionPrediction is the model's answer to one decision.
type DecisionPrediction struct {
	Predicted     string             `json:"predicted,omitempty"`
	Final         string             `json:"final,omitempty"`
	Probabilities map[string]float64 `json:"probabilities,omitempty"`
}

// SnapshotPredictions extracts the predictions half of a snapshot.
func SnapshotPredictions(s DecisionSnapshot) LabelPredictions {
	out := LabelPredictions{Hypothesis: s.Hypothesis, Score: s.Score, Thresholds: s.Thresholds, Decisions: map[string]DecisionPrediction{}}
	for _, d := range s.Decisions {
		out.Decisions[d.ID] = DecisionPrediction{Predicted: d.Predicted, Final: d.Final, Probabilities: d.Probabilities}
	}
	return out
}

// AsSnapshot is the inverse of SnapshotPredictions for the exporter, which
// reads only Decisions[].ID/Predicted/Final/Probabilities.
func (p LabelPredictions) AsSnapshot() *DecisionSnapshot {
	out := &DecisionSnapshot{Hypothesis: p.Hypothesis, Score: p.Score, Thresholds: p.Thresholds}
	for id, d := range p.Decisions {
		out.Decisions = append(out.Decisions, SnapshotDecision{ID: id, Predicted: d.Predicted, Final: d.Final, Probabilities: d.Probabilities})
	}
	return out
}
