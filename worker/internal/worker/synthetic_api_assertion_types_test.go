package worker

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/yassinebenameur/probara/shared/models"
)

// TestSyntheticAssertionValueTypes pins how an assertion's JSON value type
// decides the comparison. Text operators (every header/body op, json
// contains/regex) compare against the value's text: a JSON string's content,
// any other value's literal JSON text — never Go's rendering of the decoded
// value (map[status:ok], 1e+21). JSON equals compares by the value's type: a
// string compares text (a number's text matches it, as before), and a
// number, boolean or null needs a JSON value that can be that type — never a
// missing path or an unrelated string coerced to 0 or false.
func TestSyntheticAssertionValueTypes(t *testing.T) {
	body := `{"id":"123","n":123,"flag":true,"s_true":"true","zero":0,"nul":null,"txt":"abc","big":12345678901234567890,"obj":{"a":1},"arr":[1,2]}`
	resp := &http.Response{StatusCode: 200, Header: http.Header{"X-Version": []string{"1.50"}}}

	cases := []struct {
		name   string
		target string
		path   string
		op     string
		value  string // raw JSON
		pass   bool
	}{
		// json equals: string expected compares text.
		{"json string equals string", "json", "id", "equals", `"123"`, true},
		{"json string equals number text", "json", "n", "equals", `"123"`, true},
		{"json string equals true text", "json", "flag", "equals", `"true"`, true},
		{"json string never equals missing", "json", "missing", "equals", `""`, false},
		// json equals: number and boolean need a value of that type (or, for
		// stored assertions the old form coerced, a string that parses as it).
		{"json number equals number", "json", "n", "equals", `123`, true},
		{"json number equals numeric string (legacy)", "json", "id", "equals", `123`, true},
		{"json zero never equals missing", "json", "missing", "equals", `0`, false},
		{"json zero never equals null", "json", "nul", "equals", `0`, false},
		{"json zero never equals non-numeric string", "json", "txt", "equals", `0`, false},
		{"json bool equals bool", "json", "flag", "equals", `true`, true},
		{"json bool equals boolean string (legacy)", "json", "s_true", "equals", `true`, true},
		{"json false never equals missing", "json", "missing", "equals", `false`, false},
		{"json true never equals a number", "json", "n", "equals", `true`, false},
		{"json not_equals false holds for missing", "json", "missing", "not_equals", `false`, true},
		{"json null equals null", "json", "nul", "equals", `null`, true},
		{"json null never equals missing", "json", "missing", "equals", `null`, false},
		{"json object equals object", "json", "obj", "equals", `{"a":1}`, true},
		{"json array equals array", "json", "arr", "equals", `[1,2]`, true},
		{"json array differs", "json", "arr", "equals", `[2,1]`, false},
		// Text operators use the literal JSON text of a non-string value.
		{"json contains big number literal", "json", "big", "contains", `12345678901234567890`, true},
		{"body contains object literal", "body", "", "contains", `{"a":1}`, true},
		{"body contains array literal", "body", "", "contains", `[1,2]`, true},
		{"header equals decimal text", "header", "X-Version", "equals", `"1.50"`, true},
		{"header equals decimal literal", "header", "X-Version", "equals", `1.50`, true},
		{"body regex from string", "body", "", "regex", `"\"n\":12\\d"`, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertion := models.SyntheticAPIAssertionConfig{Target: tc.target, Op: tc.op, Path: tc.path, Value: json.RawMessage(tc.value)}
			failures, err := evaluateSyntheticAssertions([]models.SyntheticAPIAssertionConfig{assertion}, resp, body)
			if err != nil {
				t.Fatalf("evaluate: %v", err)
			}
			if got := len(failures) == 0; got != tc.pass {
				t.Fatalf("pass = %v, want %v (failures %v)", got, tc.pass, failures)
			}
		})
	}
}
