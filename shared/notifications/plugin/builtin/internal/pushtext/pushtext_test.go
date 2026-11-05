package pushtext

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/yassinebenameur/probara/shared/notifications/present"
)

func TestClip(t *testing.T) {
	if got := Clip("short", 10); got != "short" {
		t.Errorf("Clip short = %q", got)
	}
	got := Clip(strings.Repeat("é", 10), 8)
	if len(got) > 8 || !utf8.ValidString(got) || !strings.HasSuffix(got, "…") {
		t.Errorf("Clip multibyte = %q (%d bytes)", got, len(got))
	}
}

func TestBody_FitsAndKeepsSummary(t *testing.T) {
	m := present.Message{
		Summary:   "The monitor failed 3 consecutive checks.",
		Rows:      []present.Row{{Label: "Triggered", Value: "now"}, {Label: "Failures", Value: strings.Repeat("x", 300)}},
		LastError: strings.Repeat("e", 2000),
	}
	for _, limit := range []int{1024, 200, 60} {
		got := Body(m, limit)
		if len(got) > limit {
			t.Errorf("limit %d: body is %d bytes", limit, len(got))
		}
		if !strings.HasPrefix(got, "The monitor") {
			t.Errorf("limit %d: summary lost: %q", limit, got)
		}
	}
	if got := Body(m, 1024); !strings.Contains(got, "Triggered: now") || !strings.Contains(got, "Last error: eee") {
		t.Errorf("body dropped parts it had room for: %q", got)
	}
}
