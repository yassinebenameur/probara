package ai

import (
	"context"
	"strings"
	"testing"
)

func TestParseResult(t *testing.T) {
	tests := []struct {
		name        string
		content     string
		wantErr     bool
		wantSummary string
		wantConf    string
		wantFactors int
	}{
		{
			name:        "plain json",
			content:     `{"summary":"DB down","probable_root_cause":"primary unreachable","contributing_factors":["x","y"],"recommended_actions":["failover"],"confidence":"high"}`,
			wantSummary: "DB down",
			wantConf:    "high",
			wantFactors: 2,
		},
		{
			name:        "fenced json",
			content:     "```json\n{\"summary\":\"s\",\"probable_root_cause\":\"r\",\"confidence\":\"LOW\"}\n```",
			wantSummary: "s",
			wantConf:    "low",
		},
		{
			name:        "surrounding prose",
			content:     "Here is the analysis:\n{\"summary\":\"s\",\"probable_root_cause\":\"r\",\"confidence\":\"moderate\"}\nHope that helps.",
			wantSummary: "s",
			wantConf:    "medium",
		},
		{
			name:     "unknown confidence defaults to medium",
			content:  `{"summary":"s","probable_root_cause":"r","confidence":"banana"}`,
			wantConf: "medium",
		},
		{
			name:    "no json object",
			content: "I cannot analyze this.",
			wantErr: true,
		},
		{
			name:    "missing summary and root cause",
			content: `{"confidence":"high"}`,
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseResult(tc.content)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got result %+v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tc.wantSummary != "" && got.Summary != tc.wantSummary {
				t.Errorf("summary = %q, want %q", got.Summary, tc.wantSummary)
			}
			if tc.wantConf != "" && got.Confidence != tc.wantConf {
				t.Errorf("confidence = %q, want %q", got.Confidence, tc.wantConf)
			}
			if tc.wantFactors != 0 && len(got.ContributingFactors) != tc.wantFactors {
				t.Errorf("factors = %d, want %d", len(got.ContributingFactors), tc.wantFactors)
			}
		})
	}
}

type fakeProvider struct {
	content   string
	model     string
	gotReq    CompletionRequest
	returnErr error
}

func (f *fakeProvider) Name() string { return "fake" }
func (f *fakeProvider) Complete(_ context.Context, req CompletionRequest) (CompletionResponse, error) {
	f.gotReq = req
	if f.returnErr != nil {
		return CompletionResponse{}, f.returnErr
	}
	return CompletionResponse{Content: f.content, Model: f.model}, nil
}

func TestAnalyze_WithFakeProvider(t *testing.T) {
	fp := &fakeProvider{
		content: `{"summary":"TLS expiry","probable_root_cause":"cert expired","contributing_factors":["cert rotated late"],"recommended_actions":["renew cert"],"confidence":"high","evidence":["days_until_expiry=0"]}`,
		model:   "test-model-xl",
	}
	a := &llmAnalyzer{provider: fp, model: "configured-model", maxTokens: 512}

	result, err := a.Analyze(context.Background(), AnalysisInput{
		Incident: IncidentContext{Title: "API down"},
	})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if result.ProbableRootCause != "cert expired" {
		t.Errorf("root cause = %q", result.ProbableRootCause)
	}
	if result.Model != "test-model-xl" {
		t.Errorf("model = %q, want response model", result.Model)
	}
	// The schema and request fields must be passed through to the provider.
	if len(fp.gotReq.JSONSchema) == 0 {
		t.Error("expected JSONSchema to be set on the request")
	}
	if fp.gotReq.Model != "configured-model" {
		t.Errorf("request model = %q, want configured-model", fp.gotReq.Model)
	}
	if !strings.Contains(fp.gotReq.User, "API down") {
		t.Error("expected incident evidence in user prompt")
	}
}

func TestAnalyze_ProviderModelFallback(t *testing.T) {
	fp := &fakeProvider{content: `{"summary":"s","probable_root_cause":"r","confidence":"low"}`}
	a := &llmAnalyzer{provider: fp, model: "configured-model", maxTokens: 512}
	result, err := a.Analyze(context.Background(), AnalysisInput{})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if result.Model != "configured-model" {
		t.Errorf("model = %q, want configured-model fallback", result.Model)
	}
}

func TestNewAnalyzer_NotConfigured(t *testing.T) {
	if _, err := NewAnalyzer(Config{BaseURL: ""}); err != ErrNotConfigured {
		t.Errorf("err = %v, want ErrNotConfigured", err)
	}
}

func TestNewAnalyzer_RequiresModel(t *testing.T) {
	if _, err := NewAnalyzer(Config{BaseURL: "http://x", Model: ""}); err == nil {
		t.Error("expected error when model is empty")
	}
}

func TestNewAnalyzer_UnknownProvider(t *testing.T) {
	if _, err := NewAnalyzer(Config{BaseURL: "http://x", Model: "m", Provider: "weirdvendor"}); err == nil {
		t.Error("expected error for unknown provider")
	}
}
