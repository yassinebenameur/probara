package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
)

// TestAnalyzeSnapshotMatchesWireRequest pins the snapshot to the bytes that
// went on the wire: labels replay exactly what the model saw.
func TestAnalyzeSnapshotMatchesWireRequest(t *testing.T) {
	var mu sync.Mutex
	sent := map[string]map[string]json.RawMessage{}
	batches := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Decisions []map[string]json.RawMessage `json:"decisions"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
		}
		out := BatchResult{Model: ModelIdentity{Engine: "laya-cpu", Version: "probara-laya-v1", SHA256: strings.Repeat("b", 64), Catalogue: CatalogueVersion}, Thresholds: map[string]float64{"rca": .6, "dependency": .7}}
		mu.Lock()
		batches++
		for _, d := range req.Decisions {
			var id string
			var choices map[string]string
			_ = json.Unmarshal(d["id"], &id)
			_ = json.Unmarshal(d["choices"], &choices)
			sent[id] = d
			p := map[string]float64{}
			for k := range choices {
				p[k] = 0
			}
			p["dns_failure"] = .95
			p["insufficient_evidence"] = .05
			out.Results = append(out.Results, DecisionResult{ID: id, Probabilities: p})
		}
		mu.Unlock()
		_ = json.NewEncoder(w).Encode(out)
	}))
	defer srv.Close()

	a, err := NewAnalyzer(Config{BaseURL: srv.URL, APIKey: "test-internal-token"})
	if err != nil {
		t.Fatal(err)
	}
	in := AnalysisInput{}
	for i := 0; i < 60; i++ {
		e := MonitorEvidence{Checks: 10, Failures: 3}
		if i%2 == 0 {
			e.DNSFailures = 3 // supports dns_failure; odd monitors are downgraded by the guard
		}
		in.Monitors = append(in.Monitors, MonitorContext{Ref: uuid.NewString(), Name: fmt.Sprintf("m%d", i), Affected: true, Summary: e})
	}
	in.Monitors = append(in.Monitors, MonitorContext{Ref: uuid.NewString(), Affected: false})

	result, err := a.Analyze(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if batches != 2 {
		t.Fatalf("expected 2 batches, got %d", batches)
	}
	snap := result.Snapshot
	if snap == nil || len(snap.Decisions) != 60 {
		t.Fatalf("snapshot %+v", snap)
	}
	if snap.V != DecisionSnapshotVersion || snap.Catalogue != CatalogueVersion {
		t.Fatalf("snapshot header %+v", snap)
	}
	if snap.Model.Version != "probara-laya-v1" || snap.Model.SHA256 != strings.Repeat("b", 64) || snap.Thresholds["rca"] != .6 {
		t.Fatalf("model identity %+v %+v", snap.Model, snap.Thresholds)
	}
	if snap.Hypothesis != "dns_failure" || snap.Hypothesis != result.Metadata.Hypothesis || snap.Score != result.Metadata.Score || snap.Score < .94 {
		t.Fatalf("incident outcome %q %v", snap.Hypothesis, snap.Score)
	}
	for i, d := range snap.Decisions {
		wire, ok := sent[d.ID]
		if !ok {
			t.Fatalf("decision %s never sent", d.ID)
		}
		id, _ := json.Marshal(d.ID)
		task, _ := json.Marshal(d.Task)
		choices, _ := json.Marshal(d.Choices)
		for field, want := range map[string][]byte{"id": id, "task": task, "evidence": d.Evidence, "choices": choices} {
			if !bytes.Equal(wire[field], want) {
				t.Fatalf("decision %d %s: wire %s snapshot %s", i, field, wire[field], want)
			}
		}
		if d.MonitorRef != in.Monitors[i].Ref {
			t.Fatalf("decision %d monitor ref %q, want %q", i, d.MonitorRef, in.Monitors[i].Ref)
		}
		if d.Probabilities["dns_failure"] != .95 || len(d.Probabilities) != len(d.Choices) {
			t.Fatalf("probabilities %+v", d.Probabilities)
		}
		if d.Predicted != "dns_failure" {
			t.Fatalf("predicted %q", d.Predicted)
		}
		wantFinal := "dns_failure"
		if i%2 == 1 {
			wantFinal = "insufficient_evidence"
		}
		if d.Final != wantFinal {
			t.Fatalf("decision %d final %q, want %q", i, d.Final, wantFinal)
		}
	}
	// The snapshot never reaches the analysis JSON.
	b, _ := json.Marshal(result)
	if bytes.Contains(b, []byte(`"decisions"`)) || bytes.Contains(b, []byte(`"probabilities"`)) {
		t.Fatalf("snapshot serialized with the result: %s", b)
	}
}

func TestAnalyzeNoDecisionsHasNoSnapshot(t *testing.T) {
	a, err := NewAnalyzer(Config{BaseURL: "http://127.0.0.1:1", APIKey: "test-internal-token"})
	if err != nil {
		t.Fatal(err)
	}
	result, err := a.Analyze(context.Background(), AnalysisInput{Monitors: []MonitorContext{{Ref: "x", Affected: false}}})
	if err != nil {
		t.Fatal(err)
	}
	if result.Snapshot != nil {
		t.Fatalf("snapshot without decisions: %+v", result.Snapshot)
	}
	// A failed exchange returns no result at all, so no snapshot either.
	result, err = a.Analyze(context.Background(), AnalysisInput{Monitors: []MonitorContext{{Ref: "x", Affected: true, Summary: MonitorEvidence{Checks: 1}}}})
	if err == nil || result.Snapshot != nil {
		t.Fatalf("unreachable service: %v %+v", err, result.Snapshot)
	}
}

func TestWithoutPredictions(t *testing.T) {
	s := DecisionSnapshot{
		V: 1, Catalogue: CatalogueVersion, Model: ModelIdentity{Engine: "laya-cpu", Version: "v"},
		Thresholds: map[string]float64{"rca": .6}, Hypothesis: "dns_failure", Score: .9,
		Decisions: []SnapshotDecision{{ID: "d", Task: "rca", MonitorRef: "m", Evidence: json.RawMessage(`{"checks":1}`), Choices: Choices("rca"), Probabilities: map[string]float64{"dns_failure": 1}, Predicted: "dns_failure", Final: "dns_failure"}},
	}
	r := s.WithoutPredictions()
	want := DecisionSnapshot{V: 1, Catalogue: CatalogueVersion, Model: s.Model, Decisions: []SnapshotDecision{{ID: "d", Task: "rca", MonitorRef: "m", Evidence: json.RawMessage(`{"checks":1}`), Choices: Choices("rca")}}}
	if !reflect.DeepEqual(r, want) {
		t.Fatalf("got %+v", r)
	}
	r.Decisions[0].Choices["x"] = "y"
	r.Decisions[0].Evidence[2] = 'X'
	if _, ok := s.Decisions[0].Choices["x"]; ok || s.Decisions[0].Evidence[2] != 'c' {
		t.Fatal("WithoutPredictions aliases the original")
	}
}

func TestChoiceOptions(t *testing.T) {
	opts := ChoiceOptions("rca")
	if len(opts) != len(Choices("rca")) || opts[len(opts)-1].Key != "insufficient_evidence" {
		t.Fatalf("%+v", opts)
	}
	for i := 1; i < len(opts)-1; i++ {
		if opts[i-1].Title > opts[i].Title {
			t.Fatalf("not sorted by title: %+v", opts)
		}
	}
	if ChoiceTitle("rca", "tls_problem") != "TLS or certificate problem" || ChoiceTitle("dependency", "a_depends_b") != "a_depends_b" || ChoiceTitle("rca", "gone") != "gone" {
		t.Fatal("titles")
	}
	dep := ChoiceOptions("dependency")
	if len(dep) != 5 || dep[len(dep)-1].Key != "insufficient_evidence" {
		t.Fatalf("%+v", dep)
	}
}

func TestPairDecisionForMatchesCandidatePairs(t *testing.T) {
	in := DependencyInput{}
	for i := 0; i < 100; i++ {
		in.Monitors = append(in.Monitors, DependencyMonitorRef{Ref: fmt.Sprintf("m%d", i), Name: fmt.Sprintf("svc%d", i), Tags: []string{"billing"}})
	}
	in.Monitors = append(in.Monitors, DependencyMonitorRef{Ref: "lonely", Name: "solo"})
	in.CoFiringAlerts = []CoFiringPair{
		{Earlier: "m2", Later: "m10", TimesCoFired: 3, MedianLeadSeconds: 40, ReverseCoFires: 1},
		{Earlier: "m5", Later: "m6", TimesCoFired: 2, MedianLeadSeconds: 15},
	}
	in.ExistingDependencies = []DependencyEdgeRef{{Monitor: "m7", DependsOn: "up"}, {Monitor: "m8", DependsOn: "up"}}

	capped := CandidatePairs(in)
	all := allCandidatePairs(in)
	if len(capped) != 50 || !reflect.DeepEqual(capped, all[:50]) {
		t.Fatalf("CandidatePairs must be the budgeted prefix: %d", len(capped))
	}
	// Regression: orientation is a < b by ref string ("m10" < "m2"), so a
	// co-firing pair whose earlier monitor sorts second gets a negative lead.
	if capped[0].a != "m10" || capped[0].b != "m2" || capped[0].e.CoFires != 3 || capped[0].e.ALeadsSeconds != -40 || capped[0].e.ReverseCoFires != 1 || !capped[0].e.SharedService {
		t.Fatalf("first candidate %+v", capped[0])
	}
	if capped[1].a != "m5" || capped[1].b != "m6" || capped[1].e.ALeadsSeconds != 15 {
		t.Fatalf("second candidate %+v", capped[1])
	}
	for _, c := range append(append([]candidate{}, capped...), all[100], all[len(all)-1]) {
		a, b, e, ok := PairDecisionFor(in, c.b, c.a) // request orientation must not matter
		if !ok || a != c.a || b != c.b || e != c.e {
			t.Fatalf("PairDecisionFor(%s,%s) = %s %s %+v %v, want %+v", c.b, c.a, a, b, e, ok, c)
		}
	}
	if _, _, e, ok := PairDecisionFor(in, "m7", "m8"); !ok || !e.CommonUpstream {
		t.Fatalf("common upstream pair %+v %v", e, ok)
	}
	if _, _, _, ok := PairDecisionFor(in, "m1", "lonely"); ok {
		t.Fatal("non-candidate pair resolved")
	}
	if _, _, _, ok := PairDecisionFor(in, "m1", "m1"); ok {
		t.Fatal("self pair resolved")
	}
	d := PairSnapshotDecision("A", "B", capped[0].e)
	if d.Task != "dependency" || d.Pair == nil || d.Pair.A != "A" || d.Pair.B != "B" || !reflect.DeepEqual(d.Choices, Choices("dependency")) {
		t.Fatalf("%+v", d)
	}
	wire, _ := json.Marshal(capped[0].e)
	if !bytes.Equal(d.Evidence, wire) || !validUUID(d.ID) {
		t.Fatalf("%+v", d)
	}
}

func validUUID(s string) bool {
	_, err := uuid.Parse(s)
	return err == nil
}
