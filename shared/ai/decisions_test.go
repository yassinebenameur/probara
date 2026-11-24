package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func decisionServer(t *testing.T, chosen string, mutate func(*BatchResult)) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-internal-token" {
			t.Error("missing auth")
		}
		var req struct {
			Decisions []Decision `json:"decisions"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
		}
		out := BatchResult{Model: ModelIdentity{Engine: "laya-cpu", Version: "fixture", SHA256: strings.Repeat("a", 64), Catalogue: CatalogueVersion}, Thresholds: map[string]float64{"rca": .9, "dependency": .9}}
		for _, d := range req.Decisions {
			p := map[string]float64{}
			for k := range d.Choices {
				p[k] = 0
			}
			p[chosen] = 1
			out.Results = append(out.Results, DecisionResult{ID: d.ID, Probabilities: p})
		}
		if mutate != nil {
			mutate(&out)
		}
		json.NewEncoder(w).Encode(out)
	}))
}
func TestDecisionProbabilityValidation(t *testing.T) {
	for name, mutate := range map[string]func(*BatchResult){
		"wrong id":       func(b *BatchResult) { b.Results[0].ID = "unexpected" },
		"negative":       func(b *BatchResult) { b.Results[0].Probabilities["dns_failure"] = -1 },
		"sum":            func(b *BatchResult) { b.Results[0].Probabilities["dns_failure"] = .8 },
		"unknown":        func(b *BatchResult) { b.Results[0].Probabilities["made_up"] = .1 },
		"missing":        func(b *BatchResult) { delete(b.Results[0].Probabilities, "insufficient_evidence") },
		"threshold":      func(b *BatchResult) { b.Thresholds["rca"] = -.1 },
		"zero threshold": func(b *BatchResult) { b.Thresholds["rca"] = 0 },
		"high threshold": func(b *BatchResult) { b.Thresholds["rca"] = 1.1 },
		"model":          func(b *BatchResult) { b.Model.Engine = "external" },
	} {
		t.Run(name, func(t *testing.T) {
			srv := decisionServer(t, "dns_failure", mutate)
			defer srv.Close()
			a, _ := NewAnalyzer(Config{BaseURL: srv.URL, APIKey: "test-internal-token"})
			_, err := a.(*localAnalyzer).decide(context.Background(), []Decision{{ID: "d1", Task: "rca", Evidence: MonitorEvidence{}, Choices: choices("rca")}})
			if err == nil {
				t.Fatal("malformed decision accepted")
			}
		})
	}
}
func TestRCARendersOnlyMeasuredFacts(t *testing.T) {
	srv := decisionServer(t, "dns_failure", func(b *BatchResult) {
		b.Thresholds["rca"] = .35
		b.Results[0].Probabilities["dns_failure"] = .4
		b.Results[0].Probabilities["connection_failure"] = .3
		b.Results[0].Probabilities["insufficient_evidence"] = .3
	})
	defer srv.Close()
	a, _ := NewAnalyzer(Config{BaseURL: srv.URL, APIKey: "test-internal-token"})
	e := MonitorEvidence{Checks: 10, Failures: 4, DNSFailures: 4}
	result, err := a.Analyze(context.Background(), AnalysisInput{Incident: IncidentContext{Summary: "credential=super-secret"}, Monitors: []MonitorContext{{Ref: "m1", Name: "dns", Affected: true, Summary: e}}})
	if err != nil {
		t.Fatal(err)
	}
	if result.Metadata.Hypothesis != "dns_failure" || !strings.Contains(result.Metadata.Evidence[0].Fact, "4 of 10") {
		t.Fatalf("%+v", result)
	}
	if result.Confidence != "low" || result.Metadata.Score != .4 {
		t.Fatalf("calibrated score overstated: %+v", result)
	}
	if strings.Contains(result.Summary, "super-secret") {
		t.Fatal("untrusted text rendered")
	}
	// A confident model decision cannot fabricate missing DNS evidence.
	result, err = a.Analyze(context.Background(), AnalysisInput{Monitors: []MonitorContext{{Ref: "m1", Name: "app", Affected: true, Summary: MonitorEvidence{Checks: 10, Failures: 4}}}})
	if err != nil || result.Metadata.Outcome != "insufficient_evidence" {
		t.Fatalf("%+v %v", result, err)
	}
}
func TestDependencyDirectionsAndCycles(t *testing.T) {
	srv := decisionServer(t, "b_depends_a", nil)
	defer srv.Close()
	advisor, _ := NewDependencyAdvisor(Config{BaseURL: srv.URL, APIKey: "test-internal-token"})
	// Two services (no backing resource, so no structural basis) sharing a
	// rare tag; differing tag sets keep them from being same-role peers.
	in := DependencyInput{Monitors: []DependencyMonitorRef{{Ref: "a", Name: "billing-gateway", Type: "http", Tags: []string{"billing", "edge"}}, {Ref: "b", Name: "billing-api", Type: "http", Tags: []string{"billing"}}}, CoFiringAlerts: []CoFiringPair{{Earlier: "a", Later: "b", TimesCoFired: 5, MedianLeadSeconds: 20, WindowMonitors: 2}}}
	out, err := advisor.SuggestDependencies(context.Background(), in)
	if err != nil || len(out.Suggestions) != 1 || out.Suggestions[0].Monitor != "b" || out.Suggestions[0].Basis != DependencyBasisTiming {
		t.Fatalf("%+v %v", out, err)
	}
	in.ExistingDependencies = []DependencyEdgeRef{{Monitor: "a", DependsOn: "outside"}, {Monitor: "outside", DependsOn: "b"}}
	out, err = advisor.SuggestDependencies(context.Background(), in)
	if err != nil || len(out.Suggestions) != 0 {
		t.Fatalf("cycle accepted %+v %v", out, err)
	}
	in.ExistingDependencies = nil
	in.CoFiringAlerts[0].ReverseCoFires = 2
	out, err = advisor.SuggestDependencies(context.Background(), in)
	if err != nil || len(out.Suggestions) != 0 {
		t.Fatal("conflicting direction accepted")
	}
	in.CoFiringAlerts = nil
	out, err = advisor.SuggestDependencies(context.Background(), in)
	if err != nil || len(out.Suggestions) != 0 {
		t.Fatal("naming-only proposal accepted")
	}
}
func TestCandidateBudget(t *testing.T) {
	in := DependencyInput{}
	for i := 0; i < 60; i++ {
		tag := fmt.Sprintf("svc-%d", i)
		in.Monitors = append(in.Monitors,
			DependencyMonitorRef{Ref: fmt.Sprintf("s%03d", i), Name: fmt.Sprintf("service %d", i), Type: "http", Tags: []string{tag}},
			DependencyMonitorRef{Ref: fmt.Sprintf("r%03d", i), Name: fmt.Sprintf("store %d", i), Type: "postgres", Tags: []string{tag}})
	}
	pairs := CandidatePairs(in)
	if len(pairs) != 50 {
		t.Fatalf("%d", len(pairs))
	}
	for _, p := range pairs {
		if p.basis != DependencyBasisStructural {
			t.Fatalf("%+v", p)
		}
	}
}
func TestFullWindowSummaryAndAllowlist(t *testing.T) {
	baseline := 100
	slow := 900
	checks := []CheckContext{{Status: "success", LatencyMS: &baseline, CheckedAt: time.Now()}, {Status: "failure", ErrorMessage: "status_code: got 503 secret=token", LatencyMS: &slow, Metrics: json.RawMessage(`{"http":{"timing":{"dns_ms":9},"display_values":[{"value":"secret"}],"final_url":"https://user:password@host"}}`)}}
	e := SummarizeChecks(checks)
	if e.ProtocolFailures != 1 || e.SlowChecks != 1 || e.MaxDNSMS != 9 {
		t.Fatalf("%+v", e)
	}
	sample, _ := SampleTransitions(append(checks, checks...), 2)
	b, _ := json.Marshal(sample)
	if strings.Contains(string(b), "secret") || strings.Contains(string(b), "password") {
		t.Fatal("secret escaped sample allowlist")
	}
}

// Certificate facts come from wherever shared/tlscert locates the
// certificate, so a SIP-over-TLS monitor's expired or replaced certificate
// is evidence like an HTTPS one's.
func TestSummarizeChecksCertificateFromAnyEnvelope(t *testing.T) {
	now := time.Now().UTC()
	expired := now.Add(-time.Hour).Format(time.RFC3339)
	valid := now.Add(48 * time.Hour).Format(time.RFC3339)
	sip := func(notAfter, serial string) json.RawMessage {
		return json.RawMessage(`{"sip":{"method":"OPTIONS","tls":{"not_after":"` + notAfter + `","serial_number":"` + serial + `"}}}`)
	}
	e := SummarizeChecks([]CheckContext{
		{Status: "success", CheckedAt: now.Add(-time.Minute), Metrics: sip(valid, "1")},
		{Status: "success", CheckedAt: now, Metrics: sip(expired, "2")},
	})
	if !e.CertificateExpired || !e.CertificateChanged {
		t.Fatalf("sip certificate evidence = %+v, want expired and changed", e)
	}
	e = SummarizeChecks([]CheckContext{{Status: "success", CheckedAt: now, Metrics: json.RawMessage(`{"http":{"tls":{"not_after":"` + expired + `"}}}`)}})
	if !e.CertificateExpired {
		t.Fatalf("http certificate evidence = %+v, want expired", e)
	}
	// An expired intermediate counts although the leaf is valid: the chain's
	// earliest expiry is the one the tls_expiry alert reports.
	chain := json.RawMessage(`{"tcp":{"tls":{"not_after":"` + valid + `","chain_not_after":"` + expired + `"}}}`)
	if e = SummarizeChecks([]CheckContext{{Status: "success", CheckedAt: now, Metrics: chain}}); !e.CertificateExpired {
		t.Fatalf("chain certificate evidence = %+v, want expired", e)
	}
}
func TestDisabledAndCancellation(t *testing.T) {
	if _, err := NewAnalyzer(Config{Disabled: true}); err != ErrNotConfigured {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer srv.Close()
	a, _ := NewAnalyzer(Config{BaseURL: srv.URL, APIKey: "test-internal-token"})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := a.(*localAnalyzer).decide(ctx, []Decision{{ID: "d1", Task: "rca", Choices: choices("rca")}})
	if err == nil {
		t.Fatal("cancelled request accepted")
	}
}
