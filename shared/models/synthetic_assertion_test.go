package models

import (
	"encoding/json"
	"testing"
)

func TestSyntheticAPIAssertionValueText(t *testing.T) {
	cases := map[string]string{
		`"123"`:                "123",
		`"  ok  "`:             "  ok  ",
		`123`:                  "123",
		`1.50`:                 "1.50",
		`12345678901234567890`: "12345678901234567890",
		`true`:                 "true",
		`{ "status" : "ok" }`:  `{"status":"ok"}`,
		`[1, 2]`:               "[1,2]",
		`null`:                 "null",
		``:                     "",
	}
	for raw, want := range cases {
		got := SyntheticAPIAssertionConfig{Value: json.RawMessage(raw)}.ValueText()
		if got != want {
			t.Errorf("ValueText(%s) = %q, want %q", raw, got, want)
		}
	}
}
