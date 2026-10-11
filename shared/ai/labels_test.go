package ai

import (
	"errors"
	"reflect"
	"testing"
)

func rcaDecision(id string, e MonitorEvidence, final string) SnapshotDecision {
	return SnapshotDecision{ID: id, Task: "rca", MonitorRef: "ref-" + id, Evidence: mustMarshalEvidence(e), Choices: Choices("rca"), Predicted: final, Final: final}
}

func TestResolveIncidentAnswers(t *testing.T) {
	dns := MonitorEvidence{Checks: 10, Failures: 4, DNSFailures: 4}
	conn := MonitorEvidence{Checks: 10, Failures: 4, ConnectionFailures: 4}
	missing := MonitorEvidence{MissingChecks: true}
	snap := DecisionSnapshot{V: 1, Catalogue: CatalogueVersion, Hypothesis: "dns_failure", Decisions: []SnapshotDecision{
		rcaDecision("d1", dns, "dns_failure"),
		rcaDecision("d2", conn, "connection_failure"),
		rcaDecision("d3", missing, "insufficient_evidence"),
	}}

	t.Run("confirmed", func(t *testing.T) {
		for _, label := range []string{"", "dns_failure"} {
			answers, incident, err := ResolveIncidentAnswers(snap, "confirmed", label, nil)
			if err != nil || incident != "dns_failure" {
				t.Fatalf("%v %q", err, incident)
			}
			want := map[string]LabelAnswer{
				"d1": {"dns_failure", "confirmed"},
				"d2": {"connection_failure", "confirmed"},
				"d3": {"insufficient_evidence", "confirmed"},
			}
			if !reflect.DeepEqual(answers, want) {
				t.Fatalf("%+v", answers)
			}
		}
	})
	t.Run("corrected propagates only where supported", func(t *testing.T) {
		answers, incident, err := ResolveIncidentAnswers(snap, "corrected", "connection_failure", nil)
		if err != nil || incident != "connection_failure" {
			t.Fatalf("%v %q", err, incident)
		}
		want := map[string]LabelAnswer{
			"d1": {"insufficient_evidence", "propagated"},
			"d2": {"connection_failure", "propagated"},
			"d3": {"insufficient_evidence", "propagated"},
		}
		if !reflect.DeepEqual(answers, want) {
			t.Fatalf("%+v", answers)
		}
	})
	t.Run("overrides", func(t *testing.T) {
		answers, _, err := ResolveIncidentAnswers(snap, "corrected", "connection_failure", map[string]string{"d1": "dns_failure"})
		if err != nil || answers["d1"] != (LabelAnswer{"dns_failure", "operator"}) || answers["d2"].Source != "propagated" {
			t.Fatalf("%v %+v", err, answers)
		}
		answers, _, err = ResolveIncidentAnswers(snap, "confirmed", "", map[string]string{"d2": "insufficient_evidence"})
		if err != nil || answers["d2"] != (LabelAnswer{"insufficient_evidence", "operator"}) || answers["d1"].Source != "confirmed" {
			t.Fatalf("%v %+v", err, answers)
		}
	})
	t.Run("validation", func(t *testing.T) {
		cases := []struct {
			verdict, label string
			overrides      map[string]string
		}{
			{"confirmed", "tls_problem", nil},
			{"corrected", "", nil},
			{"corrected", "made_up", nil},
			{"rejected", "", nil},
			{"", "", nil},
			{"corrected", "dns_failure", map[string]string{"nope": "dns_failure"}},
			{"corrected", "dns_failure", map[string]string{"d1": "a_depends_b"}},
		}
		for _, c := range cases {
			_, _, err := ResolveIncidentAnswers(snap, c.verdict, c.label, c.overrides)
			var ve *LabelValidationError
			if !errors.As(err, &ve) {
				t.Fatalf("%+v: expected LabelValidationError, got %v", c, err)
			}
		}
	})
	t.Run("snapshot catalogue is authoritative", func(t *testing.T) {
		old := DecisionSnapshot{V: 1, Catalogue: "probara-evidence-v0", Hypothesis: "legacy_cause", Decisions: []SnapshotDecision{{
			ID: "d", Task: "rca", Evidence: mustMarshalEvidence(dns), Final: "legacy_cause",
			Choices: map[string]string{"legacy_cause": "x", "insufficient_evidence": "y"},
		}}}
		if _, incident, err := ResolveIncidentAnswers(old, "confirmed", "", nil); err != nil || incident != "legacy_cause" {
			t.Fatalf("%v %q", err, incident)
		}
		if _, _, err := ResolveIncidentAnswers(old, "corrected", "dns_failure", nil); err == nil {
			t.Fatal("a running-catalogue key absent from the snapshot was accepted")
		}
		answers, _, err := ResolveIncidentAnswers(old, "corrected", "insufficient_evidence", nil)
		if err != nil || answers["d"].Label != "insufficient_evidence" {
			t.Fatalf("%v %+v", err, answers)
		}
	})
	t.Run("unlabelable", func(t *testing.T) {
		if _, _, err := ResolveIncidentAnswers(DecisionSnapshot{}, "confirmed", "", nil); !errors.Is(err, ErrSnapshotNotLabelable) {
			t.Fatal(err)
		}
	})
}

func TestDependencyAnswer(t *testing.T) {
	d := PairSnapshotDecision("A", "B", PairEvidence{CoFires: 3, ALeadsSeconds: -30})
	cases := []struct {
		monitor, verdict, corrected, want string
	}{
		{"A", "confirmed", "", "a_depends_b"},
		{"B", "confirmed", "", "b_depends_a"},
		{"A", "rejected", "", "no_dependency"},
		{"B", "rejected", "no_dependency", "no_dependency"},
		{"A", "rejected", "reversed", "b_depends_a"},
		{"B", "rejected", "reversed", "a_depends_b"},
		{"A", "rejected", "shared_upstream", "shared_upstream"},
		{"B", "rejected", "insufficient_evidence", "insufficient_evidence"},
	}
	for _, c := range cases {
		got, err := DependencyAnswer(d, c.monitor, c.verdict, c.corrected)
		if err != nil || got != (LabelAnswer{c.want, "operator"}) {
			t.Fatalf("%+v: %+v %v", c, got, err)
		}
	}
	for _, c := range [][3]string{{"A", "confirmed", "reversed"}, {"A", "rejected", "bogus"}, {"A", "corrected", ""}, {"C", "confirmed", ""}} {
		var ve *LabelValidationError
		if _, err := DependencyAnswer(d, c[0], c[1], c[2]); !errors.As(err, &ve) {
			t.Fatalf("%v: expected LabelValidationError, got %v", c, err)
		}
	}
	if _, err := DependencyAnswer(rcaDecision("x", MonitorEvidence{}, "dns_failure"), "A", "confirmed", ""); err == nil {
		t.Fatal("rca decision accepted as a pair")
	}
}
