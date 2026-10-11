package ai

import (
	"context"
	"fmt"
	"github.com/google/uuid"
	"sort"
	"strings"
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
	ReverseCoFires    int    `json:"reverse_co_fires"`
}

// DependencySuggestion proposes that Monitor depends on DependsOn.
type DependencySuggestion struct {
	Monitor    string   `json:"monitor"`
	DependsOn  string   `json:"depends_on"`
	Reason     string   `json:"reason"`
	Confidence string   `json:"confidence"`
	Metadata   Metadata `json:"metadata"`
}

// DependencySuggestions is the model's output.
type DependencySuggestions struct {
	Suggestions []DependencySuggestion `json:"suggestions"`
	Model       string                 `json:"-"`
	Analyzed    int                    `json:"analyzed_pairs"`
	Abstained   int                    `json:"abstained_pairs"`
}

// PairEvidence contains measurements and boolean corroboration, never names.
type PairEvidence struct {
	CoFires        int  `json:"co_fires"`
	ALeadsSeconds  int  `json:"a_leads_seconds"`
	ReverseCoFires int  `json:"reverse_co_fires"`
	SharedService  bool `json:"shared_service"`
	SharedGroup    bool `json:"shared_group"`
	NamingSignal   bool `json:"naming_signal"`
	CommonUpstream bool `json:"common_upstream"`
}
type candidate struct {
	a, b string
	e    PairEvidence
}

func intersection(a, b []string, ignoreGeneric bool) bool {
	for _, x := range a {
		for _, y := range b {
			if x == y && x != "" {
				if ignoreGeneric {
					switch strings.ToLower(x) {
					case "prod", "production", "dev", "staging", "test":
						continue
					}
				}
				return true
			}
		}
	}
	return false
}
func naming(a, b string) bool {
	tokens := func(s string) []string {
		return strings.FieldsFunc(strings.ToLower(s), func(r rune) bool { return r == '-' || r == '_' || r == ' ' || r == '.' })
	}
	for _, x := range tokens(a) {
		if len(x) < 3 {
			continue
		}
		switch x {
		case "http", "tcp", "dns", "api", "db", "prod", "dev", "server", "monitor":
			continue
		}
		for _, y := range tokens(b) {
			if x == y {
				return true
			}
		}
	}
	return false
}

// maxCandidatePairs is the per-request decision budget of SuggestDependencies.
const maxCandidatePairs = 50

// CandidatePairs returns the pairs the advisor sends, best evidence first,
// capped at the decision budget. Pairs are oriented a < b by ref.
func CandidatePairs(in DependencyInput) []candidate {
	out := allCandidatePairs(in)
	if len(out) > maxCandidatePairs {
		out = out[:maxCandidatePairs]
	}
	return out
}

// PairDecisionFor returns the evidence the advisor would send for the pair
// {refX, refY}, oriented as CandidatePairs orients it (a < b by ref), without
// the 50-pair budget. ok=false when the pair is not a candidate.
func PairDecisionFor(in DependencyInput, refX, refY string) (aRef, bRef string, e PairEvidence, ok bool) {
	if refX == refY {
		return "", "", PairEvidence{}, false
	}
	if refX > refY {
		refX, refY = refY, refX
	}
	for _, c := range allCandidatePairs(in) {
		if c.a == refX && c.b == refY {
			return c.a, c.b, c.e, true
		}
	}
	return "", "", PairEvidence{}, false
}

// allCandidatePairs is CandidatePairs without the budget. Operator labels
// (PairDecisionFor) resolve through it so a pair ranked past the budget gets
// the same evidence the advisor would compute for it.
func allCandidatePairs(in DependencyInput) []candidate {
	refs := map[string]DependencyMonitorRef{}
	for _, m := range in.Monitors {
		refs[m.Ref] = m
	}
	pairs := map[[2]string]PairEvidence{}
	key := func(a, b string) [2]string {
		if a > b {
			return [2]string{b, a}
		}
		return [2]string{a, b}
	}
	for _, p := range in.CoFiringAlerts {
		if p.Earlier == p.Later || p.TimesCoFired < 1 {
			continue
		}
		if _, ok := refs[p.Earlier]; !ok {
			continue
		}
		if _, ok := refs[p.Later]; !ok {
			continue
		}
		k := key(p.Earlier, p.Later)
		e := pairs[k]
		if e.CoFires > 0 {
			e.ReverseCoFires += p.TimesCoFired
		} else {
			e.CoFires = p.TimesCoFired
			e.ReverseCoFires = p.ReverseCoFires
			e.ALeadsSeconds = p.MedianLeadSeconds
			if p.Earlier != k[0] {
				e.ALeadsSeconds = -e.ALeadsSeconds
			}
		}
		pairs[k] = e
	}
	upstream := map[string]map[string]bool{}
	for _, edge := range in.ExistingDependencies {
		if upstream[edge.Monitor] == nil {
			upstream[edge.Monitor] = map[string]bool{}
		}
		upstream[edge.Monitor][edge.DependsOn] = true
	}
	for i, a := range in.Monitors {
		for _, b := range in.Monitors[i+1:] {
			k := key(a.Ref, b.Ref)
			e := pairs[k]
			e.SharedService = intersection(a.Tags, b.Tags, true)
			e.SharedGroup = intersection(a.Groups, b.Groups, false)
			e.NamingSignal = naming(a.Name, b.Name)
			for u := range upstream[a.Ref] {
				if upstream[b.Ref][u] {
					e.CommonUpstream = true
				}
			}
			if e.CoFires > 0 || e.SharedService || e.SharedGroup || e.NamingSignal {
				pairs[k] = e
			}
		}
	}
	out := []candidate{}
	for k, e := range pairs {
		out = append(out, candidate{k[0], k[1], e})
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.e.CoFires != b.e.CoFires {
			return a.e.CoFires > b.e.CoFires
		}
		rank := func(e PairEvidence) int {
			n := 0
			if e.SharedService {
				n += 4
			}
			if e.SharedGroup {
				n += 2
			}
			if e.NamingSignal {
				n++
			}
			return n
		}
		if rank(a.e) != rank(b.e) {
			return rank(a.e) > rank(b.e)
		}
		if a.a != b.a {
			return a.a < b.a
		}
		return a.b < b.b
	})
	return out
}
func WouldCycle(edges []DependencyEdgeRef, monitor, upstream string) bool {
	graph := map[string][]string{}
	for _, e := range edges {
		graph[e.Monitor] = append(graph[e.Monitor], e.DependsOn)
	}
	visited := map[string]bool{}
	todo := []string{upstream}
	for len(todo) > 0 {
		n := todo[len(todo)-1]
		todo = todo[:len(todo)-1]
		if n == monitor {
			return true
		}
		if visited[n] {
			continue
		}
		visited[n] = true
		todo = append(todo, graph[n]...)
	}
	return false
}
func (a *localAnalyzer) SuggestDependencies(ctx context.Context, in DependencyInput) (DependencySuggestions, error) {
	pairs := CandidatePairs(in)
	out := DependencySuggestions{Suggestions: []DependencySuggestion{}, Analyzed: len(pairs)}
	if len(pairs) == 0 {
		return out, nil
	}
	ds := []Decision{}
	for _, p := range pairs {
		ds = append(ds, Decision{ID: uuid.NewString(), Task: "dependency", Evidence: p.e, Choices: choices("dependency")})
	}
	batch, err := a.decide(ctx, ds)
	if err != nil {
		return out, err
	}
	out.Model = batch.Model.Version
	edges := append([]DependencyEdgeRef{}, in.ExistingDependencies...)
	existing := map[[2]string]bool{}
	for _, e := range edges {
		existing[[2]string{e.Monitor, e.DependsOn}] = true
	}
	for i, r := range batch.Results {
		h, score := best(r)
		p := pairs[i]
		monitor, upstream := p.a, p.b
		if h == "b_depends_a" {
			monitor, upstream = p.b, p.a
		}
		supported := p.e.CoFires >= 2 && p.e.ReverseCoFires == 0 && (p.e.SharedService || p.e.SharedGroup) && p.e.ALeadsSeconds != 0 && !p.e.CommonUpstream
		direction := (h == "a_depends_b" && p.e.ALeadsSeconds < 0) || (h == "b_depends_a" && p.e.ALeadsSeconds > 0)
		if score < batch.Thresholds["dependency"] || !supported || !direction || existing[[2]string{monitor, upstream}] || WouldCycle(edges, monitor, upstream) {
			out.Abstained++
			continue
		}
		lead := p.e.ALeadsSeconds
		if lead < 0 {
			lead = -lead
		}
		reason := fmt.Sprintf("Suspected dependency: %d co-firing alert windows in 30 days, with the proposed upstream leading by a median %d seconds", p.e.CoFires, lead)
		if p.e.SharedService {
			reason += ", corroborated by a shared service tag"
		}
		if p.e.SharedGroup {
			reason += ", corroborated by group membership"
		}
		reason += ". Timing correlation does not prove causation."
		refs := []EvidenceReference{{Ref: fmt.Sprintf("pair/%s/%s/alerts", p.a, p.b), Fact: reason}}
		out.Suggestions = append(out.Suggestions, DependencySuggestion{Monitor: monitor, DependsOn: upstream, Reason: reason, Confidence: confidence(score), Metadata: metadata(batch.Model, h, score, refs, nil)})
		edges = append(edges, DependencyEdgeRef{Monitor: monitor, DependsOn: upstream})
		existing[[2]string{monitor, upstream}] = true
	}
	return out, nil
}
func NewDependencyAdvisor(cfg Config) (DependencyAdvisor, error) {
	a, err := newLocalAnalyzer(cfg)
	if err != nil {
		// Literal nil keeps the interface nil on error (see NewAnalyzer).
		return nil, err
	}
	return a, nil
}
