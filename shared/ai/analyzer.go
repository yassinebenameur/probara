package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/google/uuid"
	"sort"
)

func (a *localAnalyzer) Analyze(ctx context.Context, in AnalysisInput) (AnalysisResult, error) {
	// Model requests contain only measured summaries and opaque correlation IDs.
	// Monitor names and incident free text stay local to the process.
	decisions := []Decision{}
	monitors := []MonitorContext{}
	incomplete := append([]string{}, in.Incomplete...)
	for _, m := range in.Monitors {
		if !m.Affected {
			continue
		}
		e := m.Summary
		if e.Checks == 0 && len(m.RecentChecks) > 0 {
			e = SummarizeChecks(m.RecentChecks)
		}
		m.Summary = e
		if e.MissingChecks {
			incomplete = append(incomplete, m.Ref+": missing checks")
		}
		if e.Conflict {
			incomplete = append(incomplete, m.Ref+": conflicting failure domains")
		}
		if m.Type == "agent" && !e.HostMetricsAvailable {
			incomplete = append(incomplete, m.Ref+": missing host metrics")
		}
		monitors = append(monitors, m)
		decisions = append(decisions, Decision{ID: uuid.NewString(), Task: "rca", Evidence: e, Choices: choices("rca")})
	}
	result := AnalysisResult{Summary: "Insufficient evidence to select a supported cause.", ProbableRootCause: "Insufficient evidence", ContributingFactors: []string{}, RecommendedActions: catalogue.RCA["insufficient_evidence"].Actions, Confidence: "low"}
	if len(decisions) == 0 {
		result.Metadata = metadata(ModelIdentity{Engine: "laya-cpu"}, "insufficient_evidence", 0, nil, append(incomplete, "No affected monitors with retained evidence"))
		return result, nil
	}
	var out BatchResult
	for start := 0; start < len(decisions); start += 50 {
		end := min(start+50, len(decisions))
		batch, err := a.decide(ctx, decisions[start:end])
		if err != nil {
			return AnalysisResult{}, err
		}
		if start == 0 {
			out.Model = batch.Model
			out.Thresholds = batch.Thresholds
		} else if out.Model != batch.Model || out.Thresholds["rca"] != batch.Thresholds["rca"] {
			return AnalysisResult{}, fmt.Errorf("local AI model changed during incident analysis; retry")
		}
		out.Results = append(out.Results, batch.Results...)
	}
	refs := []EvidenceReference{}
	votes := map[string]int{}
	scores := map[string]float64{}
	for i, r := range out.Results {
		h, score := best(r)
		e := monitors[i].Summary
		refs = append(refs, EvidenceFacts(monitors[i].Ref, e)...)
		if score < out.Thresholds["rca"] || !e.Supports(h) {
			h = "insufficient_evidence"
		}
		votes[h]++
		scores[h] += score
		if h != "insufficient_evidence" {
			result.ContributingFactors = append(result.ContributingFactors, fmt.Sprintf("%s: suspected %s.", monitors[i].Name, catalogue.RCA[h].Title))
		}
	}
	keys := []string{}
	for h := range votes {
		if h != "insufficient_evidence" {
			keys = append(keys, h)
		}
	}
	sort.Strings(keys)
	h := "insufficient_evidence"
	score := 0.0
	// Different supported monitor-level diagnoses are presented as symptoms,
	// without inventing a single incident-wide root cause.
	if len(keys) == 1 && votes[keys[0]]*2 >= len(monitors) {
		h = keys[0]
		score = scores[h] / float64(votes[h])
	} else if len(keys) > 1 {
		incomplete = append(incomplete, "Multiple supported hypotheses; no single incident cause established")
	}
	if h != "insufficient_evidence" {
		entry := catalogue.RCA[h]
		result.Summary = fmt.Sprintf("%d of %d affected monitor decisions support a suspected %s. These are observed symptoms; the underlying cause needs confirmation.", votes[h], len(monitors), entry.Title)
		result.ProbableRootCause = "Suspected " + entry.Title
		result.RecommendedActions = entry.Actions
		result.Confidence = confidence(score)
	}
	result.Model = out.Model.Version
	result.Metadata = metadata(out.Model, h, score, refs, incomplete)
	b, err := json.Marshal(refs)
	if err != nil {
		return AnalysisResult{}, err
	}
	result.Evidence = b
	return result, nil
}
