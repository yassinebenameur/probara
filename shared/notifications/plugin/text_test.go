package plugin

import (
	"testing"
	"unicode/utf8"
)

func TestTruncate(t *testing.T) {
	for _, tc := range []struct {
		in   string
		n    int
		want string
	}{
		{"short", 10, "short"},
		{"exactly10!", 10, "exactly10!"},
		{"this is too long", 10, "this is t…"},
		{"héllo wörld", 6, "héllo…"},
		{"日本語のテキスト", 4, "日本語…"},
		{"x", 1, "x"},
		{"xy", 1, "…"},
		{"anything", 0, ""},
	} {
		got := Truncate(tc.in, tc.n)
		if got != tc.want {
			t.Errorf("Truncate(%q, %d) = %q, want %q", tc.in, tc.n, got, tc.want)
		}
		if !utf8.ValidString(got) {
			t.Errorf("Truncate(%q, %d) = %q is not valid UTF-8", tc.in, tc.n, got)
		}
	}
}
