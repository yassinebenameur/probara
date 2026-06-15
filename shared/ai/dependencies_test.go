package ai

import (
	"context"
	"strings"
	"testing"
)

func TestParseDependencySuggestions(t *testing.T) {
	content := "```json\n{\"suggestions\":[" +
		"{\"monitor\":\"m1\",\"depends_on\":\"m0\",\"reason\":\"m0 fails first\",\"confidence\":\"HIGH\"}," +
		"{\"monitor\":\"m2\",\"depends_on\":\"m0\",\"reason\":\"co-fires\",\"confidence\":\"weird\"}" +
		"]}\n```"
	out, err := parseDependencySuggestions(content)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(out.Suggestions) != 2 {
		t.Fatalf("got %d suggestions", len(out.Suggestions))
	}
	if out.Suggestions[0].Confidence != "high" {
		t.Errorf("confidence normalize = %q", out.Suggestions[0].Confidence)
	}
	if out.Suggestions[1].Confidence != "medium" {
		t.Errorf("unknown confidence should default to medium, got %q", out.Suggestions[1].Confidence)
	}
}

func TestSuggestDependencies_WithFakeProvider(t *testing.T) {
	fp := &fakeProvider{
		content: `{"suggestions":[{"monitor":"m1","depends_on":"m0","reason":"r","confidence":"high"}]}`,
		model:   "served",
	}
	a := &llmAnalyzer{provider: fp, model: "cfg-model", maxTokens: 256}

	out, err := a.SuggestDependencies(context.Background(), DependencyInput{
		Monitors: []DependencyMonitorRef{{Ref: "m0", Name: "db", Type: "tcp"}, {Ref: "m1", Name: "api", Type: "http"}},
		CoFiringAlerts: []CoFiringPair{
			{Earlier: "m0", Later: "m1", TimesCoFired: 5, MedianLeadSeconds: 30},
		},
	})
	if err != nil {
		t.Fatalf("SuggestDependencies: %v", err)
	}
	if len(out.Suggestions) != 1 || out.Suggestions[0].Monitor != "m1" || out.Suggestions[0].DependsOn != "m0" {
		t.Fatalf("unexpected suggestions: %+v", out.Suggestions)
	}
	if out.Model != "served" {
		t.Errorf("model = %q", out.Model)
	}
	if len(fp.gotReq.JSONSchema) == 0 {
		t.Error("expected JSONSchema set on request")
	}
	if !strings.Contains(fp.gotReq.User, "co_firing_alerts") {
		t.Error("expected co-firing evidence in prompt")
	}
}
