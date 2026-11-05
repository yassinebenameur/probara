package worker

import (
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

func runCaptureCheck(t *testing.T, handler http.HandlerFunc, config map[string]any) (CheckResult, *httpFailureResponse) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	config["url"] = srv.URL
	if _, ok := config["method"]; !ok {
		config["method"] = "GET"
	}
	raw, _ := json.Marshal(config)
	res := NewHTTPChecker(1024*1024, false, nil).Check(context.Background(), raw, 5)
	if len(res.MetricsData) == 0 {
		return res, nil
	}
	var doc capturedHTTP
	if err := json.Unmarshal(res.MetricsData, &doc); err != nil {
		t.Fatalf("metrics_data: %v", err)
	}
	return res, doc.HTTP.FailureResponse
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
		h.Set("Set-Cookie", "session=abc123; HttpOnly")
		h.Set("Authorization", "echo")
		h.Set("Proxy-Status", "upstream=10.1.2.3")
		h.Set("X-Api-Token", "t")
		h.Set("X-Secret-Id", "s")
		h.Set("X-Rate-Key", "k")
		h.Set("X-Tenant", "acme") // a header the monitor sends, echoed back
		h.Set("X-Echo-Auth", r.Header.Get("X-Upstream-Auth"))
		w.WriteHeader(http.StatusServiceUnavailable)
		fmt.Fprintf(w, `{"error":"upstream down","seen_auth":%q,"seen_basic":%q,"note":"retry later"}`,
			r.Header.Get("X-Upstream-Auth"), r.Header.Get("Authorization"))
	}
	res, capture := runCaptureCheck(t, handler, map[string]any{
		"headers":                  map[string]string{"X-Tenant": "acme"},
		"secret_headers":           map[string]string{"X-Upstream-Auth": bearer},
		"basic_auth_username":      "monitor",
		"basic_auth_password":      password,
		"capture_failure_response": true,
	})
	if res.Status != "failure" {
		t.Fatalf("status = %s", res.Status)
	}
	if capture == nil {
		t.Fatalf("no failure_response in %s", res.MetricsData)
	}
	if capture.StatusLine != "HTTP/1.1 503 Service Unavailable" || capture.ContentType != "application/json" {
		t.Fatalf("status line / content type = %q %q", capture.StatusLine, capture.ContentType)
	}
	for name, want := range map[string]string{
		"Server":         "edge-7",
		"X-Request-Id":   "req-42",
		"Set-Cookie":     captureRedacted,
		"Authorization":  captureRedacted,
		"Proxy-Status":   captureRedacted,
		"X-Api-Token":    captureRedacted,
		"X-Secret-Id":    captureRedacted,
		"X-Rate-Key":     captureRedacted,
		"X-Tenant":       captureRedacted,
		"X-Echo-Auth":    captureRedacted,
		"Content-Length": capture.Headers["Content-Length"],
	} {
		if got, ok := capture.Headers[name]; !ok || got != want {
			t.Errorf("header %s = %q (present %v), want %q", name, got, ok, want)
		}
	}
	for _, leaked := range []string{bearer, "sk-live-0123456789abcdef", password, basicToken, "abc123", "10.1.2.3"} {
		if strings.Contains(string(res.MetricsData), leaked) {
			t.Errorf("metrics_data leaks %q: %s", leaked, res.MetricsData)
		}
	}
	if !strings.Contains(capture.BodyExcerpt, `"error":"upstream down"`) || !strings.Contains(capture.BodyExcerpt, captureRedacted) || capture.BodyTruncated {
		t.Errorf("body excerpt = %q truncated=%v", capture.BodyExcerpt, capture.BodyTruncated)
	}
	// The capture never reaches the error message notifications quote.
	if res.ErrorMessage == nil || strings.Contains(*res.ErrorMessage, "upstream down") || *res.ErrorMessage != "status_code: got 503" {
		t.Errorf("error message = %v", res.ErrorMessage)
	}
}

func TestHTTPFailureCaptureOnlyWhenEnabledAndFailing(t *testing.T) {
	fail := func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(500); _, _ = w.Write([]byte("boom")) }
	ok := func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("fine")) }

	if _, capture := runCaptureCheck(t, fail, map[string]any{}); capture != nil {
		t.Fatal("capture is off by default")
	}
	if res, capture := runCaptureCheck(t, ok, map[string]any{"capture_failure_response": true}); res.Status != "success" || capture != nil {
		t.Fatalf("a passing check captures nothing: %s %+v", res.Status, capture)
	}
	// An error after the response arrived (a body assertion that cannot
	// run) captures too, and only when enabled.
	badRegex := map[string]any{"body_assertions": []map[string]string{{"op": "regex", "value": "("}}}
	if res, capture := runCaptureCheck(t, fail, badRegex); res.Status != "error" || res.MetricsData != nil || capture != nil {
		t.Fatalf("error without capture: %s %s", res.Status, res.MetricsData)
	}
	badRegex["capture_failure_response"] = true
	if res, capture := runCaptureCheck(t, fail, badRegex); res.Status != "error" || capture == nil || capture.BodyExcerpt != "boom" {
		t.Fatalf("error with capture: %s %+v", res.Status, capture)
	}
}

func TestHTTPFailureCaptureTruncation(t *testing.T) {
	// 3-byte runes, a budget that falls mid-rune: the cut lands on a rune
	// boundary and the excerpt stays valid UTF-8.
	body := strings.Repeat("€", 100)
	handler := func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(502); _, _ = w.Write([]byte(body)) }
	_, capture := runCaptureCheck(t, handler, map[string]any{"capture_failure_response": true, "capture_body_bytes": 100})
	if capture == nil || !utf8.ValidString(capture.BodyExcerpt) || len(capture.BodyExcerpt) != 99 || !capture.BodyTruncated {
		t.Fatalf("excerpt = %d bytes, valid %v, truncated %v", len(capture.BodyExcerpt), utf8.ValidString(capture.BodyExcerpt), capture.BodyTruncated)
	}

	// The default budget is 2048 bytes.
	long := func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		_, _ = w.Write([]byte(strings.Repeat("a", 5000)))
	}
	_, capture = runCaptureCheck(t, long, map[string]any{"capture_failure_response": true})
	if capture == nil || len(capture.BodyExcerpt) != 2048 || !capture.BodyTruncated {
		t.Fatalf("default excerpt = %d bytes", len(capture.BodyExcerpt))
	}

	// Invalid UTF-8 is replaced, never stored raw.
	if got, cut := truncateJSONString("ok\xffok", 64); got != "ok�ok" || cut {
		t.Fatalf("invalid UTF-8 = %q %v", got, cut)
	}
}

// The worst a server can send (an 8 KiB body of characters JSON escapes six
// bytes wide, dozens of long escaped headers) stays far below the 64 KiB
// metrics_data cutoff the AI evidence applies.
func TestHTTPFailureCaptureSizeBound(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		for i := 0; i < 80; i++ {
			w.Header().Set(fmt.Sprintf("X-Filler-%02d-%s", i, strings.Repeat("n", 60)), strings.Repeat("<&>", 400))
		}
		w.WriteHeader(500)
		_, _ = w.Write([]byte(strings.Repeat("<\x01>& ", 20000)))
	}
	res, capture := runCaptureCheck(t, handler, map[string]any{
		"capture_failure_response": true,
		"capture_body_bytes":       8192,
		"collect_timing":           true,
		"display_fields":           []map[string]string{{"path": "a"}, {"path": "b"}},
	})
	if capture == nil || !capture.BodyTruncated || !capture.HeadersTruncated {
		t.Fatalf("capture = %+v", capture)
	}
	if n := len(res.MetricsData); n >= 16*1024 {
		t.Fatalf("metrics_data is %d bytes, want < 16 KiB", n)
	}
}

// A NUL byte would marshal as \u0000, which PostgreSQL's jsonb rejects, so a
// UTF-16 or binary error body would make the check result unstorable.
func TestHTTPFailureCaptureNULBody(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		_, _ = w.Write([]byte("\xff\xfee\x00r\x00r\x00"))
	}
	res, capture := runCaptureCheck(t, handler, map[string]any{"capture_failure_response": true})
	if capture == nil {
		t.Fatalf("no capture in %s", res.MetricsData)
	}
	if strings.Contains(string(res.MetricsData), `\u0000`) {
		t.Fatalf("metrics_data carries \\u0000: %s", res.MetricsData)
	}
	if want := "\uFFFD\uFFFDe\uFFFDr\uFFFDr\uFFFD"; capture.BodyExcerpt != want || capture.BodyTruncated {
		t.Fatalf("excerpt = %q truncated=%v, want %q", capture.BodyExcerpt, capture.BodyTruncated, want)
	}

	// The replacement is budgeted at its own encoded size.
	if got, cut := truncateJSONString("a\x00b", 4); got != "a\uFFFD" || !cut {
		t.Fatalf("NUL budget = %q %v", got, cut)
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
	const secret = "Bearer " + token
	const budget = 2048
	cases := map[string]string{
		// A first match shrinks the text by 41 bytes, which pulled a second
		// copy starting just past the budget back inside it.
		"shifted copy": secret + strings.Repeat(".", budget+5-len(secret)) + secret + strings.Repeat(".", 100),
		"straddling":   strings.Repeat(".", budget-20) + secret + strings.Repeat(".", 100),
		"just past":    strings.Repeat(".", budget+1) + secret,
		"many shifts":  strings.Repeat(secret, 60) + strings.Repeat(".", 10) + secret,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			handler := func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(500)
				_, _ = w.Write([]byte(body))
			}
			_, capture := runCaptureCheck(t, handler, map[string]any{
				"secret_headers":           map[string]string{"X-Upstream": secret},
				"capture_failure_response": true,
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
	config := &models.HTTPMonitorConfig{SecretHeaders: map[string]string{"X-Upstream": "Bearer " + token}}
	resp := &http.Response{Proto: "HTTP/1.1", Status: "500 Internal Server Error", Header: http.Header{}}

	for _, tail := range []string{"Bearer " + token[:30], token[:12], "Bear"} {
		capture := captureFailureResponse(resp, []byte("error: "+tail), true, config)
		if capture.BodyExcerpt != "error: " || !capture.BodyTruncated {
			t.Errorf("tail %q: excerpt = %q truncated=%v", tail, capture.BodyExcerpt, capture.BodyTruncated)
		}
	}
	// A body that was read whole keeps its tail: nothing more could follow.
	if capture := captureFailureResponse(resp, []byte("error: Bear"), false, config); capture.BodyExcerpt != "error: Bear" || capture.BodyTruncated {
		t.Errorf("whole body: excerpt = %q truncated=%v", capture.BodyExcerpt, capture.BodyTruncated)
	}
}
