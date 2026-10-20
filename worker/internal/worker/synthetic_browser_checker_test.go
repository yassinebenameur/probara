package worker

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
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

func strPtr(v string) *string {
	return &v
}

func errMsgForTest(msg *string) string {
	if msg == nil {
		return "<nil>"
	}
	return *msg
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
