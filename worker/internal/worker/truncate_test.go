package worker

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestTruncateRunesNeverSplitsACharacter(t *testing.T) {
	if got := truncateRunes("short", 80); got != "short" {
		t.Errorf("short input changed: %q", got)
	}
	ascii := strings.Repeat("a", 81)
	if got := truncateRunes(ascii, 80); got != strings.Repeat("a", 80)+"…" {
		t.Errorf("ascii cut = %q", got)
	}
	// 79 ASCII bytes then two-byte characters: a byte cut at 80 lands mid-rune.
	multi := strings.Repeat("a", 79) + strings.Repeat("é", 10)
	got := truncateRunes(multi, 80)
	if !utf8.ValidString(got) {
		t.Fatalf("cut produced invalid UTF-8: %q", got)
	}
	if want := strings.Repeat("a", 79) + "é…"; got != want {
		t.Errorf("multi-byte cut = %q, want %q", got, want)
	}
	// Under the limit in characters though over it in bytes: unchanged.
	wide := strings.Repeat("é", 50)
	if got := truncateRunes(wide, 80); got != wide {
		t.Errorf("50 two-byte characters were cut at limit 80: %q", got)
	}
}
