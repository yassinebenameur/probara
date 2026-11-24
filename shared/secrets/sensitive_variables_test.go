package secrets

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// TestIsSensitiveVariableName_SharedFixture pins the variable rule, and its TS
// twin, to testdata/sensitive_variable_names.json: the words and pairs must
// be exactly the fixture's, and every listed name classify as listed.
func TestIsSensitiveVariableName_SharedFixture(t *testing.T) {
	raw, err := os.ReadFile("testdata/sensitive_variable_names.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var fixture struct {
		FinalWords []string    `json:"final_words"`
		FinalPairs [][2]string `json:"final_pairs"`
		Sensitive  []string    `json:"sensitive"`
		Plain      []string    `json:"plain"`
	}
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatalf("decode fixture: %v", err)
	}
	if strings.Join(sensitiveVariableWords, ",") != strings.Join(fixture.FinalWords, ",") {
		t.Fatalf("words = %v, fixture has %v", sensitiveVariableWords, fixture.FinalWords)
	}
	if len(sensitiveVariablePairs) != len(fixture.FinalPairs) {
		t.Fatalf("pairs = %v, fixture has %v", sensitiveVariablePairs, fixture.FinalPairs)
	}
	for i, pair := range sensitiveVariablePairs {
		if pair != fixture.FinalPairs[i] {
			t.Fatalf("pairs = %v, fixture has %v", sensitiveVariablePairs, fixture.FinalPairs)
		}
	}
	for _, name := range fixture.Sensitive {
		if !IsSensitiveVariableName(name) {
			t.Errorf("IsSensitiveVariableName(%q) = false, want true", name)
		}
	}
	for _, name := range fixture.Plain {
		if IsSensitiveVariableName(name) {
			t.Errorf("IsSensitiveVariableName(%q) = true, want false", name)
		}
	}
}

// TestNormalizeSensitiveHeaders_OrdinaryVariablesStayPlain: a name that only
// mentions a credential (`token_url`) is ordinary configuration and must stay
// readable, editable and unredacted.
func TestNormalizeSensitiveHeaders_OrdinaryVariablesStayPlain(t *testing.T) {
	in := json.RawMessage(`{"variables":{"token_url":"https://auth.test/oauth/token","password_reset_url":"/reset","forgot_password_link":"a.forgot"},"steps":[]}`)
	for _, monitorType := range []string{"synthetic_api", "synthetic_browser"} {
		out, changed, err := NormalizeSensitiveHeaders(monitorType, in)
		if err != nil || changed || string(out) != string(in) {
			t.Fatalf("%s: changed=%v err=%v out=%s", monitorType, changed, err, out)
		}
	}
}

// TestNormalizeSensitiveHeaders_UnmovableVariablesStayPlain: moving a name a
// {{ref}} cannot reach, or one differing only by case from another secret
// name, would store a config validation refuses, so it stays plain and is
// reported for the backfill.
func TestNormalizeSensitiveHeaders_UnmovableVariablesStayPlain(t *testing.T) {
	cases := []struct {
		name, config string
		unmovable    []string
	}{
		{"case clash between variables", `{"variables":{"Password":"a","password":"b"},"steps":[]}`, []string{"Password", "password"}},
		{"name unreachable by a ref", `{"variables":{"old password":"a"},"steps":[]}`, []string{"old password"}},
		{"case clash with a stored secret", `{"variables":{"Token":"a"},"secret_variables":{"token":"enc"},"steps":[]}`, []string{"Token"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, changed, err := NormalizeSensitiveHeaders("synthetic_api", json.RawMessage(tc.config))
			if err != nil || changed || string(out) != tc.config {
				t.Fatalf("changed=%v err=%v out=%s", changed, err, out)
			}
			if got := UnmovableSensitiveVariables("synthetic_api", []byte(tc.config)); strings.Join(got, ",") != strings.Join(tc.unmovable, ",") {
				t.Fatalf("unmovable = %v, want %v", got, tc.unmovable)
			}
		})
	}
	// A movable one beside them still moves.
	out, changed, _ := NormalizeSensitiveHeaders("synthetic_api", json.RawMessage(`{"variables":{"old password":"a","api_key":"k"},"steps":[]}`))
	if !changed || !strings.Contains(string(out), `"secret_variables":{"api_key":"k"}`) || !strings.Contains(string(out), `"old password":"a"`) {
		t.Fatalf("out = %s", out)
	}
}

// A generated step-header variable never differs only by case from an
// existing name.
func TestNormalizeSensitiveHeaders_GeneratedNameAvoidsCaseClash(t *testing.T) {
	in := json.RawMessage(`{"secret_variables":{"Login_Authorization":"enc"},"steps":[{"id":"login","request":{"method":"GET","url":"https://x.test","headers":{"Authorization":"Bearer t"}}}]}`)
	out, _, err := NormalizeSensitiveHeaders("synthetic_api", in)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), `"Authorization":"{{login_authorization_2}}"`) {
		t.Fatalf("out = %s", out)
	}
}
