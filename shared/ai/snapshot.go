package ai

import (
	"encoding/json"
	"maps"

	"github.com/google/uuid"
)

// DecisionSnapshotVersion is the shape version of DecisionSnapshot.
const DecisionSnapshotVersion = 1

// DecisionSnapshot is the server-side record of one model exchange: exactly the
// decisions sent (id, task, evidence, choices) plus what came back. It never
// crosses the inference boundary; MonitorRef/Pair stay server-side and are
// dropped by the label exporter (WriteLabelExport).
//
// Operator labels copy it server-side (incident_ai_analyses.decision_snapshot,
// or depsuggest.PairDecision for pairs) so a training row replays the exact
// evidence the model saw, never anything a client submitted.
type DecisionSnapshot struct {
	V          int                `json:"v"`
	Catalogue  string             `json:"catalogue"` // CatalogueVersion at send time
	Model      ModelIdentity      `json:"model"`     // identity reported by the batch
	Thresholds map[string]float64 `json:"thresholds,omitempty"`
	Hypothesis string             `json:"hypothesis,omitempty"` // incident-level outcome (rca)
	Score      float64            `json:"calibrated_score,omitempty"`
	Decisions  []SnapshotDecision `json:"decisions"`
}

// SnapshotDecision is one decision as sent, plus the model's answer to it.
type SnapshotDecision struct {
	ID            string             `json:"id"`
	Task          string             `json:"task"`                  // "rca" | "dependency"
	MonitorRef    string             `json:"monitor_ref,omitempty"` // rca: MonitorContext.Ref (monitor UUID in the worker)
	Pair          *SnapshotPair      `json:"pair,omitempty"`        // dependency: monitor UUIDs in evidence orientation
	Evidence      json.RawMessage    `json:"evidence"`              // json.Marshal of the exact Evidence value sent
	Choices       map[string]string  `json:"choices"`               // exact Choices sent
	Probabilities map[string]float64 `json:"probabilities,omitempty"`
	Predicted     string             `json:"predicted,omitempty"` // best() argmax
	Final         string             `json:"final,omitempty"`     // after threshold + Supports guard (what the UI showed)
}

// SnapshotPair names the monitors behind a dependency decision, A and B in
// the orientation of its PairEvidence.
type SnapshotPair struct {
	A string `json:"a"`
	B string `json:"b"`
}

// WithoutPredictions returns a copy with Probabilities/Predicted/Final/
// Thresholds/Hypothesis/Score cleared: the "request" half stored on
// ai_labels.request. The copy shares no maps or slices with s.
func (s DecisionSnapshot) WithoutPredictions() DecisionSnapshot {
	out := DecisionSnapshot{V: s.V, Catalogue: s.Catalogue, Model: s.Model, Decisions: make([]SnapshotDecision, len(s.Decisions))}
	for i, d := range s.Decisions {
		c := SnapshotDecision{ID: d.ID, Task: d.Task, MonitorRef: d.MonitorRef, Choices: maps.Clone(d.Choices)}
		if d.Pair != nil {
			p := *d.Pair
			c.Pair = &p
		}
		if d.Evidence != nil {
			c.Evidence = append(json.RawMessage(nil), d.Evidence...)
		}
		out.Decisions[i] = c
	}
	return out
}

// mustMarshalEvidence encodes evidence the way the wire request does. Evidence
// types are flat structs of ints and bools, so encoding cannot fail.
func mustMarshalEvidence(e any) json.RawMessage {
	b, err := json.Marshal(e)
	if err != nil {
		panic(err)
	}
	return b
}

// PairSnapshotDecision builds a dependency SnapshotDecision for the pair
// (aMonitorID, bMonitorID) in the orientation of e: a new id, Task
// "dependency" and the running catalogue's dependency choices, exactly as
// SuggestDependencies would send it.
func PairSnapshotDecision(aMonitorID, bMonitorID string, e PairEvidence) SnapshotDecision {
	return SnapshotDecision{
		ID:       uuid.NewString(),
		Task:     "dependency",
		Pair:     &SnapshotPair{A: aMonitorID, B: bMonitorID},
		Evidence: mustMarshalEvidence(e),
		Choices:  choices("dependency"),
	}
}
