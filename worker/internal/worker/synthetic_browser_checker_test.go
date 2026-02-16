package worker

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yassinebenameur/probara/shared/models"
)

type syntheticBrowserMetricsTestEnvelope struct {
	SyntheticBrowser struct {
		CompletedSteps int    `json:"completed_steps"`
		FailedStepID   string `json:"failed_step_id"`
		FinalURL       string `json:"final_url"`
		Steps          []struct {
			ID     string `json:"id"`
			Action string `json:"action"`
			Status string `json:"status"`
			Error  string `json:"error"`
		} `json:"steps"`
		Artifacts struct {
			ScreenshotPath string   `json:"screenshot_path"`
			Warnings       []string `json:"warnings"`
		} `json:"artifacts"`
	} `json:"synthetic_browser"`
}

func requireBrowserForTest(t *testing.T) {
	t.Helper()
	if findBrowserExecutable() == "" {
		t.Skip("Chrome/Chromium executable not available; skipping synthetic browser worker tests")
	}
}

func strPtr(v string) *string {
	return &v
}

func TestSyntheticBrowserChecker_SuccessJourney(t *testing.T) {
	requireBrowserForTest(t)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/login":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte(`
<!doctype html>
<html>
  <body>
    <input id="email" />
    <a id="continue" href="/dashboard">Continue</a>
  </body>
</html>`))
		case "/dashboard":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte(`
<!doctype html>
<html>
  <body>
    <div id="welcome">Welcome to the dashboard</div>
  </body>
</html>`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	checker := NewSyntheticBrowserChecker(t.TempDir())
	cfg := map[string]interface{}{
		"start_url": server.URL + "/login",
		"steps": []map[string]interface{}{
			{"id": "fill_email", "action": "fill", "selector": "#email", "value": "alice@example.com"},
			{"id": "go_dashboard", "action": "click", "selector": "#continue"},
			{"id": "wait_dashboard", "action": "wait_for", "selector": "#welcome"},
			{"id": "assert_visible", "action": "assert_visible", "selector": "#welcome"},
			{"id": "assert_text", "action": "assert_text", "selector": "#welcome", "value": "Welcome"},
			{"id": "assert_url", "action": "assert_url", "value": "/dashboard"},
		},
	}
	configJSON, _ := json.Marshal(cfg)

	result := checker.Check(context.Background(), configJSON, 30)
	if result.Status != "success" {
		t.Fatalf("expected success, got %s (err=%v)", result.Status, result.ErrorMessage)
	}
	if result.ErrorMessage != nil {
		t.Fatalf("expected nil error, got %v", *result.ErrorMessage)
	}
	if result.LatencyMs == nil || *result.LatencyMs <= 0 {
		t.Fatalf("expected positive latency, got %#v", result.LatencyMs)
	}
	if len(result.MetricsData) == 0 {
		t.Fatal("expected metrics_data")
	}

	var envelope syntheticBrowserMetricsTestEnvelope
	if err := json.Unmarshal(result.MetricsData, &envelope); err != nil {
		t.Fatalf("failed to decode metrics_data: %v", err)
	}
	if envelope.SyntheticBrowser.CompletedSteps != 6 {
		t.Fatalf("expected completed_steps=6, got %d", envelope.SyntheticBrowser.CompletedSteps)
	}
	if len(envelope.SyntheticBrowser.Steps) != 6 {
		t.Fatalf("expected 6 step metrics, got %d", len(envelope.SyntheticBrowser.Steps))
	}
	for i, step := range envelope.SyntheticBrowser.Steps {
		if step.Status != "success" {
			t.Fatalf("expected step %d success, got %s (error=%s)", i, step.Status, step.Error)
		}
	}
	if !strings.Contains(envelope.SyntheticBrowser.FinalURL, "/dashboard") {
		t.Fatalf("expected final URL to contain /dashboard, got %q", envelope.SyntheticBrowser.FinalURL)
	}
}

func TestSyntheticBrowserChecker_FailureModeContinueAndArtifacts(t *testing.T) {
	requireBrowserForTest(t)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`
<!doctype html>
<html>
  <body>
    <div id="ready">Ready</div>
  </body>
</html>`))
	}))
	defer server.Close()

	artifactsDir := t.TempDir()
	checker := NewSyntheticBrowserChecker(artifactsDir)
	cfg := map[string]interface{}{
		"start_url":    server.URL,
		"failure_mode": "continue",
		"artifacts": map[string]interface{}{
			"screenshot_on_failure": true,
			"trace_on_failure":      true,
			"har_on_failure":        true,
		},
		"steps": []map[string]interface{}{
			{"id": "s1", "action": "assert_visible", "selector": "#missing", "timeout_seconds": 2},
			{"id": "s2", "action": "assert_visible", "selector": "#ready", "timeout_seconds": 10},
		},
	}
	configJSON, _ := json.Marshal(cfg)

	ctx := withSyntheticBrowserMonitorID(context.Background(), "test-monitor-id")
	result := checker.Check(ctx, configJSON, 45)
	if result.Status != "failure" {
		t.Fatalf("expected failure, got %s (err=%v)", result.Status, result.ErrorMessage)
	}
	if result.ErrorMessage == nil || !strings.Contains(*result.ErrorMessage, "assert_visible failed") {
		t.Fatalf("expected assert_visible error message, got %v", result.ErrorMessage)
	}
	if len(result.MetricsData) == 0 {
		t.Fatal("expected metrics_data")
	}

	var envelope syntheticBrowserMetricsTestEnvelope
	if err := json.Unmarshal(result.MetricsData, &envelope); err != nil {
		t.Fatalf("failed to decode metrics_data: %v", err)
	}
	if envelope.SyntheticBrowser.CompletedSteps != 2 {
		t.Fatalf("expected completed_steps=2, got %d", envelope.SyntheticBrowser.CompletedSteps)
	}
	if envelope.SyntheticBrowser.FailedStepID != "s1" {
		t.Fatalf("expected failed_step_id=s1, got %q", envelope.SyntheticBrowser.FailedStepID)
	}
	if len(envelope.SyntheticBrowser.Steps) != 2 {
		t.Fatalf("expected 2 steps, got %d", len(envelope.SyntheticBrowser.Steps))
	}
	if envelope.SyntheticBrowser.Steps[0].Status != "failure" {
		t.Fatalf("expected first step to fail, got %s", envelope.SyntheticBrowser.Steps[0].Status)
	}
	if envelope.SyntheticBrowser.Steps[1].Status != "success" {
		t.Fatalf("expected second step success, got %s", envelope.SyntheticBrowser.Steps[1].Status)
	}

	screenshotPath := envelope.SyntheticBrowser.Artifacts.ScreenshotPath
	if screenshotPath == "" {
		t.Fatalf("expected screenshot path in artifacts")
	}
	if !strings.HasPrefix(screenshotPath, "test-monitor-id/") {
		t.Fatalf("expected screenshot path to be monitor-prefixed, got %q", screenshotPath)
	}
	if _, err := os.Stat(filepath.Join(artifactsDir, screenshotPath)); err != nil {
		t.Fatalf("expected screenshot file to exist at %s: %v", screenshotPath, err)
	}
	if len(envelope.SyntheticBrowser.Artifacts.Warnings) < 2 {
		t.Fatalf("expected unsupported feature warnings for trace+har, got %v", envelope.SyntheticBrowser.Artifacts.Warnings)
	}
}

func TestSyntheticBrowserChecker_MissingVariable(t *testing.T) {
	checker := NewSyntheticBrowserChecker(t.TempDir())
	cfg := map[string]interface{}{
		"start_url": "https://example.com/{{missing}}",
		"steps": []map[string]interface{}{
			{"id": "s1", "action": "goto", "url": "https://example.com"},
		},
	}
	configJSON, _ := json.Marshal(cfg)

	result := checker.Check(context.Background(), configJSON, 10)
	if result.Status != "error" {
		t.Fatalf("expected error, got %s", result.Status)
	}
	if result.ErrorMessage == nil || !strings.Contains(*result.ErrorMessage, "missing variable") {
		t.Fatalf("expected missing variable error, got %v", result.ErrorMessage)
	}
}

func TestSyntheticBrowserChecker_UsesTemplatedStepValues(t *testing.T) {
	requireBrowserForTest(t)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/landing":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte(`
<!doctype html>
<html>
  <body>
    <div data-test="ready">env=prod</div>
  </body>
</html>`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	checker := NewSyntheticBrowserChecker(t.TempDir())
	cfg := models.SyntheticBrowserMonitorConfig{
		StartURL: server.URL + "/landing",
		Variables: map[string]string{
			"selector_suffix": "ready",
			"expected_text":   "env=prod",
		},
		Steps: []models.SyntheticBrowserStepConfig{
			{ID: "s1", Action: "assert_visible", Selector: `[data-test="{{selector_suffix}}"]`},
			{ID: "s2", Action: "assert_text", Selector: `[data-test="{{selector_suffix}}"]`, Value: strPtr("{{expected_text}}")},
		},
	}
	configJSON, _ := json.Marshal(cfg)

	result := checker.Check(context.Background(), configJSON, 30)
	if result.Status != "success" {
		t.Fatalf("expected success, got %s (err=%v)", result.Status, result.ErrorMessage)
	}
}

func TestSyntheticBrowserChecker_FailFastStopsAfterFirstFailure(t *testing.T) {
	requireBrowserForTest(t)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`
<!doctype html>
<html>
  <body>
    <div id="ready">Ready</div>
  </body>
</html>`))
	}))
	defer server.Close()

	checker := NewSyntheticBrowserChecker(t.TempDir())
	cfg := map[string]interface{}{
		"start_url":    server.URL,
		"failure_mode": "fail_fast",
		"steps": []map[string]interface{}{
			{"id": "s1", "action": "assert_visible", "selector": "#missing"},
			{"id": "s2", "action": "assert_visible", "selector": "#ready"},
		},
	}
	configJSON, _ := json.Marshal(cfg)

	result := checker.Check(context.Background(), configJSON, 30)
	if result.Status != "failure" {
		t.Fatalf("expected failure, got %s (err=%v)", result.Status, result.ErrorMessage)
	}

	var envelope syntheticBrowserMetricsTestEnvelope
	if err := json.Unmarshal(result.MetricsData, &envelope); err != nil {
		t.Fatalf("failed to decode metrics_data: %v", err)
	}
	if envelope.SyntheticBrowser.CompletedSteps != 1 {
		t.Fatalf("expected completed_steps=1 with fail_fast, got %d", envelope.SyntheticBrowser.CompletedSteps)
	}
	if len(envelope.SyntheticBrowser.Steps) != 1 {
		t.Fatalf("expected one executed step, got %d", len(envelope.SyntheticBrowser.Steps))
	}
	if envelope.SyntheticBrowser.FailedStepID != "s1" {
		t.Fatalf("expected failed_step_id=s1, got %q", envelope.SyntheticBrowser.FailedStepID)
	}
}

func TestSyntheticBrowserChecker_RelativeGotoResolvesAgainstCurrentURL(t *testing.T) {
	requireBrowserForTest(t)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/start":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte(`
<!doctype html>
<html>
  <body>Start</body>
</html>`))
		case "/next":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte(`
<!doctype html>
<html>
  <body>
    <div id="done">Done</div>
  </body>
</html>`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	checker := NewSyntheticBrowserChecker(t.TempDir())
	cfg := map[string]interface{}{
		"start_url": server.URL + "/start",
		"steps": []map[string]interface{}{
			{"id": "s1", "action": "goto", "url": "/next"},
			{"id": "s2", "action": "assert_visible", "selector": "#done"},
			{"id": "s3", "action": "assert_url", "value": "/next"},
		},
	}
	configJSON, _ := json.Marshal(cfg)

	result := checker.Check(context.Background(), configJSON, 30)
	if result.Status != "success" {
		t.Fatalf("expected success, got %s (err=%v)", result.Status, result.ErrorMessage)
	}

	var envelope syntheticBrowserMetricsTestEnvelope
	if err := json.Unmarshal(result.MetricsData, &envelope); err != nil {
		t.Fatalf("failed to decode metrics_data: %v", err)
	}
	if !strings.Contains(envelope.SyntheticBrowser.FinalURL, "/next") {
		t.Fatalf("expected final URL to contain /next, got %q", envelope.SyntheticBrowser.FinalURL)
	}
}

func TestSyntheticBrowserChecker_UnsupportedActionReturnsError(t *testing.T) {
	requireBrowserForTest(t)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte("<!doctype html><html><body>ok</body></html>"))
	}))
	defer server.Close()

	checker := NewSyntheticBrowserChecker(t.TempDir())
	cfg := map[string]interface{}{
		"start_url": server.URL,
		"steps": []map[string]interface{}{
			{"id": "s1", "action": "hover", "selector": "body"},
		},
	}
	configJSON, _ := json.Marshal(cfg)

	result := checker.Check(context.Background(), configJSON, 30)
	if result.Status != "error" {
		t.Fatalf("expected error, got %s", result.Status)
	}
	if result.ErrorMessage == nil || !strings.Contains(*result.ErrorMessage, "unsupported action") {
		t.Fatalf("expected unsupported action error message, got %v", result.ErrorMessage)
	}

	var envelope syntheticBrowserMetricsTestEnvelope
	if err := json.Unmarshal(result.MetricsData, &envelope); err != nil {
		t.Fatalf("failed to decode metrics_data: %v", err)
	}
	if len(envelope.SyntheticBrowser.Steps) != 1 {
		t.Fatalf("expected one step metric, got %d", len(envelope.SyntheticBrowser.Steps))
	}
	if envelope.SyntheticBrowser.Steps[0].Status != "error" {
		t.Fatalf("expected step status error, got %s", envelope.SyntheticBrowser.Steps[0].Status)
	}
}

func TestSyntheticBrowserChecker_StartURLMustBeAbsolute(t *testing.T) {
	checker := NewSyntheticBrowserChecker(t.TempDir())
	cfg := map[string]interface{}{
		"start_url": "/relative",
		"steps": []map[string]interface{}{
			{"id": "s1", "action": "goto", "url": "https://example.com"},
		},
	}
	configJSON, _ := json.Marshal(cfg)

	result := checker.Check(context.Background(), configJSON, 10)
	if result.Status != "error" {
		t.Fatalf("expected error, got %s", result.Status)
	}
	if result.ErrorMessage == nil || !strings.Contains(*result.ErrorMessage, "start_url must be absolute") {
		t.Fatalf("expected absolute URL validation error, got %v", result.ErrorMessage)
	}
}
