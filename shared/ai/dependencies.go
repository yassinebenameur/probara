package ai

import (
	"context"
	"encoding/json"
	"fmt"
)

// DependencyAdvisor suggests monitor dependency edges from observed signals
// (currently co-firing alerts). It is a separate capability from
// RootCauseAnalyzer but shares the same Provider/transport.
type DependencyAdvisor interface {
	SuggestDependencies(ctx context.Context, in DependencyInput) (DependencySuggestions, error)
}

// DependencyInput is the evidence handed to the model. Monitors are referenced
// by an opaque Ref (not name) so the model can't invent identifiers — the
// caller maps refs back to monitor IDs and discards anything unrecognized.
type DependencyInput struct {
	Monitors             []DependencyMonitorRef `json:"monitors"`
	ExistingDependencies []DependencyEdgeRef    `json:"existing_dependencies,omitempty"`
	CoFiringAlerts       []CoFiringPair         `json:"co_firing_alerts,omitempty"`
}

// DependencyMonitorRef is a monitor the model may reference. Tags and Groups
// give the model naming/clustering signals beyond co-firing alerts.
type DependencyMonitorRef struct {
	Ref    string   `json:"ref"`
	Name   string   `json:"name"`
	Type   string   `json:"type"`
	Tags   []string `json:"tags,omitempty"`
	Groups []string `json:"groups,omitempty"`
}

// DependencyEdgeRef is an existing edge: Monitor depends on DependsOn. Provided
// so the model doesn't re-suggest edges that already exist.
type DependencyEdgeRef struct {
	Monitor   string `json:"monitor"`
	DependsOn string `json:"depends_on"`
}

// CoFiringPair captures that Earlier's alerts tend to fire at or shortly before
// Later's, within a short window — a hint that Later may depend on Earlier.
type CoFiringPair struct {
	Earlier           string `json:"earlier_monitor"`
	Later             string `json:"later_monitor"`
	TimesCoFired      int    `json:"times_co_fired"`
	MedianLeadSeconds int    `json:"median_lead_seconds"`
}

// DependencySuggestion proposes that Monitor depends on DependsOn.
type DependencySuggestion struct {
	Monitor    string `json:"monitor"`
	DependsOn  string `json:"depends_on"`
	Reason     string `json:"reason"`
	Confidence string `json:"confidence"`
}

// DependencySuggestions is the model's output.
type DependencySuggestions struct {
	Suggestions []DependencySuggestion `json:"suggestions"`
	Model       string                 `json:"-"`
}

var dependencyJSONSchema = json.RawMessage(`{
  "type": "object",
  "additionalProperties": false,
  "properties": {
    "suggestions": {
      "type": "array",
      "items": {
        "type": "object",
        "additionalProperties": false,
        "properties": {
          "monitor": {"type": "string"},
          "depends_on": {"type": "string"},
          "reason": {"type": "string"},
          "confidence": {"type": "string", "enum": ["high", "medium", "low"]}
        },
        "required": ["monitor", "depends_on", "reason", "confidence"]
      }
    }
  },
  "required": ["suggestions"]
}`)

const dependencySystemPrompt = `You infer dependency relationships between infrastructure monitors.

You are given a set of monitors (each with an opaque "ref", a name, a type, optional tags, and optional
group memberships), the dependency edges that already exist, and co-firing alert statistics. Propose
edges of the form "monitor depends_on depends_on" — the first depends on the second, i.e. the second is
upstream.

Signals, strongest to weakest:
1. Co-firing alerts. Monitors whose alerts fire in the same short window are likely related; when one
   consistently fails BEFORE the other (positive median_lead_seconds for the "earlier" monitor), the
   earlier one is the upstream (the depends_on). This is the strongest signal.
2. Shared group membership and overlapping tags. Monitors in the same group or sharing service tags
   often belong to the same system and may share an upstream — a clustering hint, not proof of direction.
3. Naming patterns. A shared service token or tiers implied by names (e.g. "checkout-api" depending on
   "checkout-db", or an app monitor depending on its database/DNS/TCP monitor for the same service).

Calibrate confidence to the evidence:
- "high": strong, consistent co-firing with a clear lead direction, ideally corroborated by tags/group/name.
- "medium": co-firing without a clear lead, OR a clear naming/tier relationship backed by shared tags/group.
- "low": inference from names/tags/groups alone, with no co-firing support.
Agreement across signals raises confidence; a single weak signal stays "low".

Rules:
- Reference monitors ONLY by their exact "ref" value. Never invent refs.
- Do NOT repeat edges already in existing_dependencies, and never suggest a self-edge.
- Favor precision over recall. Do not connect monitors merely because they share a generic tag like
  "prod". It is fine to return few or no suggestions.
- Give a one-sentence reason citing the specific evidence (which signal, and the direction rationale).

Respond with a single JSON object: {"suggestions": [{"monitor": "<ref>", "depends_on": "<ref>", "reason": "...", "confidence": "high|medium|low"}]}`

// SuggestDependencies builds the prompt, calls the provider, and parses the
// suggestions defensively. Monitor refs are validated by the caller.
func (a *llmAnalyzer) SuggestDependencies(ctx context.Context, in DependencyInput) (DependencySuggestions, error) {
	payload, err := json.MarshalIndent(in, "", "  ")
	if err != nil {
		return DependencySuggestions{}, fmt.Errorf("ai: marshal dependency input: %w", err)
	}

	resp, err := a.provider.Complete(ctx, CompletionRequest{
		Model:      a.model,
		System:     dependencySystemPrompt,
		User:       "Signals:\n" + string(payload),
		MaxTokens:  a.maxTokens,
		JSONSchema: dependencyJSONSchema,
		SchemaName: "dependency_suggestions",
	})
	if err != nil {
		return DependencySuggestions{}, err
	}

	out, err := parseDependencySuggestions(resp.Content)
	if err != nil {
		return DependencySuggestions{}, err
	}
	out.Model = resp.Model
	if out.Model == "" {
		out.Model = a.model
	}
	return out, nil
}

func parseDependencySuggestions(content string) (DependencySuggestions, error) {
	raw := extractJSONObject(content)
	if raw == "" {
		return DependencySuggestions{}, fmt.Errorf("ai: model returned no JSON object")
	}
	var parsed struct {
		Suggestions []DependencySuggestion `json:"suggestions"`
	}
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		return DependencySuggestions{}, fmt.Errorf("ai: parse dependency JSON: %w", err)
	}
	for i := range parsed.Suggestions {
		parsed.Suggestions[i].Confidence = normalizeConfidence(parsed.Suggestions[i].Confidence)
	}
	return DependencySuggestions{Suggestions: parsed.Suggestions}, nil
}

// NewDependencyAdvisor builds a DependencyAdvisor from config. Same provider
// construction and ErrNotConfigured semantics as NewAnalyzer.
func NewDependencyAdvisor(cfg Config) (DependencyAdvisor, error) {
	analyzer, err := NewAnalyzer(cfg)
	if err != nil {
		return nil, err
	}
	return analyzer.(*llmAnalyzer), nil
}
