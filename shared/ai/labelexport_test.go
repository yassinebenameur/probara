package ai

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

var updateFixture = flag.Bool("update", false, "rewrite shared/ai/testdata golden files")

// Golden strings produced by ai-service probara_ai/contract.py state_text for
// the same evidence.
func TestStateTextMatchesContract(t *testing.T) {
	cases := []struct{ task, evidence, want string }{
		{"rca", `{"checks":12,"failures":5,"dns_failures":5,"connection_failures":0,"tls_failures":0,"protocol_failures":0,"slow_checks":0,"baseline_ms":84,"max_latency_ms":2210,"max_dns_ms":812,"max_connect_ms":0,"max_tls_ms":0,"max_ttfb_ms":0,"certificate_expired":false,"certificate_changed":false,"resource_pressure":false,"host_metrics_available":false,"upstream_failed_before":false,"maintenance_overlap":false,"conflict":false,"missing_checks":false}`,
			"Checks: 12. Failed observations: 5. baseline ms: 84. dns failures: 5. max dns ms: 812. max latency ms: 2210."},
		{"rca", `{"checks":0,"failures":0,"missing_checks":true}`,
			"Checks: 0. Failed observations: 0. missing checks: observed."},
		{"rca", `{"checks":10,"failures":4,"dns_failures":0,"connection_failures":1,"tls_failures":4,"protocol_failures":3,"slow_checks":2,"baseline_ms":0,"max_latency_ms":0,"max_dns_ms":0,"max_connect_ms":7,"max_tls_ms":950,"max_ttfb_ms":9,"certificate_expired":true,"certificate_changed":true,"resource_pressure":true,"host_metrics_available":true,"upstream_failed_before":true,"maintenance_overlap":true,"conflict":true,"missing_checks":false}`,
			"Checks: 10. Failed observations: 4. certificate changed: observed. certificate expired: observed. conflict: observed. connection failures: 1. host metrics available: observed. maintenance overlap: observed. max connect ms: 7. max tls ms: 950. max ttfb ms: 9. protocol failures: 3. resource pressure: observed. slow checks: 2. tls failures: 4. upstream failed before: observed."},
		{"dependency", `{"co_fires":4,"a_leads_seconds":-95,"reverse_co_fires":0,"shared_service":true,"shared_group":false,"naming_signal":true,"common_upstream":false}`,
			"Co-firing windows: 4. B precedes A by 95 seconds. Shared naming token: observed. Shared service tag: observed."},
		{"dependency", `{"co_fires":2,"a_leads_seconds":30,"reverse_co_fires":1,"shared_service":false,"shared_group":true,"naming_signal":false,"common_upstream":false}`,
			"Co-firing windows: 2. A precedes B by 30 seconds. Reverse-direction windows: 1. Shared group: observed."},
		{"dependency", `{"co_fires":3,"a_leads_seconds":0,"reverse_co_fires":0,"shared_service":true,"shared_group":false,"naming_signal":false,"common_upstream":true}`,
			"Co-firing windows: 3. No clear time direction. Known common upstream: observed. Shared service tag: observed."},
		{"dependency", `{}`, "Co-firing windows: 0. No clear time direction."},
	}
	for _, c := range cases {
		got, err := StateText(c.task, json.RawMessage(c.evidence))
		if err != nil || got != c.want {
			t.Errorf("StateText(%s, %s)\n got %q %v\nwant %q", c.task, c.evidence, got, err, c.want)
		}
	}
}

func TestStateTextValidation(t *testing.T) {
	for _, c := range []struct{ task, evidence string }{
		{"rca", `{"checks":3,"failures":4}`},
		{"rca", `{"checks":3,"failures":1,"dns_failures":-1}`},
		{"rca", `{"checks":300000000,"failures":1}`},
		{"rca", `{"checks":3,"failures":1,"hostname":1}`},
		{"rca", `{"failures":0}`},
		{"rca", `{"checks":3,"failures":1,"conflict":1}`},
		{"rca", `{"checks":3,"failures":1.5}`},
		{"rca", `{"checks":3,"failures":null}`},
		{"rca", `[]`},
		{"dependency", `{"co_fires":-1}`},
		{"dependency", `{"a_leads_seconds":-100000001}`},
		{"dependency", `{"co_fires":1} {}`},
		{"triage", `{}`},
	} {
		if s, err := StateText(c.task, json.RawMessage(c.evidence)); err == nil {
			t.Errorf("StateText(%s, %s) accepted: %q", c.task, c.evidence, s)
		}
	}
	// Leads may be negative within the bound.
	if _, err := StateText("dependency", json.RawMessage(`{"a_leads_seconds":-100000000}`)); err != nil {
		t.Fatal(err)
	}
}

func fixtureSalt() []byte {
	salt := make([]byte, 32)
	for i := range salt {
		salt[i] = byte(i)
	}
	return salt
}

const fixtureSHA = "3f9a0c7e5b1d2a4c6e8f0a1b3c5d7e9f2a4b6c8d0e1f3a5b7c9d1e3f5a7b9c0d"

// fixtureRecords builds the labels behind testdata/label-export-v1.jsonl
// through the same resolution code the API uses.
func fixtureRecords(t *testing.T) []LabelRecord {
	t.Helper()
	at := time.Date(2026, 10, 11, 9, 30, 0, 0, time.UTC)
	model := ModelIdentity{Engine: "laya-cpu", Version: "probara-laya-v1", SHA256: fixtureSHA, Catalogue: CatalogueVersion}
	var records []LabelRecord

	// 1. Corrected incident: one DNS override, one missing-checks decision
	//    propagated to insufficient_evidence.
	d1 := rcaDecision("0b6c1f9e-3d2a-4e5f-8a7b-6c5d4e3f2a10", MonitorEvidence{Checks: 12, Failures: 5, DNSFailures: 5, BaselineMS: 84, MaxLatencyMS: 2210, MaxDNSMS: 812}, "insufficient_evidence")
	d1.MonitorRef = "1f2e3d4c-5b6a-4978-8695-a4b3c2d1e0f9"
	d1.Predicted = "connection_failure"
	d1.Probabilities = map[string]float64{"connection_failure": .58, "dns_failure": .3, "insufficient_evidence": .12}
	d2 := rcaDecision("c41f7a2b-9e8d-4c6b-a5f4-e3d2c1b0a998", MonitorEvidence{MissingChecks: true}, "insufficient_evidence")
	d2.MonitorRef = "77aa66bb-55cc-44dd-83ee-22ff11009988"
	d2.Probabilities = map[string]float64{"insufficient_evidence": .9, "dns_failure": .1}
	snap := DecisionSnapshot{V: 1, Catalogue: CatalogueVersion, Model: model, Thresholds: map[string]float64{"rca": .6}, Hypothesis: "insufficient_evidence", Decisions: []SnapshotDecision{d1, d2}}
	answers, _, err := ResolveIncidentAnswers(snap, "corrected", "dns_failure", map[string]string{d1.ID: "dns_failure"})
	if err != nil {
		t.Fatal(err)
	}
	records = append(records, LabelRecord{SubjectType: "incident_analysis", SubjectKey: "6a1d0c2e-8b4f-4a39-9d7e-5c3b2a1f0e4d", GroupKey: "incident:9e8d7c6b-5a49-4382-a1b0-c9d8e7f6a5b4", Verdict: "corrected", Request: snap.WithoutPredictions(), Predicted: SnapshotPredictions(snap).AsSnapshot(), Answers: answers, LabeledAt: at})

	// 2. Confirmed single-decision TLS incident.
	d3 := rcaDecision("5d4c3b2a-1f0e-4d9c-8b7a-695847362514", MonitorEvidence{Checks: 10, Failures: 4, TLSFailures: 4, MaxTLSMS: 950, CertificateExpired: true}, "tls_problem")
	d3.MonitorRef = "aa11bb22-cc33-4d44-9e55-ff6600778899"
	d3.Probabilities = map[string]float64{"tls_problem": .93, "insufficient_evidence": .07}
	snap = DecisionSnapshot{V: 1, Catalogue: CatalogueVersion, Model: model, Thresholds: map[string]float64{"rca": .6}, Hypothesis: "tls_problem", Score: .93, Decisions: []SnapshotDecision{d3}}
	answers, _, err = ResolveIncidentAnswers(snap, "confirmed", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	records = append(records, LabelRecord{SubjectType: "incident_analysis", SubjectKey: "2b3c4d5e-6f70-4812-9a3b-4c5d6e7f8091", GroupKey: "incident:0a1b2c3d-4e5f-4607-8819-2a3b4c5d6e7f", Verdict: "confirmed", Request: snap.WithoutPredictions(), Predicted: SnapshotPredictions(snap).AsSnapshot(), Answers: answers, LabeledAt: at.Add(time.Minute)})

	pair := func(id, a, b string, e PairEvidence, monitor, verdict, corrected string, at time.Time) LabelRecord {
		d := PairSnapshotDecision(a, b, e)
		d.ID = id
		ans, err := DependencyAnswer(d, monitor, verdict, corrected)
		if err != nil {
			t.Fatal(err)
		}
		return LabelRecord{SubjectType: "dependency_pair", SubjectKey: a + ":" + b, GroupKey: "pair:" + a + ":" + b, Verdict: verdict,
			Request: DecisionSnapshot{V: 1, Catalogue: CatalogueVersion, Decisions: []SnapshotDecision{d}}, Answers: map[string]LabelAnswer{d.ID: ans}, LabeledAt: at}
	}
	// 3. Pair confirmed a_depends_b (B fails first).
	records = append(records, pair("e1d2c3b4-a596-4877-9869-5a4b3c2d1e0f", "10000000-0000-4000-8000-000000000001", "20000000-0000-4000-8000-000000000002",
		PairEvidence{CoFires: 4, ALeadsSeconds: -95, SharedService: true, NamingSignal: true}, "10000000-0000-4000-8000-000000000001", "confirmed", "", at.Add(2*time.Minute)))
	// 4. Pair rejected: no dependency.
	records = append(records, pair("f2e3d4c5-b6a7-4988-a97a-6b5c4d3e2f10", "30000000-0000-4000-8000-000000000003", "40000000-0000-4000-8000-000000000004",
		PairEvidence{CoFires: 2, ALeadsSeconds: 30, ReverseCoFires: 1, SharedGroup: true}, "40000000-0000-4000-8000-000000000004", "rejected", "", at.Add(3*time.Minute)))
	// 5. Pair rejected and corrected to shared_upstream.
	records = append(records, pair("a3b4c5d6-e7f8-4a09-8b1c-2d3e4f5a6b7c", "50000000-0000-4000-8000-000000000005", "60000000-0000-4000-8000-000000000006",
		PairEvidence{CoFires: 3, SharedService: true, CommonUpstream: true}, "50000000-0000-4000-8000-000000000005", "rejected", "shared_upstream", at.Add(4*time.Minute)))

	// 6. Invalid evidence (failures > checks): skipped, absent from the fixture.
	bad := rcaDecision("b4c5d6e7-f809-4b1a-9c2d-3e4f5a6b7c8d", MonitorEvidence{Checks: 2, Failures: 5}, "insufficient_evidence")
	records = append(records, LabelRecord{SubjectType: "incident_analysis", SubjectKey: "c5d6e7f8-091a-4b2c-8d3e-4f5a6b7c8d9e", GroupKey: "incident:d6e7f809-1a2b-4c3d-9e4f-5a6b7c8d9e0f", Verdict: "confirmed",
		Request: DecisionSnapshot{V: 1, Catalogue: CatalogueVersion, Model: model, Decisions: []SnapshotDecision{bad}}, Answers: map[string]LabelAnswer{bad.ID: {"insufficient_evidence", "confirmed"}}, LabeledAt: at.Add(5 * time.Minute)})
	return records
}

func TestLabelExportFixture(t *testing.T) {
	var buf bytes.Buffer
	rows, skipped, err := WriteLabelExport(&buf, fixtureSalt(), fixtureRecords(t))
	if err != nil {
		t.Fatal(err)
	}
	if rows != 6 || skipped != 1 {
		t.Fatalf("rows=%d skipped=%d, want 6/1", rows, skipped)
	}
	path := filepath.Join("testdata", "label-export-v1.jsonl")
	if *updateFixture {
		if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (regenerate with go test ./shared/ai -run TestLabelExportFixture -update)", err)
	}
	if !bytes.Equal(buf.Bytes(), want) {
		t.Fatalf("export drifted from %s (regenerate with -update and update the ai-service side):\n%s", path, buf.String())
	}
	lines := strings.Split(strings.TrimSuffix(buf.String(), "\n"), "\n")
	if len(lines) != 6 {
		t.Fatalf("%d lines", len(lines))
	}
	for _, line := range lines {
		var row map[string]any
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(line, `"failures":5,"dns_failures":0`) {
			t.Fatal("invalid-evidence record exported")
		}
	}
}

var uuidRE = regexp.MustCompile(`[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}`)

func TestLabelExportRedaction(t *testing.T) {
	records := fixtureRecords(t)
	var a, b, c bytes.Buffer
	if _, _, err := WriteLabelExport(&a, fixtureSalt(), records); err != nil {
		t.Fatal(err)
	}
	if _, _, err := WriteLabelExport(&b, fixtureSalt(), records); err != nil {
		t.Fatal(err)
	}
	other := fixtureSalt()
	other[0] = 0xff
	if _, _, err := WriteLabelExport(&c, other, records); err != nil {
		t.Fatal(err)
	}
	if m := uuidRE.FindString(a.String()); m != "" {
		t.Fatalf("UUID %s leaked into the export", m)
	}
	for _, key := range []string{`"monitor_ref"`, `"pair"`, `"id"`, `"incident_id"`, `"labeled_by"`, `"tenant`} {
		if strings.Contains(a.String(), key) {
			t.Fatalf("%s leaked into the export", key)
		}
	}
	if a.String() != b.String() {
		t.Fatal("export is not stable for the same salt")
	}
	groups := func(s string) []string {
		var out []string
		for _, line := range strings.Split(strings.TrimSpace(s), "\n") {
			var row struct {
				Group      string `json:"group"`
				Provenance struct {
					Row string `json:"row"`
				} `json:"provenance"`
			}
			_ = json.Unmarshal([]byte(line), &row)
			if !strings.HasPrefix(row.Group, "op-") || !strings.HasPrefix(row.Provenance.Row, "op-") {
				t.Fatalf("ids must carry the op- prefix: %s", line)
			}
			out = append(out, row.Group, row.Provenance.Row)
		}
		return out
	}
	ga, gc := groups(a.String()), groups(c.String())
	// Both decisions of the corrected incident share its group; rows differ.
	if ga[0] != ga[2] || ga[1] == ga[3] {
		t.Fatalf("incident decisions must share a group but not a row id: %v", ga[:4])
	}
	for i := range ga {
		if ga[i] == gc[i] {
			t.Fatalf("id %s identical under a different salt", ga[i])
		}
	}
	// Re-labelling (same subject, new verdict) keeps the row id stable.
	relabeled := append([]LabelRecord{}, records[2:3]...)
	relabeled[0].Verdict = "rejected"
	var r bytes.Buffer
	if _, _, err := WriteLabelExport(&r, fixtureSalt(), relabeled); err != nil {
		t.Fatal(err)
	}
	if gr := groups(r.String()); gr[1] != ga[7] {
		t.Fatal("row id changed on re-label")
	}
}
