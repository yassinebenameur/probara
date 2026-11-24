package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// strictEvidence decodes a fixture's evidence object, refusing any key
// PairEvidence does not have, so the fixtures and the struct cannot drift.
func strictEvidence(t *testing.T, raw json.RawMessage) PairEvidence {
	t.Helper()
	var e PairEvidence
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&e); err != nil {
		t.Fatalf("evidence %s: %v", raw, err)
	}
	var keys map[string]any
	_ = json.Unmarshal(raw, &keys)
	if n := len(keys); n != 19 {
		t.Fatalf("evidence has %d keys, want all 19: %s", n, raw)
	}
	return e
}

func readFixture(t *testing.T, name string, v any) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, v); err != nil {
		t.Fatal(err)
	}
}

// testdata/dependency-guard-cases.json is shared with the ai-service guard:
// both languages must reproduce every case.
func TestDependencyGuardCases(t *testing.T) {
	var f struct {
		RareMax int `json:"rare_max"`
		Cases   []struct {
			Name     string          `json:"name"`
			Evidence json.RawMessage `json:"evidence"`
			Basis    string          `json:"basis"`
			Label    string          `json:"label"`
		} `json:"cases"`
	}
	readFixture(t, "dependency-guard-cases.json", &f)
	if f.RareMax != dependencyRareMax {
		t.Fatalf("rare_max %d, dependencyRareMax %d", f.RareMax, dependencyRareMax)
	}
	if len(f.Cases) == 0 {
		t.Fatal("no cases")
	}
	for _, c := range f.Cases {
		basis, label := DependencyGuard(strictEvidence(t, c.Evidence))
		if basis != c.Basis || label != c.Label {
			t.Errorf("%s: got %s/%s, want %s/%s", c.Name, basis, label, c.Basis, c.Label)
		}
	}
}

// testdata/dependency-regression.json holds operator-verified pairs: the guard
// must return the expected outcome for each, never suggest a pair the operator
// rejected, and never propose a direction the operator contradicts.
func TestDependencyGuardRegression(t *testing.T) {
	var f struct {
		Rows []struct {
			Source        string          `json:"source"`
			Truth         string          `json:"truth"`
			Evidence      json.RawMessage `json:"evidence"`
			ExpectedBasis string          `json:"expected_basis"`
			ExpectedLabel string          `json:"expected_label"`
		} `json:"rows"`
	}
	readFixture(t, "dependency-regression.json", &f)
	if len(f.Rows) == 0 {
		t.Fatal("no rows")
	}
	for i, r := range f.Rows {
		basis, label := DependencyGuard(strictEvidence(t, r.Evidence))
		if basis != r.ExpectedBasis || label != r.ExpectedLabel {
			t.Errorf("row %d (%s): got %s/%s, want %s/%s", i, r.Source, basis, label, r.ExpectedBasis, r.ExpectedLabel)
		}
		if r.Truth == "no_dependency" && basis != DependencyBasisNone {
			t.Errorf("row %d: operator-rejected pair gets basis %s", i, basis)
		}
		if (label == "a_depends_b" || label == "b_depends_a") && label != r.Truth {
			t.Errorf("row %d: direction %s contradicts the operator's %s", i, label, r.Truth)
		}
	}
}

func evidenceFor(t *testing.T, in DependencyInput, x, y string) PairEvidence {
	t.Helper()
	_, _, e, ok := PairDecisionFor(in, x, y)
	if !ok {
		t.Fatalf("pair %s/%s is not a candidate", x, y)
	}
	return e
}

func TestPairEvidenceStructureFields(t *testing.T) {
	in := DependencyInput{Monitors: []DependencyMonitorRef{
		{Ref: "a", Name: "Billing API", Type: "http", Tags: []string{"billing", "prod"}, Groups: []DependencyGroupRef{{"g1", 4}, {"g2", 40}}, AlertWindows: 7},
		{Ref: "b", Name: "billing_api.degraded", Type: "http", Tags: []string{"billing", "prod"}, Groups: []DependencyGroupRef{{"g1", 4}}, AlertWindows: 3},
		{Ref: "c", Name: "billing-api-db", Type: "postgres", Tags: []string{"billing", "storage"}, Groups: []DependencyGroupRef{{"g2", 40}}},
		{Ref: "d", Name: "billing-api-worker", Type: "http", Tags: []string{"billing", "Prod"}},
		{Ref: "e", Name: "ledger", Type: "redis", Tags: []string{"prod"}},
	}, CoFiringAlerts: []CoFiringPair{{Earlier: "b", Later: "a", TimesCoFired: 3, MedianLeadSeconds: 40, WindowMonitors: 5}}}

	e := evidenceFor(t, in, "a", "b")
	want := PairEvidence{CoFires: 3, ALeadsSeconds: -40, SharedService: true, SharedGroup: true, NamingSignal: true,
		AWindows: 7, BWindows: 3, WindowMonitors: 5, SharedTagMonitors: 4, SharedGroupMonitors: 4, SharedNamePrefix: 2,
		BExtendsA: true, SameType: true}
	if e != want {
		t.Fatalf("a/b\n got %+v\nwant %+v", e, want)
	}
	// "billing" is held by four monitors; "prod" is generic and never counts.
	e = evidenceFor(t, in, "a", "c")
	if e.SharedTagMonitors != 4 || e.SharedGroupMonitors != 40 || e.SharedNamePrefix != 2 || !e.BExtendsA || e.AExtendsB || e.SameType || e.AInfra || !e.BInfra || e.Peers || e.WindowMonitors != 0 {
		t.Fatalf("a/c %+v", e)
	}
	// Same type and tag set (generic tags ignored), neither name extends the
	// other... except here d extends a, so they are not peers.
	if e = evidenceFor(t, in, "a", "d"); e.Peers || !e.BExtendsA {
		t.Fatalf("a/d %+v", e)
	}
	// b and d: same type, same service tags, no extension: peers.
	if e = evidenceFor(t, in, "b", "d"); !e.Peers || e.SharedNamePrefix != 2 || e.AExtendsB || e.BExtendsA {
		t.Fatalf("b/d %+v", e)
	}
	// Only a generic tag in common and no name signal: not a candidate.
	if _, _, _, ok := PairDecisionFor(in, "a", "e"); ok {
		t.Fatal("generic-only pair is a candidate")
	}
	// A two-token name prefix alone keeps a pair a candidate.
	in = DependencyInput{Monitors: []DependencyMonitorRef{{Ref: "x", Name: "my-db", Type: "http"}, {Ref: "y", Name: "my-db-replica", Type: "mysql"}}}
	if e = evidenceFor(t, in, "x", "y"); e.SharedNamePrefix != 2 || e.NamingSignal || e.SharedService {
		t.Fatalf("x/y %+v", e)
	}
}

// guardServer answers each decision with the label the test's oracle picks
// and counts the decisions it saw.
func guardServer(t *testing.T, sent *int, pick func(choices map[string]string) string, score float64) *httptest.Server {
	t.Helper()
	var mu sync.Mutex
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Decisions []Decision `json:"decisions"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
		}
		out := BatchResult{Model: ModelIdentity{Engine: "laya-cpu", Version: "fixture", SHA256: strings.Repeat("a", 64), Catalogue: CatalogueVersion}, Thresholds: map[string]float64{"dependency": .6}}
		mu.Lock()
		*sent += len(req.Decisions)
		mu.Unlock()
		for _, d := range req.Decisions {
			chosen := pick(d.Choices)
			p := map[string]float64{}
			for k := range d.Choices {
				p[k] = 0
			}
			p[chosen] = score
			p["insufficient_evidence"] += 1 - score
			if chosen == "insufficient_evidence" {
				p[chosen] = 1
			}
			out.Results = append(out.Results, DecisionResult{ID: d.ID, Probabilities: p})
		}
		_ = json.NewEncoder(w).Encode(out)
	}))
}

// Guard-rejected pairs with the most co-fires must not spend the budget: a
// 30-monitor pile-up (435 pairs, 20 co-fires each) no longer starves the one
// structural pair, and only that pair reaches the model.
func TestSuggestBudgetSkipsGuardRejectedPairs(t *testing.T) {
	in := DependencyInput{}
	for i := 0; i < 30; i++ {
		in.Monitors = append(in.Monitors, DependencyMonitorRef{Ref: fmt.Sprintf("p%02d", i), Name: fmt.Sprintf("edge node %d", i), Type: "http", Tags: []string{"edge"}})
	}
	for i := 0; i < 30; i++ {
		for j := i + 1; j < 30; j++ {
			in.CoFiringAlerts = append(in.CoFiringAlerts, CoFiringPair{Earlier: fmt.Sprintf("p%02d", i), Later: fmt.Sprintf("p%02d", j), TimesCoFired: 20, MedianLeadSeconds: 30, WindowMonitors: 30})
		}
	}
	in.Monitors = append(in.Monitors,
		DependencyMonitorRef{Ref: "svc", Name: "billing-api", Type: "http", Tags: []string{"billing"}},
		DependencyMonitorRef{Ref: "store", Name: "billing-store", Type: "postgres", Tags: []string{"billing"}})

	sent := 0
	srv := guardServer(t, &sent, func(map[string]string) string { return "b_depends_a" }, .95)
	defer srv.Close()
	advisor, _ := NewDependencyAdvisor(Config{BaseURL: srv.URL, APIKey: "test-internal-token"})
	out, err := advisor.SuggestDependencies(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if sent != 1 {
		t.Fatalf("%d decisions sent, want only the structural pair", sent)
	}
	// "store" < "svc": the service is B, so b_depends_a is the guard's label.
	if len(out.Suggestions) != 1 || out.Suggestions[0].Monitor != "svc" || out.Suggestions[0].DependsOn != "store" {
		t.Fatalf("%+v", out)
	}
	if out.Analyzed != 436 || out.Abstained != 435 || out.Analyzed != out.Abstained+len(out.Suggestions) {
		t.Fatalf("analyzed %d abstained %d", out.Analyzed, out.Abstained)
	}
}

// No guard-eligible pair: nothing is sent, every pair abstains.
func TestSuggestWithoutEligiblePairsSkipsModel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("model called without an eligible pair")
	}))
	defer srv.Close()
	advisor, _ := NewDependencyAdvisor(Config{BaseURL: srv.URL, APIKey: "test-internal-token"})
	in := DependencyInput{Monitors: []DependencyMonitorRef{{Ref: "a", Name: "web-1", Type: "http", Tags: []string{"web"}}, {Ref: "b", Name: "web-2", Type: "http", Tags: []string{"web"}}},
		CoFiringAlerts: []CoFiringPair{{Earlier: "a", Later: "b", TimesCoFired: 9, MedianLeadSeconds: 60, WindowMonitors: 2}}}
	out, err := advisor.SuggestDependencies(context.Background(), in)
	if err != nil || out.Analyzed != 1 || out.Abstained != 1 || len(out.Suggestions) != 0 || out.Model != "" {
		t.Fatalf("%+v %v", out, err)
	}
}

// A suggestion needs the model to pick exactly the guard's label.
func TestSuggestRequiresGuardLabel(t *testing.T) {
	sent := 0
	srv := guardServer(t, &sent, func(map[string]string) string { return "a_depends_b" }, .95)
	defer srv.Close()
	advisor, _ := NewDependencyAdvisor(Config{BaseURL: srv.URL, APIKey: "test-internal-token"})
	in := DependencyInput{Monitors: []DependencyMonitorRef{
		{Ref: "a", Name: "billing-store", Type: "postgres", Tags: []string{"billing"}},
		{Ref: "b", Name: "billing-api", Type: "http", Tags: []string{"billing"}},
	}}
	out, err := advisor.SuggestDependencies(context.Background(), in)
	if err != nil || sent != 1 || len(out.Suggestions) != 0 || out.Abstained != 1 {
		t.Fatalf("model direction against the guard accepted: %+v %v", out, err)
	}
}

func TestSuggestionBasisReasonAndConfidence(t *testing.T) {
	sent := 0
	// Answer each decision with its guard label at a high score.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Decisions []struct {
				ID       string            `json:"id"`
				Evidence PairEvidence      `json:"evidence"`
				Choices  map[string]string `json:"choices"`
			} `json:"decisions"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		sent += len(req.Decisions)
		out := BatchResult{Model: ModelIdentity{Engine: "laya-cpu", Version: "fixture", SHA256: strings.Repeat("a", 64), Catalogue: CatalogueVersion}, Thresholds: map[string]float64{"dependency": .6}}
		for _, d := range req.Decisions {
			_, label := DependencyGuard(d.Evidence)
			p := map[string]float64{}
			for k := range d.Choices {
				p[k] = 0
			}
			p[label] = .95
			p["insufficient_evidence"] = .05
			out.Results = append(out.Results, DecisionResult{ID: d.ID, Probabilities: p})
		}
		_ = json.NewEncoder(w).Encode(out)
	}))
	defer srv.Close()
	advisor, _ := NewDependencyAdvisor(Config{BaseURL: srv.URL, APIKey: "test-internal-token"})
	in := DependencyInput{
		Monitors: []DependencyMonitorRef{
			// timing: two services, rare tag, directed small windows.
			{Ref: "t1", Name: "orders-gateway", Type: "http", Tags: []string{"orders", "edge"}},
			{Ref: "t2", Name: "orders-api", Type: "http", Tags: []string{"orders"}},
			// variant: "checkout" fails first, "checkout slow" after it.
			{Ref: "v1", Name: "checkout", Type: "http"},
			{Ref: "v2", Name: "checkout slow", Type: "http"},
			// structural: a service and a backing resource sharing a rare tag.
			{Ref: "s1", Name: "invoices-api", Type: "http", Tags: []string{"invoices"}},
			{Ref: "s2", Name: "ledger-store", Type: "mysql", Tags: []string{"invoices"}},
		},
		CoFiringAlerts: []CoFiringPair{
			{Earlier: "t1", Later: "t2", TimesCoFired: 4, MedianLeadSeconds: 45, WindowMonitors: 2},
			{Earlier: "v1", Later: "v2", TimesCoFired: 2, MedianLeadSeconds: 90, WindowMonitors: 6},
		},
	}
	out, err := advisor.SuggestDependencies(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if sent != 3 || len(out.Suggestions) != 3 || out.Analyzed != out.Abstained+3 {
		t.Fatalf("sent %d %+v", sent, out)
	}
	by := map[string]DependencySuggestion{}
	for _, s := range out.Suggestions {
		by[s.Basis] = s
	}
	// The budget orders timing, variant, structural.
	if out.Suggestions[0].Basis != DependencyBasisTiming || out.Suggestions[1].Basis != DependencyBasisVariant || out.Suggestions[2].Basis != DependencyBasisStructural {
		t.Fatalf("order %+v", out.Suggestions)
	}
	timing, variant, structural := by[DependencyBasisTiming], by[DependencyBasisVariant], by[DependencyBasisStructural]
	if timing.Monitor != "t2" || timing.DependsOn != "t1" || timing.Confidence != "high" ||
		!strings.HasPrefix(timing.Reason, "Alert timing: 4 co-firing alert windows") || !strings.Contains(timing.Reason, "tag held by only 2 monitors") ||
		!strings.Contains(timing.Reason, "median of 2 monitors alerted") || !strings.HasSuffix(timing.Reason, "does not prove causation.") ||
		timing.Metadata.Evidence[0].Ref != "pair/t1/t2/alerts" {
		t.Fatalf("timing %+v", timing)
	}
	if variant.Monitor != "v2" || variant.DependsOn != "v1" || variant.Confidence != "high" ||
		!strings.HasPrefix(variant.Reason, "Same target variant:") || !strings.Contains(variant.Reason, "in 2 alert windows") {
		t.Fatalf("variant %+v", variant)
	}
	if structural.Monitor != "s1" || structural.DependsOn != "s2" || structural.Confidence != "low" ||
		structural.Metadata.Score != .95 || structural.Metadata.Hypothesis != "a_depends_b" || structural.Metadata.Evidence[0].Ref != "pair/s1/s2/structure" ||
		!strings.HasPrefix(structural.Reason, "Structure, no alert correlation required:") || !strings.Contains(structural.Reason, "tag held by only 2 monitors") {
		t.Fatalf("structural %+v", structural)
	}
	for _, s := range out.Suggestions {
		for _, name := range []string{"orders", "checkout", "invoices", "ledger"} {
			if strings.Contains(s.Reason, name) {
				t.Fatalf("reason renders a name: %q", s.Reason)
			}
		}
	}
}

// A service sharing a rare tag with a resource "X" and its variant "X …"
// is suggested only against the base.
func TestStructuralVariantUpstreamDropped(t *testing.T) {
	sent := 0
	srv := guardServer(t, &sent, func(map[string]string) string { return "a_depends_b" }, .95)
	defer srv.Close()
	advisor, _ := NewDependencyAdvisor(Config{BaseURL: srv.URL, APIKey: "test-internal-token"})
	// "app" sorts before both resources, so a_depends_b is the guard's label
	// for both service/resource pairs; the resource pair itself is rejected.
	in := DependencyInput{Monitors: []DependencyMonitorRef{
		{Ref: "app", Name: "reports-api", Type: "http", Tags: []string{"reports"}},
		{Ref: "db1", Name: "reports store", Type: "postgres", Tags: []string{"reports"}},
		{Ref: "db2", Name: "reports store replica lag", Type: "postgres", Tags: []string{"reports"}},
	}}
	out, err := advisor.SuggestDependencies(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if sent != 2 || len(out.Suggestions) != 1 || out.Suggestions[0].DependsOn != "db1" || out.Analyzed != out.Abstained+1 {
		t.Fatalf("sent %d %+v", sent, out)
	}
	// Without the base, the variant is kept.
	in.Monitors = []DependencyMonitorRef{in.Monitors[0], in.Monitors[2]}
	if out, err = advisor.SuggestDependencies(context.Background(), in); err != nil || len(out.Suggestions) != 1 || out.Suggestions[0].DependsOn != "db2" {
		t.Fatalf("%+v %v", out, err)
	}
}

// Evidence stored under catalogue v1 (seven fields) still decodes, exports
// with the catalogue it was labelled under, and renders the same state text.
func TestLabelExportCatalogueV1Evidence(t *testing.T) {
	old := json.RawMessage(`{"co_fires":4,"a_leads_seconds":-95,"reverse_co_fires":0,"shared_service":true,"shared_group":false,"naming_signal":true,"common_upstream":false}`)
	var e PairEvidence
	if err := json.Unmarshal(old, &e); err != nil || e.CoFires != 4 || e.ALeadsSeconds != -95 || !e.SharedService {
		t.Fatalf("%+v %v", e, err)
	}
	d := SnapshotDecision{ID: "d1", Task: "dependency", Pair: &SnapshotPair{A: "a", B: "b"}, Evidence: old,
		Choices: map[string]string{"a_depends_b": "x", "b_depends_a": "y", "no_dependency": "z", "insufficient_evidence": "w", "shared_upstream": "v"}}
	rec := LabelRecord{SubjectType: "dependency_pair", SubjectKey: "a:b", GroupKey: "pair:a:b", Verdict: "confirmed",
		Request: DecisionSnapshot{V: 1, Catalogue: "probara-evidence-v1", Decisions: []SnapshotDecision{d}},
		Answers: map[string]LabelAnswer{"d1": {Label: "a_depends_b", Source: LabelSourceOperator}}}
	var buf bytes.Buffer
	rows, skipped, err := WriteLabelExport(&buf, fixtureSalt(), []LabelRecord{rec})
	if err != nil || rows != 1 || skipped != 0 {
		t.Fatalf("rows=%d skipped=%d %v", rows, skipped, err)
	}
	var row struct {
		State      string `json:"state"`
		Provenance struct {
			Catalogue string `json:"catalogue"`
		} `json:"provenance"`
	}
	if err := json.Unmarshal(buf.Bytes(), &row); err != nil {
		t.Fatal(err)
	}
	if row.Provenance.Catalogue != "probara-evidence-v1" || row.State != "Co-firing windows: 4. B precedes A by 95 seconds. Shared naming token: observed. Shared service tag: observed." {
		t.Fatalf("%s", buf.String())
	}
}

func TestPairEvidenceNumberedSiblingsAreNotVariants(t *testing.T) {
	in := DependencyInput{Monitors: []DependencyMonitorRef{
		{Ref: "a", Name: "prod-web-01", Type: "http", Tags: []string{"web"}},
		{Ref: "b", Name: "prod-web-02", Type: "http", Tags: []string{"web", "eu"}},
		{Ref: "c", Name: "callbot instance 1", Type: "sip"},
		{Ref: "d", Name: "callbot instance 2", Type: "sip"},
		{Ref: "e", Name: "node", Type: "tcp"},
		{Ref: "f", Name: "node-2", Type: "tcp"},
		{Ref: "g", Name: "node-2", Type: "http"},
		{Ref: "h", Name: "db", Type: "postgres"},
		{Ref: "i", Name: "db degraded", Type: "postgres"},
		{Ref: "j", Name: "db 2 replica", Type: "postgres"},
	}, CoFiringAlerts: []CoFiringPair{
		// Variants only matter once they co-fire; that also makes them candidates.
		{Earlier: "h", Later: "i", TimesCoFired: 2, MedianLeadSeconds: 30},
		{Earlier: "h", Later: "j", TimesCoFired: 2, MedianLeadSeconds: 30},
	}}
	// Numbered siblings of one type are peers whatever their tags.
	for _, p := range [][2]string{{"a", "b"}, {"c", "d"}, {"e", "f"}} {
		if e := evidenceFor(t, in, p[0], p[1]); !e.Peers || e.AExtendsB || e.BExtendsA {
			t.Errorf("%s/%s %+v", p[0], p[1], e)
		}
	}
	// A different type is not a peer, and "node-2" still does not extend "node".
	if e := evidenceFor(t, in, "e", "g"); e.Peers || e.BExtendsA {
		t.Errorf("e/g %+v", e)
	}
	// A word extension is a variant; one with a word after the number too.
	if e := evidenceFor(t, in, "h", "i"); !e.BExtendsA || e.Peers {
		t.Errorf("h/i %+v", e)
	}
	if e := evidenceFor(t, in, "h", "j"); !e.BExtendsA {
		t.Errorf("h/j %+v", e)
	}
}

func TestPairEvidenceNamePrefixMustBeRare(t *testing.T) {
	in := DependencyInput{Monitors: []DependencyMonitorRef{
		{Ref: "api", Name: "prod-eu-billing-api", Type: "http"},
		{Ref: "db", Name: "prod-eu-billing-db", Type: "postgres"},
	}}
	if e := evidenceFor(t, in, "api", "db"); e.SharedNamePrefix != 3 {
		t.Fatalf("rare prefix %+v", e)
	}
	// Eleven monitors carry "prod eu": the prefix is reported as 1 and no
	// longer qualifies the pair as structural.
	for i := 0; i < 9; i++ {
		in.Monitors = append(in.Monitors, DependencyMonitorRef{Ref: fmt.Sprintf("x%d", i), Name: fmt.Sprintf("prod eu other%d", i), Type: "http"})
	}
	e := evidenceFor(t, in, "api", "db")
	if e.SharedNamePrefix != 1 {
		t.Fatalf("common prefix %+v", e)
	}
	if basis, _ := DependencyGuard(e); basis != DependencyBasisNone {
		t.Fatalf("common prefix qualifies %s", basis)
	}
}

func TestPairEvidencePopulations(t *testing.T) {
	in := DependencyInput{Monitors: []DependencyMonitorRef{
		{Ref: "a", Name: "orders api", Type: "http", Tags: []string{"orders"}},
		{Ref: "b", Name: "orders store", Type: "postgres", Tags: []string{"orders"}},
	}}
	// Fallback: counted over the input's monitors.
	if e := evidenceFor(t, in, "a", "b"); e.SharedTagMonitors != 2 || e.SharedNamePrefix != 1 {
		t.Fatalf("fallback %+v", e)
	}
	// Passed-in tenant-wide populations win: the tag is common across the
	// tenant, so the pair is no longer a structural candidate.
	in.TagMonitors = map[string]int{"orders": 40}
	in.PrefixMonitors = map[string]int{}
	e := evidenceFor(t, in, "a", "b")
	if e.SharedTagMonitors != 40 {
		t.Fatalf("passed-in %+v", e)
	}
	if basis, _ := DependencyGuard(e); basis != DependencyBasisNone {
		t.Fatalf("common tag qualifies %s", basis)
	}
	// A two-token prefix population is passed in the same way.
	in.Monitors[1].Name = "orders api store"
	in.PrefixMonitors = map[string]int{"orders api": 12}
	if e := evidenceFor(t, in, "a", "b"); e.SharedNamePrefix != 1 {
		t.Fatalf("passed-in prefix %+v", e)
	}
	in.PrefixMonitors = map[string]int{"orders api": 3}
	if e := evidenceFor(t, in, "a", "b"); e.SharedNamePrefix != 2 {
		t.Fatalf("passed-in rare prefix %+v", e)
	}
	tags, prefixes := MonitorPopulations([]DependencyMonitorRef{{Name: "a-b-c", Tags: []string{"x", "x", "prod"}}, {Name: "A b", Tags: []string{"x"}}, {Name: "solo"}})
	if tags["x"] != 2 || tags["prod"] != 0 || prefixes["a b"] != 2 || len(prefixes) != 1 {
		t.Fatalf("populations %v %v", tags, prefixes)
	}
}

func TestPairEvidenceGroupsMatchByIdentity(t *testing.T) {
	in := DependencyInput{Monitors: []DependencyMonitorRef{
		{Ref: "a", Name: "alpha", Type: "http", Groups: []DependencyGroupRef{{"g1", 3}}},
		{Ref: "b", Name: "beta", Type: "http", Groups: []DependencyGroupRef{{"g2", 3}}},
		{Ref: "c", Name: "gamma", Type: "http", Groups: []DependencyGroupRef{{"g1", 3}, {"g3", 2}}},
	}}
	// a and b are in two different groups (which may share a name): no pair.
	if _, _, _, ok := PairDecisionFor(in, "a", "b"); ok {
		t.Fatal("different groups matched")
	}
	if e := evidenceFor(t, in, "a", "c"); !e.SharedGroup || e.SharedGroupMonitors != 3 {
		t.Fatalf("a/c %+v", e)
	}
}

// A base suggestion rejected by the cycle filter does not cost its variant.
func TestCycleFilterRunsBeforeVariantDedup(t *testing.T) {
	sent := 0
	srv := guardServer(t, &sent, func(map[string]string) string { return "a_depends_b" }, .95)
	defer srv.Close()
	advisor, _ := NewDependencyAdvisor(Config{BaseURL: srv.URL, APIKey: "test-internal-token"})
	in := DependencyInput{
		Monitors: []DependencyMonitorRef{
			{Ref: "app", Name: "reports-api", Type: "http", Tags: []string{"reports"}},
			{Ref: "db1", Name: "reports store", Type: "postgres", Tags: []string{"reports"}},
			{Ref: "db2", Name: "reports store replica lag", Type: "postgres", Tags: []string{"reports"}},
		},
		// db1 already depends on app, so app -> db1 would close a cycle.
		ExistingDependencies: []DependencyEdgeRef{{Monitor: "db1", DependsOn: "app"}},
	}
	out, err := advisor.SuggestDependencies(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if sent != 2 || len(out.Suggestions) != 1 || out.Suggestions[0].DependsOn != "db2" || out.Analyzed != out.Abstained+1 {
		t.Fatalf("sent %d %+v", sent, out)
	}
}
