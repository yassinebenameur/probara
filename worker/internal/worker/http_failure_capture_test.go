package worker

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/yassinebenameur/probara/shared/models"
)

type capturedHTTP struct {
	HTTP struct {
		FailureResponse *httpFailureResponse `json:"failure_response"`
		Assertions      []string             `json:"assertions_failed"`
	} `json:"http"`
}

func runCaptureCheckWithLimit(t *testing.T, limit int, handler http.HandlerFunc, config map[string]any) (CheckResult, *httpFailureResponse) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	config["url"] = srv.URL
	if _, ok := config["method"]; !ok {
		config["method"] = "GET"
	}
	raw, _ := json.Marshal(config)
	res := NewHTTPChecker(limit, false, nil).Check(context.Background(), raw, 5)
	if len(res.MetricsData) == 0 {
		return res, nil
	}
	var doc capturedHTTP
	if err := json.Unmarshal(res.MetricsData, &doc); err != nil {
		t.Fatalf("metrics_data: %v", err)
	}
	return res, doc.HTTP.FailureResponse
}

func runCaptureCheck(t *testing.T, handler http.HandlerFunc, config map[string]any) (CheckResult, *httpFailureResponse) {
	t.Helper()
	return runCaptureCheckWithLimit(t, 1024*1024, handler, config)
}

func TestHTTPFailureCaptureRedaction(t *testing.T) {
	const bearer = "Bearer sk-live-0123456789abcdef"
	const password = "hunter2-correct-horse"
	basicToken := base64.StdEncoding.EncodeToString([]byte("monitor:" + password))
	handler := func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Type", "application/json")
		h.Set("Server", "edge-7")
		h.Set("X-Request-Id", "req-42")
		h.Set("Set-Cookie", "sid=abc123; HttpOnly")
		h.Set("Authorization", "echo")
		h.Set("Proxy-Status", "upstream=10.1.2.3")
		h.Set("X-Api-Token", "t")
		h.Set("X-Session-Id", "s-77")
		h.Set("X-Tenant", "acme") // a header the monitor sends, echoed back
		h.Set("Location", "https://login.example/?next=%2F&access_token=leaked-access-0001")
		w.WriteHeader(http.StatusServiceUnavailable)
		fmt.Fprintf(w, `{"error":"upstream down","seen_auth":%q,"seen_basic":%q,"note":"retry later","refresh_token":"rt-998877","client_secret":"cs-445566"}`,
			r.Header.Get("X-Upstream-Auth"), r.Header.Get("Authorization"))
	}
	res, capture := runCaptureCheck(t, handler, map[string]any{
		"headers":             map[string]string{"X-Tenant": "acme"},
		"secret_headers":      map[string]string{"X-Upstream-Auth": bearer},
		"basic_auth_username": "monitor",
		"basic_auth_password": password,
	})
	if res.Status != "failure" {
		t.Fatalf("status = %s", res.Status)
	}
	if capture == nil {
		t.Fatalf("no failure_response in %s", res.MetricsData)
	}
	if capture.StatusLine != "HTTP/1.1 503 Service Unavailable" {
		t.Fatalf("status line = %q", capture.StatusLine)
	}
	for name, want := range map[string]string{
		"Server":        "edge-7",
		"X-Request-Id":  "req-42",
		"Content-Type":  "application/json",
		"Set-Cookie":    captureRedacted,
		"Authorization": captureRedacted,
		"Proxy-Status":  captureRedacted,
		"X-Api-Token":   captureRedacted,
		"X-Session-Id":  captureRedacted,
		"X-Tenant":      captureRedacted,
		"Location":      "https://login.example/?next=%2F&access_token=" + captureRedacted,
	} {
		if got, ok := capture.Headers[name]; !ok || got != want {
			t.Errorf("header %s = %q (present %v), want %q", name, got, ok, want)
		}
	}
	for _, leaked := range []string{bearer, "sk-live-0123456789abcdef", password, basicToken, "abc123", "10.1.2.3", "s-77", "rt-998877", "cs-445566", "leaked-access-0001"} {
		if strings.Contains(string(res.MetricsData), leaked) {
			t.Errorf("metrics_data leaks %q: %s", leaked, res.MetricsData)
		}
	}
	if !strings.Contains(capture.BodyExcerpt, `"error":"upstream down"`) || !strings.Contains(capture.BodyExcerpt, `"refresh_token":"[REDACTED]"`) || capture.BodyTruncated {
		t.Errorf("body excerpt = %q truncated=%v", capture.BodyExcerpt, capture.BodyTruncated)
	}
	// The capture never reaches the error message notifications quote.
	if res.ErrorMessage == nil || *res.ErrorMessage != "status_code: got 503" {
		t.Errorf("error message = %v", res.ErrorMessage)
	}
}

// Always on for a check that failed or errored after a response arrived;
// a passing check stores nothing extra.
func TestHTTPFailureCaptureOnlyWhenFailing(t *testing.T) {
	fail := func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(500); _, _ = w.Write([]byte("boom")) }
	ok := func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("fine")) }

	if res, capture := runCaptureCheck(t, fail, map[string]any{}); res.Status != "failure" || capture == nil || capture.BodyExcerpt != "boom" {
		t.Fatalf("failure: %s %+v", res.Status, capture)
	}
	res, capture := runCaptureCheck(t, ok, map[string]any{})
	if res.Status != "success" || capture != nil || strings.Contains(string(res.MetricsData), "failure_response") {
		t.Fatalf("a passing check captures nothing: %s %s", res.Status, res.MetricsData)
	}
	// An error after the response arrived (a body assertion that cannot
	// run) captures too.
	badRegex := map[string]any{"body_assertions": []map[string]string{{"op": "regex", "value": "("}}}
	if res, capture := runCaptureCheck(t, fail, badRegex); res.Status != "error" || capture == nil || capture.BodyExcerpt != "boom" {
		t.Fatalf("error with response: %s %+v", res.Status, capture)
	}
}

func TestHTTPFailureCaptureTruncation(t *testing.T) {
	// 3-byte runes, a budget that falls mid-rune: the cut lands on a rune
	// boundary and the excerpt stays valid UTF-8.
	body := strings.Repeat("€", 3000)
	handler := func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(502); _, _ = w.Write([]byte(body)) }
	_, capture := runCaptureCheck(t, handler, map[string]any{})
	if capture == nil || !utf8.ValidString(capture.BodyExcerpt) || len(capture.BodyExcerpt) != captureBodyBudget-captureBodyBudget%3 || !capture.BodyTruncated {
		t.Fatalf("excerpt = %d bytes, valid %v, truncated %v", len(capture.BodyExcerpt), utf8.ValidString(capture.BodyExcerpt), capture.BodyTruncated)
	}

	// A body cut by the worker's read limit is marked truncated even when the
	// excerpt itself fits.
	short := func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		_, _ = w.Write([]byte(strings.Repeat("a", 500)))
	}
	_, capture = runCaptureCheckWithLimit(t, 100, short, map[string]any{})
	if capture == nil || len(capture.BodyExcerpt) != 100 || !capture.BodyTruncated {
		t.Fatalf("read-capped excerpt = %d bytes truncated=%v", len(capture.BodyExcerpt), capture != nil && capture.BodyTruncated)
	}

	// Invalid UTF-8 is replaced, never stored raw.
	if got, cut := truncateJSONString("ok\xffok", 64); got != "ok�ok" || cut {
		t.Fatalf("invalid UTF-8 = %q %v", got, cut)
	}
}

// The worst a server can send (a body of characters JSON escapes six bytes
// wide, dozens of long escaped headers) keeps the capture under 16 KiB, and
// the whole metrics_data far below the 64 KiB the AI evidence reads.
func TestFailureCaptureBounded(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		for i := 0; i < 80; i++ {
			w.Header().Set(fmt.Sprintf("X-Filler-%02d-%s", i, strings.Repeat("n", 200)), strings.Repeat("<&>", 400))
		}
		w.WriteHeader(500)
		_, _ = w.Write([]byte(strings.Repeat("<\x01>&  ", 20000)))
	}
	res, capture := runCaptureCheck(t, handler, map[string]any{
		"collect_timing": true,
		"display_fields": []map[string]string{{"path": "a"}, {"path": "b"}},
	})
	if capture == nil || !capture.BodyTruncated || !capture.HeadersTruncated {
		t.Fatalf("capture = %+v", capture)
	}
	encoded, _ := json.Marshal(capture)
	if len(encoded) >= captureMaxBytes {
		t.Fatalf("failure_response is %d bytes, want < %d", len(encoded), captureMaxBytes)
	}
	if n := len(res.MetricsData); n >= 20*1024 {
		t.Fatalf("metrics_data is %d bytes", n)
	}
}

// A NUL byte would marshal as \u0000, which PostgreSQL's jsonb rejects, so a
// UTF-16 or broken text body would make the check result unstorable.
func TestHTTPFailureCaptureNULBody(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(500)
		_, _ = w.Write([]byte("\xff\xfee\x00r\x00r\x00"))
	}
	res, capture := runCaptureCheck(t, handler, map[string]any{})
	if capture == nil {
		t.Fatalf("no capture in %s", res.MetricsData)
	}
	if strings.Contains(string(res.MetricsData), `\u0000`) {
		t.Fatalf("metrics_data carries \\u0000: %s", res.MetricsData)
	}
	if want := "��e�r�r�"; capture.BodyExcerpt != want || capture.BodyTruncated {
		t.Fatalf("excerpt = %q truncated=%v, want %q", capture.BodyExcerpt, capture.BodyTruncated, want)
	}

	// The replacement is budgeted at its own encoded size.
	if got, cut := truncateJSONString("a\x00b", 4); got != "a�" || !cut {
		t.Fatalf("NUL budget = %q %v", got, cut)
	}
}

// Binary bodies are described, not stored.
func TestHTTPFailureCaptureBinaryBody(t *testing.T) {
	png := []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01")
	cases := []struct {
		name, contentType string
		body              []byte
		wantNote          string
		wantText          string
	}{
		{"declared image", "image/png", png, "binary body not stored (image/png, 20 bytes)", ""},
		{"undeclared binary", "", png, "binary body not stored (image/png, 20 bytes)", ""},
		{"octet-stream that is text", "application/octet-stream", []byte("gateway timeout"), "", "gateway timeout"},
		{"octet-stream that is binary", "application/octet-stream", []byte{0, 1, 2, 3, 0xff}, "binary body not stored (application/octet-stream, 5 bytes)", ""},
		{"problem json", "application/problem+json; charset=utf-8", []byte(`{"title":"down"}`), "", `{"title":"down"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			handler := func(w http.ResponseWriter, r *http.Request) {
				if tc.contentType != "" {
					w.Header().Set("Content-Type", tc.contentType)
				} else {
					w.Header()["Content-Type"] = nil // no sniffing by net/http
				}
				w.WriteHeader(500)
				_, _ = w.Write(tc.body)
			}
			_, capture := runCaptureCheck(t, handler, map[string]any{})
			if capture == nil || capture.BodyNote != tc.wantNote || capture.BodyExcerpt != tc.wantText {
				t.Fatalf("capture = %+v", capture)
			}
		})
	}
}

func TestRedactCredentialFields(t *testing.T) {
	cases := map[string]string{
		`{"password":"p@ss\"word","user":"ann"}`:             `{"password":"[REDACTED]","user":"ann"}`,
		`{"accessToken": "eyJabc.def", "expires_in": 3600}`:  `{"accessToken": "[REDACTED]", "expires_in": 3600}`,
		`{"api_key":"k-1","session_id":"s-2","author":"bo"}`: `{"api_key":"[REDACTED]","session_id":"[REDACTED]","author":"bo"}`,
		"grant=x&client_secret=abc&scope=read":               "grant=x&client_secret=[REDACTED]&scope=read",
		"see https://a.example/cb?code=1&id_token=zzz now":   "see https://a.example/cb?code=1&id_token=[REDACTED] now",
		"Authorization: Bearer abc.def.ghi\nHost: a":         "Authorization: [REDACTED]\nHost: a",
		"db:\n  password: hunter2\n  host: db1":              "db:\n  password: [REDACTED]\n  host: db1",
		"upstream sent bearer 0123456789abcdefXYZ":           "upstream sent bearer [REDACTED]",
		"Bearer authentication required":                     "Bearer authentication required",
		`{"error":"invalid_grant"}`:                          `{"error":"invalid_grant"}`,
		// A capped read ending inside a value still loses the value.
		`{"ok":false,"token":"abcdef`: `{"ok":false,"token":"[REDACTED]`,
	}
	for in, want := range cases {
		if got := redactCredentialFields(in); got != want {
			t.Errorf("redactCredentialFields(%q)\n got %q\nwant %q", in, got, want)
		}
	}
}

// captureLeak returns a run of n or more characters of token found in s.
func captureLeak(s, token string, n int) string {
	for i := 0; i+n <= len(token); i++ {
		if strings.Contains(s, token[i:i+n]) {
			return token[i : i+n]
		}
	}
	return ""
}

// Redaction runs before the excerpt is cut: a secret straddling the budget,
// or a copy past it that earlier matches would shift into it, never shows.
func TestHTTPFailureCaptureRedactionAtBudget(t *testing.T) {
	const token = "abcdefghijklmnopqrstuvwxyz0123456789abcdefgh" // 44 chars
	const secret = "Custom " + token
	const budget = captureBodyBudget
	cases := map[string]string{
		// A first match shrinks the text by 41 bytes, which pulled a second
		// copy starting just past the budget back inside it.
		"shifted copy": secret + strings.Repeat(".", budget+5-len(secret)) + secret + strings.Repeat(".", 100),
		"straddling":   strings.Repeat(".", budget-20) + secret + strings.Repeat(".", 100),
		"just past":    strings.Repeat(".", budget+1) + secret,
		"many shifts":  strings.Repeat(secret, 220) + strings.Repeat(".", 10) + secret,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			handler := func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/plain")
				w.WriteHeader(500)
				_, _ = w.Write([]byte(body))
			}
			_, capture := runCaptureCheck(t, handler, map[string]any{
				"secret_headers": map[string]string{"X-Upstream": secret},
			})
			if capture == nil {
				t.Fatal("no capture")
			}
			if leak := captureLeak(capture.BodyExcerpt, token, 4); leak != "" {
				t.Fatalf("excerpt leaks %q of the token: ...%s", leak, capture.BodyExcerpt[max(0, len(capture.BodyExcerpt)-80):])
			}
			if len(capture.BodyExcerpt) > budget {
				t.Fatalf("excerpt = %d bytes", len(capture.BodyExcerpt))
			}
		})
	}
}

// A read that stopped at the worker's body limit may end inside a secret;
// that partial secret matches no form, so the tail is dropped.
func TestHTTPFailureCaptureReadCapEndsInSecret(t *testing.T) {
	const token = "abcdefghijklmnopqrstuvwxyz0123456789abcdefgh"
	config := &models.HTTPMonitorConfig{SecretHeaders: map[string]string{"X-Upstream": "Custom " + token}}
	resp := &http.Response{Proto: "HTTP/1.1", Status: "500 Internal Server Error", Header: http.Header{"Content-Type": {"text/plain"}}}

	for _, tail := range []string{"Custom " + token[:30], token[:12], "Cust"} {
		capture := captureFailureResponse(resp, []byte("error: "+tail), true, config)
		if capture.BodyExcerpt != "error: " || !capture.BodyTruncated {
			t.Errorf("tail %q: excerpt = %q truncated=%v", tail, capture.BodyExcerpt, capture.BodyTruncated)
		}
	}
	// A body that was read whole keeps its tail: nothing more could follow.
	if capture := captureFailureResponse(resp, []byte("error: Cust"), false, config); capture.BodyExcerpt != "error: Cust" || capture.BodyTruncated {
		t.Errorf("whole body: excerpt = %q truncated=%v", capture.BodyExcerpt, capture.BodyTruncated)
	}
}

// A body the transport did not decode (the monitor asked for gzip itself) is
// compressed bytes redaction cannot see into: described, never stored. One
// the transport decoded is text as usual.
func TestHTTPFailureCaptureEncodedBody(t *testing.T) {
	var gz bytes.Buffer
	zw := gzip.NewWriter(&gz)
	_, _ = zw.Write([]byte(`{"error":"down","token":"secret-123456"}`))
	_ = zw.Close()
	handler := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Encoding", "gzip")
		w.WriteHeader(500)
		_, _ = w.Write(gz.Bytes())
	}
	_, capture := runCaptureCheck(t, handler, map[string]any{"headers": map[string]string{"Accept-Encoding": "gzip"}})
	if capture == nil || capture.BodyExcerpt != "" || capture.BodyNote != fmt.Sprintf("encoded body not stored (gzip, %d bytes)", gz.Len()) {
		t.Fatalf("monitor-requested gzip: capture = %+v", capture)
	}
	_, capture = runCaptureCheck(t, handler, map[string]any{})
	if capture == nil || capture.BodyNote != "" || capture.BodyExcerpt != `{"error":"down","token":"[REDACTED]"}` {
		t.Fatalf("transport-decoded gzip: capture = %+v", capture)
	}
}
