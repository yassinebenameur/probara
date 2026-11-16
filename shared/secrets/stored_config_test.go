package secrets

import (
	"encoding/json"
	"strings"
	"testing"
)

// Rows stored before the move rules ran on every save can hold credentials
// in plain maps and under miscased keys; none may survive masking.
func TestMaskStoredMonitorConfig_LeavesNoCredential(t *testing.T) {
	cases := []struct {
		typ, raw string
		keep     []string // non-secret values that must survive
	}{
		{"http", `{"url":"https://a","headers":{"Authorization":"Bearer p-1","Accept":"json"},"Headers":{"Cookie":"p-2"},"Basic_Auth_Password":"p-3","basic_auth_username":"ops"}`,
			[]string{"https://a", "json", "ops"}},
		// Two spellings that disagree cannot be folded; both are scrubbed.
		{"http", `{"url":"https://a","headers":{"X-Api-Key":"p-1"},"Headers":{"X-Api-Key":"p-2"}}`, []string{"https://a"}},
		{"grpc", `{"host":"g","metadata":{"authorization":"p-1","x-tenant":"t1"}}`, []string{"t1"}},
		{"synthetic_api", `{"variables":{"Password":"p-1","password":"p-2","user":"robot"},"steps":[{"id":"a","request":{"method":"GET","url":"https://x","headers":{"X-Token":"p-3","Authorization":"Bearer {{tok}}"}}}]}`,
			[]string{"robot", "Bearer {{tok}}"}},
		{"synthetic_browser", `{"start_url":"https://x","variables":{"api_key":"p-1"}}`, []string{"https://x"}},
		{"postgres", `{"host":"db","password":"p-1","Connection_String":"p-2"}`, []string{"db"}},
	}
	for _, tc := range cases {
		out, err := MaskStoredMonitorConfig(tc.typ, json.RawMessage(tc.raw))
		if err != nil {
			t.Fatalf("%s: %v", tc.typ, err)
		}
		text := string(out)
		for _, secret := range []string{"p-1", "p-2", "p-3"} {
			if strings.Contains(text, secret) {
				t.Fatalf("%s kept %q: %s", tc.typ, secret, text)
			}
		}
		for _, value := range tc.keep {
			if !strings.Contains(text, value) {
				t.Fatalf("%s lost %q: %s", tc.typ, value, text)
			}
		}
	}
	// Moved credentials land in the secret map, masked.
	out, _ := MaskStoredMonitorConfig("http", json.RawMessage(`{"headers":{"Authorization":"Bearer p-1"}}`))
	if string(out) != `{"secret_headers":{"Authorization":"***"}}` {
		t.Fatalf("moved header = %s", out)
	}
	// Types without secrets pass through byte for byte.
	if out, _ := MaskStoredMonitorConfig("ping", json.RawMessage(`{"host":"h"}`)); string(out) != `{"host":"h"}` {
		t.Fatalf("ping = %s", out)
	}
}

func TestWalkPlainCredentials(t *testing.T) {
	var cfg map[string]any
	_ = json.Unmarshal([]byte(`{"variables":{"token":"t","token_url":"u"},"steps":[{"request":{"Headers":{"Cookie":"c","X-Ref":"{{v}}","Authorization":"{{auth}}"}}}]}`), &cfg)
	var got []string
	WalkPlainCredentials("synthetic_api", cfg, func(where string, _ map[string]any, name string) {
		got = append(got, where+"."+name)
	})
	if strings.Join(got, ",") != "variables.token,steps[0].request.Headers.Cookie" {
		t.Fatalf("visited %v", got)
	}
}

func TestMaskURLCredentials(t *testing.T) {
	cases := map[string]string{
		"https://ops:p-1@api.example/x?token=p-2&page=2#top": "https://ops:***@api.example/x?token=***&page=2#top",
		"wss://h.example/ws?access_token=p-1&api_key=p-2":    "wss://h.example/ws?access_token=***&api_key=***",
		"https://ops@h.example/?token={{tok}}":               "https://ops@h.example/?token={{tok}}",
		"https://h.example/?token_url=https://x&q=1":         "https://h.example/?token_url=https://x&q=1",
		"not a url, token=p-1":                               "not a url, token=p-1",
	}
	for in, want := range cases {
		got, _ := MaskURLCredentials(in)
		if got != want {
			t.Fatalf("MaskURLCredentials(%q) = %q, want %q", in, got, want)
		}
	}
	stripped, changed := StripMaskedURLCredentials("https://ops:***@h.example/x?token=***&page=2")
	if !changed || stripped != "https://ops@h.example/x?page=2" {
		t.Fatalf("strip = %q %v", stripped, changed)
	}
	// Snapshots mask URL credentials for every type, nested ones included.
	out, _ := MaskStoredMonitorConfig("synthetic_api", json.RawMessage(`{"base_url":"https://u:p-1@h","steps":[{"id":"a","request":{"method":"GET","url":"https://h/x?apikey=p-2"}}]}`))
	if strings.Contains(string(out), "p-1") || strings.Contains(string(out), "p-2") {
		t.Fatalf("URL credentials survived: %s", out)
	}
	if out, _ := MaskStoredMonitorConfig("ping", json.RawMessage(`{"host":"h"}`)); string(out) != `{"host":"h"}` {
		t.Fatalf("ping = %s", out)
	}
}
