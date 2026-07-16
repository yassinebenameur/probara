package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// resultJSONSchema constrains the model output. Providers that support strict
// JSON schemas use it directly; for those that don't, the same shape is also
// described in the system prompt and parsed defensively.
var resultJSONSchema = json.RawMessage(`{
  "type": "object",
  "additionalProperties": false,
  "properties": {
    "summary": {"type": "string"},
    "probable_root_cause": {"type": "string"},
    "contributing_factors": {"type": "array", "items": {"type": "string"}},
    "recommended_actions": {"type": "array", "items": {"type": "string"}},
    "confidence": {"type": "string", "enum": ["high", "medium", "low"]},
    "evidence": {"type": "array", "items": {"type": "string"}}
  },
  "required": ["summary", "probable_root_cause", "contributing_factors", "recommended_actions", "confidence"]
}`)

const systemPrompt = `You are an SRE assistant that diagnoses the root cause of monitoring incidents.
You are given structured evidence about an incident: the affected monitors and their configuration,
recent check results (including timing breakdowns such as DNS/connect/TLS/TTFB and TLS certificate
details), the alerts that fired, the dependency-graph root cause already computed by the system, and
any active maintenance windows.

Reason over the evidence and produce a concise, actionable diagnosis. Prefer concrete, evidence-backed
conclusions (e.g. "TLS handshake latency tripled and the cert serial changed 10 minutes before the
first failure") over generic statements. If active maintenance covers the affected monitors, factor
that in rather than blaming expected downtime.

Respond with a single JSON object and nothing else, matching exactly this shape:
{
  "summary": "<one short paragraph a responder can read at a glance>",
  "probable_root_cause": "<the single most likely root cause>",
  "contributing_factors": ["<factor>", ...],
  "recommended_actions": ["<next step>", ...],
  "confidence": "high" | "medium" | "low",
  "evidence": ["<specific signal you relied on>", ...]
}`

// llmAnalyzer is the default RootCauseAnalyzer: it builds the prompt, delegates
// the round-trip to a Provider, and parses the result defensively.
type llmAnalyzer struct {
	provider  Provider
	model     string
	maxTokens int
}

func (a *llmAnalyzer) Analyze(ctx context.Context, in AnalysisInput) (AnalysisResult, error) {
	evidence, err := json.MarshalIndent(in, "", "  ")
	if err != nil {
		return AnalysisResult{}, fmt.Errorf("ai: marshal analysis input: %w", err)
	}

	resp, err := a.provider.Complete(ctx, CompletionRequest{
		Model:      a.model,
		System:     systemPrompt,
		User:       "Incident evidence:\n" + string(evidence),
		MaxTokens:  a.maxTokens,
		JSONSchema: resultJSONSchema,
		SchemaName: "incident_root_cause",
	})
	if err != nil {
		return AnalysisResult{}, err
	}

	result, err := parseResult(resp.Content)
	if err != nil {
		return AnalysisResult{}, err
	}
	result.Model = resp.Model
	if result.Model == "" {
		result.Model = a.model
	}
	return result, nil
}

// parseResult extracts the JSON object from the model's text and normalizes it.
// It tolerates code fences and surrounding prose so providers that ignore JSON
// mode still work.
func parseResult(content string) (AnalysisResult, error) {
	raw := extractJSONObject(content)
	if raw == "" {
		return AnalysisResult{}, fmt.Errorf("ai: model returned no JSON object")
	}

	// "evidence" may come back as either an array of strings or a free-form
	// object; accept both by decoding it into json.RawMessage.
	var parsed struct {
		Summary             string          `json:"summary"`
		ProbableRootCause   string          `json:"probable_root_cause"`
		ContributingFactors []string        `json:"contributing_factors"`
		RecommendedActions  []string        `json:"recommended_actions"`
		Confidence          string          `json:"confidence"`
		Evidence            json.RawMessage `json:"evidence"`
	}
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		return AnalysisResult{}, fmt.Errorf("ai: parse model JSON: %w", err)
	}

	result := AnalysisResult{
		Summary:             strings.TrimSpace(parsed.Summary),
		ProbableRootCause:   strings.TrimSpace(parsed.ProbableRootCause),
		ContributingFactors: parsed.ContributingFactors,
		RecommendedActions:  parsed.RecommendedActions,
		Confidence:          normalizeConfidence(parsed.Confidence),
		Evidence:            parsed.Evidence,
	}
	if result.Summary == "" && result.ProbableRootCause == "" {
		return AnalysisResult{}, fmt.Errorf("ai: model JSON missing summary and root cause")
	}
	return result, nil
}

// extractJSONObject returns the substring from the first '{' to the last '}',
// stripping common ```json code fences first.
func extractJSONObject(s string) string {
	s = strings.TrimSpace(s)
	if fenced := stripCodeFence(s); fenced != "" {
		s = fenced
	}
	start := strings.IndexByte(s, '{')
	end := strings.LastIndexByte(s, '}')
	if start < 0 || end < start {
		return ""
	}
	return s[start : end+1]
}

func stripCodeFence(s string) string {
	if !strings.HasPrefix(s, "```") {
		return ""
	}
	// Drop the opening fence line (``` or ```json) and the trailing fence.
	if nl := strings.IndexByte(s, '\n'); nl >= 0 {
		s = s[nl+1:]
	}
	if idx := strings.LastIndex(s, "```"); idx >= 0 {
		s = s[:idx]
	}
	return strings.TrimSpace(s)
}

func normalizeConfidence(c string) string {
	switch strings.ToLower(strings.TrimSpace(c)) {
	case "high":
		return "high"
	case "low":
		return "low"
	case "medium", "med", "moderate":
		return "medium"
	default:
		return "medium"
	}
}
