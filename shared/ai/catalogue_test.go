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
