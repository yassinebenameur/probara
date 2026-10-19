package ailabels

import (
	"encoding/json"

	"strings"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/shared/ai"
)

// testSnapshot is what the worker stores for a two-monitor DNS incident: m1
// has DNS evidence, m2 only connection failures (its dns answer was
// downgraded by the guard).
func testSnapshot(m1, m2 uuid.UUID) ai.DecisionSnapshot {
	ev := func(e ai.MonitorEvidence) json.RawMessage { b, _ := json.Marshal(e); return b }
	return ai.DecisionSnapshot{
		V: 1, Catalogue: ai.CatalogueVersion,
		Model:      ai.ModelIdentity{Engine: "laya-cpu", Version: "probara-laya-v1", SHA256: strings.Repeat("c", 64), Catalogue: ai.CatalogueVersion},
		Thresholds: map[string]float64{"rca": .6}, Hypothesis: "dns_failure", Score: .9,
		Decisions: []ai.SnapshotDecision{
			{ID: uuid.NewString(), Task: "rca", MonitorRef: m1.String(), Evidence: ev(ai.MonitorEvidence{Checks: 10, Failures: 4, DNSFailures: 4}), Choices: ai.Choices("rca"),
				Probabilities: map[string]float64{"dns_failure": .9, "insufficient_evidence": .1}, Predicted: "dns_failure", Final: "dns_failure"},
			{ID: uuid.NewString(), Task: "rca", MonitorRef: m2.String(), Evidence: ev(ai.MonitorEvidence{Checks: 10, Failures: 4, ConnectionFailures: 4}), Choices: ai.Choices("rca"),
				Probabilities: map[string]float64{"dns_failure": .9, "insufficient_evidence": .1}, Predicted: "dns_failure", Final: "insufficient_evidence"},
		},
	}
}
