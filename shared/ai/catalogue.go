package ai

import (
	_ "embed"
	"encoding/json"
	"sort"
)

const CatalogueVersion = "probara-evidence-v1"

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
		if (out[i].Key == "insufficient_evidence") != (out[j].Key == "insufficient_evidence") {
			return out[j].Key == "insufficient_evidence"
		}
		if out[i].Title != out[j].Title {
			return out[i].Title < out[j].Title
		}
		return out[i].Key < out[j].Key
	})
	return out
}

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
	if h == "insufficient_evidence" {
		outcome = "insufficient_evidence"
	}
	if refs == nil {
		refs = []EvidenceReference{}
	}
	if incomplete == nil {
		incomplete = []string{}
	}
	return Metadata{Engine: model.Engine, ModelVersion: model.Version, ModelSHA256: model.SHA256, CatalogueVersion: CatalogueVersion, Hypothesis: h, Score: score, Outcome: outcome, Evidence: refs, Incomplete: incomplete}
}
