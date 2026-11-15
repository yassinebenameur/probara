package worker

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func syntheticConfig(t *testing.T, cfg map[string]any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	return raw
}

// Every synthetic_api step dials through the shared guard: a loopback target
// is refused before any connection is made, and HTTP_ALLOWED_CIDRS (or a
// worker that does not block private addresses) lets it back in.
func TestSyntheticAPIChecker_SSRFPolicy(t *testing.T) {
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	config := syntheticConfig(t, map[string]any{
		"steps": []map[string]any{{
			"id":      "only",
			"request": map[string]any{"method": "GET", "url": server.URL + "/ok"},
			"assert":  []map[string]any{{"target": "status", "op": "equals", "value": 200}},
		}},
	})

	blocked := NewSyntheticAPIChecker(true, nil).Check(context.Background(), config, 5)
	if blocked.Status != "error" || !strings.Contains(derefErr(blocked.ErrorMessage), "ssrf_blocked") {
		t.Fatalf("blocked result = %q (%s), want an ssrf_blocked error", blocked.Status, derefErr(blocked.ErrorMessage))
	}
	if n := hits.Load(); n != 0 {
		t.Fatalf("server saw %d requests through a blocking policy, want 0", n)
	}

	_, loopback, _ := net.ParseCIDR("127.0.0.0/8")
	for name, checker := range map[string]*SyntheticAPIChecker{
		"blocking off": NewSyntheticAPIChecker(false, nil),
		"allow-listed": NewSyntheticAPIChecker(true, []*net.IPNet{loopback}),
	} {
		if result := checker.Check(context.Background(), config, 5); result.Status != "success" {
			t.Errorf("%s: status %q (%s), want success", name, result.Status, derefErr(result.ErrorMessage))
		}
	}
}

// A redirect hop is dialed through the guard too: an allow-listed first hop
// that redirects to a refused address fails at the hop, which is never
// reached.
func TestSyntheticAPIChecker_SSRFPolicyCoversRedirects(t *testing.T) {
	var targetHits atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		targetHits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()
	_, targetPort, _ := net.SplitHostPort(target.Listener.Addr().String())

	// The redirect target is [::1] when the worker allow-lists only
	// 127.0.0.0/8, so the hop is refused by the policy.
	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://[::1]:"+targetPort+"/", http.StatusFound)
	}))
	defer redirector.Close()

	_, loopbackV4, _ := net.ParseCIDR("127.0.0.0/8")
	config := syntheticConfig(t, map[string]any{
		"steps": []map[string]any{{
			"id":      "redirect",
			"request": map[string]any{"method": "GET", "url": redirector.URL + "/start"},
		}},
	})
	result := NewSyntheticAPIChecker(true, []*net.IPNet{loopbackV4}).Check(context.Background(), config, 5)
	if result.Status != "error" || !strings.Contains(derefErr(result.ErrorMessage), "ssrf_blocked") {
		t.Fatalf("result = %q (%s), want an ssrf_blocked error on the redirect hop", result.Status, derefErr(result.ErrorMessage))
	}
	if n := targetHits.Load(); n != 0 {
		t.Fatalf("redirect target saw %d requests, want 0", n)
	}
}

// Assertion values resolve {{variables}} like the request does: static
// variables, values extracted by an earlier step, and inside a larger
// string. Non-string values are left as typed.
func TestSyntheticAPIChecker_AssertionValuesAreInterpolated(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/login":
			w.Header().Set("X-Session", "sess-42")
			_, _ = w.Write([]byte(`{"token":"tok-abc","user_id":7}`))
		case "/me":
			w.Header().Set("X-Owner", "user 7")
			_, _ = w.Write([]byte(`{"email":"monitor@example.com","id":"7","greeting":"hello monitor@example.com","n":7}`))
		}
	}))
	defer server.Close()

	run := func(t *testing.T, asserts []map[string]any) CheckResult {
		t.Helper()
		return NewSyntheticAPIChecker(false, nil).Check(context.Background(), syntheticConfig(t, map[string]any{
			"base_url":  server.URL,
			"variables": map[string]string{"user": "monitor@example.com"},
			"steps": []map[string]any{
				{
					"id":      "login",
					"request": map[string]any{"method": "POST", "url": "/login"},
					"extract": []map[string]any{
						{"name": "uid", "from": "json", "path": "user_id"},
						{"name": "session", "from": "header", "path": "X-Session"},
					},
				},
				{
					"id":      "me",
					"request": map[string]any{"method": "GET", "url": "/me"},
					"assert":  asserts,
				},
			},
		}), 5)
	}

	t.Run("matching values pass", func(t *testing.T) {
		result := run(t, []map[string]any{
			{"target": "json", "path": "email", "op": "equals", "value": "{{user}}"},
			{"target": "json", "path": "id", "op": "equals", "value": "{{uid}}"},
			{"target": "json", "path": "n", "op": "equals", "value": "{{uid}}"}, // string expected matches a number's text
			{"target": "json", "path": "greeting", "op": "contains", "value": "hello {{user}}"},
			{"target": "header", "path": "X-Owner", "op": "equals", "value": "user {{ uid }}"},
			{"target": "body", "op": "contains", "value": "{{user}}"},
			{"target": "json", "path": "n", "op": "equals", "value": 7}, // typed values unchanged
		})
		if result.Status != "success" {
			t.Fatalf("status %q (%s), want success", result.Status, derefErr(result.ErrorMessage))
		}
	})

	t.Run("a mismatch fails and shows the resolved value", func(t *testing.T) {
		result := run(t, []map[string]any{
			{"target": "json", "path": "email", "op": "equals", "value": "{{session}}"},
		})
		if result.Status != "failure" {
			t.Fatalf("status %q (%s), want failure", result.Status, derefErr(result.ErrorMessage))
		}
		if msg := derefErr(result.ErrorMessage); !strings.Contains(msg, "expected=sess-42") {
			t.Errorf("error %q, want the resolved expected value", msg)
		}
	})

	t.Run("an undefined name stays literal and fails as an ordinary assertion", func(t *testing.T) {
		result := run(t, []map[string]any{
			{"target": "json", "path": "email", "op": "equals", "value": "{{nope}}"},
		})
		if result.Status != "failure" {
			t.Fatalf("status %q (%s), want failure", result.Status, derefErr(result.ErrorMessage))
		}
		if msg := derefErr(result.ErrorMessage); !strings.Contains(msg, "expected={{nope}}") {
			t.Errorf("error %q, want the unresolved {{nope}} shown", msg)
		}
	})
}

// Assertions written before values were templated keep passing: an undefined
// name is literal text, and an op that ignores its value never templates it.
func TestSyntheticAPIChecker_AssertionLiteralTemplatesStillPass(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"title":"Welcome"}`))
	}))
	defer server.Close()

	for name, assertion := range map[string]map[string]any{
		// The UI keeps the old value when the op is switched to exists.
		"exists with a leftover value": {"target": "json", "path": "title", "op": "exists", "value": "{{old}}"},
		// The page must not show an unrendered template.
		"not_contains an unrendered template": {"target": "body", "op": "not_contains", "value": "{{ title }}"},
	} {
		t.Run(name, func(t *testing.T) {
			result := NewSyntheticAPIChecker(false, nil).Check(context.Background(), syntheticConfig(t, map[string]any{
				"base_url":  server.URL,
				"variables": map[string]string{"user": "monitor@example.com"},
				"steps": []map[string]any{{
					"id":      "page",
					"request": map[string]any{"method": "GET", "url": "/"},
					"assert":  []map[string]any{assertion},
				}},
			}), 5)
			if result.Status != "success" {
				t.Fatalf("status %q (%s), want success", result.Status, derefErr(result.ErrorMessage))
			}
		})
	}
}

// A sensitive value shorter than syntheticMinScrubbedSecretLen is still
// redacted where it was substituted into the expected value, but is not
// scrubbed from the rest of the message, which would garble unrelated text.
func TestSyntheticAPIChecker_ShortSensitiveValueKeepsMessageIntact(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/login":
			_, _ = w.Write([]byte(`{"code":"1"}`))
		case "/me":
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{}`))
		}
	}))
	defer server.Close()

	result := NewSyntheticAPIChecker(false, nil).Check(context.Background(), syntheticConfig(t, map[string]any{
		"base_url": server.URL,
		"steps": []map[string]any{
			{
				"id":      "login",
				"request": map[string]any{"method": "POST", "url": "/login"},
				"extract": []map[string]any{{"name": "code", "from": "json", "path": "code", "sensitive": true}},
			},
			{
				"id":      "me",
				"request": map[string]any{"method": "GET", "url": "/me"},
				"assert":  []map[string]any{{"target": "status", "op": "equals", "value": "20{{code}}"}},
			},
		},
	}), 5)
	if result.Status != "failure" {
		t.Fatalf("status %q (%s), want failure", result.Status, derefErr(result.ErrorMessage))
	}
	msg := derefErr(result.ErrorMessage)
	if !strings.Contains(msg, "expected=20[REDACTED]") {
		t.Errorf("error %q, want the substituted expected value redacted", msg)
	}
	if !strings.Contains(msg, "actual=401") {
		t.Errorf("error %q, want the actual status left intact", msg)
	}
}

// The transport ignores HTTP(S)_PROXY, like every other worker checker: a
// proxy would leave the guard vetting only the proxy's address.
func TestSyntheticAPIChecker_TransportHasNoProxy(t *testing.T) {
	if transport := NewSyntheticAPIChecker(true, nil).newTransport(); transport.Proxy != nil {
		t.Fatal("transport.Proxy is set, want nil")
	}
}

// A sensitive extracted value used in an assertion never shows in the check's
// error message or step metrics, whether the assertion failed on the
// expected value, echoed the secret back as the actual value, or failed to
// compile it as a pattern.
func TestSyntheticAPIChecker_SensitiveValueNotInAssertionMessages(t *testing.T) {
	const secret = "s3cr3t-T0ken(" // the "(" makes it an invalid regex
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/login":
			_, _ = w.Write([]byte(`{"token":"` + secret + `"}`))
		case "/me":
			w.Header().Set("X-Echo", secret)
			_, _ = w.Write([]byte(`{"token":"other"}`))
		}
	}))
	defer server.Close()

	for name, assertion := range map[string]map[string]any{
		"expected value":    {"target": "json", "path": "token", "op": "equals", "value": "{{token}}"},
		"inside a template": {"target": "body", "op": "contains", "value": "Bearer {{token}}"},
		"echoed as actual":  {"target": "header", "path": "X-Echo", "op": "not_equals", "value": "{{token}}"},
		"regex error":       {"target": "body", "op": "regex", "value": "{{token}}"},
	} {
		t.Run(name, func(t *testing.T) {
			result := NewSyntheticAPIChecker(false, nil).Check(context.Background(), syntheticConfig(t, map[string]any{
				"base_url": server.URL,
				"steps": []map[string]any{
					{
						"id":      "login",
						"request": map[string]any{"method": "POST", "url": "/login"},
						"extract": []map[string]any{{"name": "token", "from": "json", "path": "token", "sensitive": true}},
					},
					{
						"id":      "me",
						"request": map[string]any{"method": "GET", "url": "/me"},
						"assert":  []map[string]any{assertion},
					},
				},
			}), 5)
			if result.Status == "success" {
				t.Fatal("status = success, want the assertion to fail")
			}
			msg := derefErr(result.ErrorMessage)
			if strings.Contains(msg, "s3cr3t") || strings.Contains(string(result.MetricsData), "s3cr3t") {
				t.Fatalf("secret leaked: error %q, metrics %s", msg, result.MetricsData)
			}
			if !strings.Contains(msg, "[REDACTED]") {
				t.Errorf("error %q, want the secret shown as [REDACTED]", msg)
			}
		})
	}
}
