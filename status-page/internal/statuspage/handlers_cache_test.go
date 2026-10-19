package statuspage

import (
	"testing"
)

func TestEtagMatches(t *testing.T) {
	etag := `"abc123"`
	cases := []struct {
		ifNoneMatch string
		want        bool
	}{
		{"", false},
		{`"abc123"`, true},
		{`"other"`, false},
		{"*", true},
		{`"first", "abc123"`, true},
		{`"first", "second"`, false},
		{`W/"abc123"`, true},
		{`"first" , "abc123" `, true},
		{"abc123", false}, // unquoted token is not the same validator
	}
	for _, tc := range cases {
		if got := etagMatches(tc.ifNoneMatch, etag); got != tc.want {
			t.Errorf("etagMatches(%q, %q) = %v, want %v", tc.ifNoneMatch, etag, got, tc.want)
		}
	}
	if etagMatches("*", "") {
		t.Error(`etagMatches("*", "") = true, want false (no etag to match)`)
	}
}
