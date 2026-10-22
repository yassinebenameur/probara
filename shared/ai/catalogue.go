package ai

import (
	_ "embed"
	"encoding/json"
	"sort"
)

const CatalogueVersion = "probara-evidence-v1"

// RCAInsufficientEvidence is the rca key the model answers when it abstains.
// It is a catalogue entry like any other, but never a named cause.
const RCAInsufficientEvidence = "insufficient_evidence"

//go:embed catalogue.json
var catalogueJSON []byte

type catalogueEntry struct {
	Description string   `json:"description"`
	Title       string   `json:"title"`
	Actions     []string `json:"actions"`
}

var catalogue struct {
	Version    string                    `json:"version"`
	RCA        map[string]catalogueEntry `json:"rca"`
	Dependency map[string]catalogueEntry `json:"dependency"`
}

func init() {
	if err := json.Unmarshal(catalogueJSON, &catalogue); err != nil {
		panic(err)
	}
}
func choices(task string) map[string]string {
	entries := catalogue.RCA
	if task == "dependency" {
		entries = catalogue.Dependency
	}
	out := map[string]string{}
	for k, v := range entries {
		out[k] = v.Description
	}
	return out
}

// Choices returns the running catalogue's choice keys and descriptions for a
// task ("rca" or "dependency"), as sent in Decision.Choices.
func Choices(task string) map[string]string { return choices(task) }

// ChoiceTitle is the operator-facing title of a choice. RCA entries carry
// titles; dependency entries do not, so they fall back to the key, as does
// any key the running catalogue does not know.
func ChoiceTitle(task, key string) string {
	entries := catalogue.RCA
	if task == "dependency" {
		entries = catalogue.Dependency
	}
	if e, ok := entries[key]; ok && e.Title != "" {
		return e.Title
	}
	return key
}

// ChoiceOption is one selectable answer for an operator label form.
type ChoiceOption struct {
	Key   string `json:"key"`
	Title string `json:"title"`
}

// ChoiceOptions lists a task's choices sorted by title, with
// "insufficient_evidence" last.
func ChoiceOptions(task string) []ChoiceOption {
	out := []ChoiceOption{}
	for k := range choices(task) {
		out = append(out, ChoiceOption{Key: k, Title: ChoiceTitle(task, k)})
	}
	sort.Slice(out, func(i, j int) bool {
		if (out[i].Key == RCAInsufficientEvidence) != (out[j].Key == RCAInsufficientEvidence) {
			return out[j].Key == RCAInsufficientEvidence
		}
		if out[i].Title != out[j].Title {
			return out[i].Title < out[j].Title
		}
		return out[i].Key < out[j].Key
	})
	return out
}

// IsNamedCause reports whether key is an rca cause the running catalogue
// knows, other than the abstention. Every consumer that acts on a triage or
// analysis hypothesis (suspected-cause notifications, and anything that
// groups or reports by cause) gates on it, so an unknown key from an older
// catalogue or an abstention is never presented as a cause.
func IsNamedCause(key string) bool {
	if key == RCAInsufficientEvidence {
		return false
	}
	_, ok := catalogue.RCA[key]
	return ok
}

// NamedCauses lists every key IsNamedCause accepts, sorted, for SQL filters
// (`hypothesis = ANY($n)`).
func NamedCauses() []string {
	out := make([]string, 0, len(catalogue.RCA))
	for k := range catalogue.RCA {
		if IsNamedCause(k) {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

// RCAChoice returns a named cause's operator-facing title and a copy of its
// suggested actions; ok is false for the abstention and for keys the running
// catalogue does not know.
func RCAChoice(key string) (title string, actions []string, ok bool) {
	if !IsNamedCause(key) {
		return "", nil, false
	}
	e := catalogue.RCA[key]
	title = e.Title
	if title == "" {
		title = key
	}
	return title, append([]string(nil), e.Actions...), true
}

// IsConfident reports whether a confidence level is "medium" or "high", i.e.
// a calibrated score of at least 0.7 (see confidence). Low-confidence leads
// stay on the alert's pill and never interrupt anyone.
func IsConfident(level string) bool {
	return level == "medium" || level == "high"
}

// ConfidentLevels lists the levels IsConfident accepts, for SQL filters.
func ConfidentLevels() []string { return []string{"medium", "high"} }

func best(r DecisionResult) (string, float64) {
	key := ""
	score := 0.0
	for k, p := range r.Probabilities {
		if p > score || (p == score && (key == "" || k < key)) {
			key, score = k, p
		}
	}
	return key, score
}

func confidence(score float64) string {
	if score >= .9 {
		return "high"
	}
	if score >= .7 {
		return "medium"
	}
	return "low"
}

type EvidenceReference struct {
	Ref        string `json:"ref"`
	MonitorRef string `json:"monitor_ref,omitempty"`
	Fact       string `json:"fact"`
}
type Metadata struct {
	Engine           string              `json:"engine"`
	ModelVersion     string              `json:"model_version,omitempty"`
	ModelSHA256      string              `json:"model_sha256,omitempty"`
	CatalogueVersion string              `json:"catalogue_version,omitempty"`
	Hypothesis       string              `json:"hypothesis,omitempty"`
	Score            float64             `json:"calibrated_score,omitempty"`
	Outcome          string              `json:"outcome"`
	Evidence         []EvidenceReference `json:"evidence_references"`
	Incomplete       []string            `json:"incomplete_evidence"`
}

func metadata(model ModelIdentity, h string, score float64, refs []EvidenceReference, incomplete []string) Metadata {
	outcome := "supported"
	if h == RCAInsufficientEvidence {
		outcome = RCAInsufficientEvidence
	}
	if refs == nil {
		refs = []EvidenceReference{}
	}
	// Notes come from several collectors (context, analyzer); show each once.
	seen := map[string]bool{}
	unique := []string{}
	for _, note := range incomplete {
		if !seen[note] {
			seen[note] = true
			unique = append(unique, note)
		}
	}
	return Metadata{Engine: model.Engine, ModelVersion: model.Version, ModelSHA256: model.SHA256, CatalogueVersion: CatalogueVersion, Hypothesis: h, Score: score, Outcome: outcome, Evidence: refs, Incomplete: unique}
}
