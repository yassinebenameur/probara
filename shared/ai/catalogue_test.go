package ai

import (
	"reflect"
	"testing"
)

func TestMetadataListsEachIncompleteNoteOnce(t *testing.T) {
	got := metadata(ModelIdentity{Engine: "laya-cpu"}, "insufficient_evidence", 0, nil,
		[]string{"m1: missing checks", "m2: missing checks", "m1: missing checks"}).Incomplete
	if want := []string{"m1: missing checks", "m2: missing checks"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("incomplete = %q, want %q", got, want)
	}
	if empty := metadata(ModelIdentity{}, "insufficient_evidence", 0, nil, nil).Incomplete; empty == nil || len(empty) != 0 {
		t.Fatalf("nil notes must serialize as [], got %#v", empty)
	}
}

func TestNamedCausePredicates(t *testing.T) {
	if !IsNamedCause("dns_failure") {
		t.Fatal("dns_failure must be a named cause")
	}
	for _, key := range []string{RCAInsufficientEvidence, "", "not_in_catalogue"} {
		if IsNamedCause(key) {
			t.Fatalf("IsNamedCause(%q) = true", key)
		}
		if _, _, ok := RCAChoice(key); ok {
			t.Fatalf("RCAChoice(%q) ok = true", key)
		}
	}
	names := NamedCauses()
	if len(names) != len(catalogue.RCA)-1 {
		t.Fatalf("NamedCauses = %v, want every rca key but the abstention", names)
	}
	for _, k := range names {
		if !IsNamedCause(k) {
			t.Fatalf("NamedCauses lists %q", k)
		}
	}

	title, actions, ok := RCAChoice("dns_failure")
	if !ok || title != catalogue.RCA["dns_failure"].Title || len(actions) == 0 {
		t.Fatalf("RCAChoice(dns_failure) = %q %v %v", title, actions, ok)
	}
	actions[0] = "mutated"
	if _, again, _ := RCAChoice("dns_failure"); again[0] == "mutated" {
		t.Fatal("RCAChoice must return a copy of the catalogue's actions")
	}

	for level, want := range map[string]bool{"high": true, "medium": true, "low": false, "": false} {
		if IsConfident(level) != want {
			t.Fatalf("IsConfident(%q) != %v", level, want)
		}
	}
	for _, level := range ConfidentLevels() {
		if !IsConfident(level) {
			t.Fatalf("ConfidentLevels lists %q", level)
		}
	}
}
