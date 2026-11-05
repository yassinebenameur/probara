package monitorstate

import (
	"encoding/json"
	"testing"
)

func TestPGSafeJSON(t *testing.T) {
	cases := []struct{ in, want string }{
		{`{"a":"plain"}`, `{"a":"plain"}`},
		{`{"a":"x\u0000y"}`, `{"a":"x\ufffdy"}`},
		{`{"a\u0000":"\u0000"}`, `{"a\ufffd":"\ufffd"}`},
		// An escaped backslash before "u0000" is literal text, not a NUL.
		{`{"a":"\\u0000"}`, `{"a":"\\u0000"}`},
		{`{"a":"\\\u0000"}`, `{"a":"\\\ufffd"}`},
		{`{"a":"\"\u0000"}`, `{"a":"\"\ufffd"}`},
		// Escapes Go writes for HTML characters stay.
		{`{"a":"\u003c\u0026"}`, `{"a":"\u003c\u0026"}`},
		// A surrogate pair stays; lone halves do not.
		{`{"a":"\ud83d\ude00"}`, `{"a":"\ud83d\ude00"}`},
		{`{"a":"\ud83dx","b":"\ude00","c":"\uD800\u0041"}`, `{"a":"\ufffdx","b":"\ufffd","c":"\ufffd\u0041"}`},
		// Outside strings nothing is rewritten.
		{`{"u0000":1}`, `{"u0000":1}`},
		{"{\"a\":\"ok\xffok\"}", `{"a":"ok` + "\uFFFD" + `ok"}`},
	}
	for _, tc := range cases {
		got := pgSafeJSON([]byte(tc.in))
		if string(got) != tc.want {
			t.Errorf("pgSafeJSON(%s) = %s, want %s", tc.in, got, tc.want)
		}
		if !json.Valid(got) {
			t.Errorf("pgSafeJSON(%s) is not valid JSON: %s", tc.in, got)
		}
	}

	// A clean document is passed through without a copy.
	clean := []byte(`{"a":"\u003c"}`)
	if got := pgSafeJSON(clean); &got[0] != &clean[0] {
		t.Error("clean document was copied")
	}
	// The caller's bytes are never rewritten in place.
	dirty := []byte(`{"a":"\u0000"}`)
	_ = pgSafeJSON(dirty)
	if string(dirty) != `{"a":"\u0000"}` {
		t.Errorf("input mutated: %s", dirty)
	}
}

func TestPGSafeText(t *testing.T) {
	if pgSafeText(nil) != nil {
		t.Error("nil must stay nil")
	}
	clean := "fine"
	if got := pgSafeText(&clean); got != &clean {
		t.Error("clean text was copied")
	}
	dirty := "a\x00b\xffc"
	if got := pgSafeText(&dirty); *got != "a\uFFFDb\uFFFDc" {
		t.Errorf("pgSafeText = %q", *got)
	}
}
