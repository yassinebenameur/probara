package ai

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"
)

// This file is the Go twin of the ai-service training contract
// (ai-service/probara_ai/contract.py: validate_evidence, state_text,
// training_row). shared/ai/testdata/label-export-v1.jsonl pins the format
// (TestLabelExportFixture) and the ai-service test suite reads the same file,
// so a change here is a change to the training data both sides must accept.

// LabelInstructions is the question instruction of every training row
// (contract.training_row).
const LabelInstructions = "Select the hypothesis supported by the measured evidence; abstain when inconclusive."

// evidenceBound is contract.validate_evidence's integer bound.
const evidenceBound = 100000000

var dependencyStateLabels = map[string]string{
	"reverse_co_fires": "Reverse-direction windows",
	"shared_service":   "Shared service tag",
	"shared_group":     "Shared group",
	"naming_signal":    "Shared naming token",
	"common_upstream":  "Known common upstream",
}

// decodeEvidence strictly decodes stored evidence for a task and returns the
// canonical struct encoding plus its fields as a map (ints as int64, bools as
// bool). It ports contract.validate_evidence: unknown fields, nulls and
// non-integer numbers are rejected, integers are bounded, only
// a_leads_seconds may be negative, and rca needs failures <= checks.
func decodeEvidence(task string, evidence json.RawMessage) (json.RawMessage, map[string]any, error) {
	var target any
	switch task {
	case "rca":
		target = &MonitorEvidence{}
	case "dependency":
		target = &PairEvidence{}
	default:
		return nil, nil, fmt.Errorf("unknown task %q", task)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(evidence, &raw); err != nil || raw == nil {
		return nil, nil, errors.New("evidence must be a JSON object")
	}
	for k, v := range raw {
		if bytes.Equal(bytes.TrimSpace(v), []byte("null")) {
			return nil, nil, fmt.Errorf("evidence field %q is null", k)
		}
	}
	if task == "rca" {
		if _, ok := raw["checks"]; !ok {
			return nil, nil, errors.New("invalid check counts")
		}
		if _, ok := raw["failures"]; !ok {
			return nil, nil, errors.New("invalid check counts")
		}
	}
	dec := json.NewDecoder(bytes.NewReader(evidence))
	dec.DisallowUnknownFields()
	if err := dec.Decode(target); err != nil {
		return nil, nil, fmt.Errorf("evidence contains fields outside the analysis allowlist: %w", err)
	}
	if dec.Decode(new(any)) != io.EOF {
		return nil, nil, errors.New("trailing data after evidence")
	}
	canonical, err := json.Marshal(target)
	if err != nil {
		return nil, nil, err
	}
	fields := map[string]any{}
	dec = json.NewDecoder(bytes.NewReader(canonical))
	dec.UseNumber()
	if err := dec.Decode(&fields); err != nil {
		return nil, nil, err
	}
	for k, v := range fields {
		n, ok := v.(json.Number)
		if !ok {
			continue
		}
		i, err := n.Int64()
		if err != nil {
			return nil, nil, fmt.Errorf("bounded integer evidence expected for %q", k)
		}
		if i > evidenceBound || i < -evidenceBound {
			return nil, nil, fmt.Errorf("bounded integer evidence expected for %q", k)
		}
		if i < 0 && k != "a_leads_seconds" {
			return nil, nil, fmt.Errorf("negative measurement %q", k)
		}
		fields[k] = i
	}
	if task == "rca" && fields["failures"].(int64) > fields["checks"].(int64) {
		return nil, nil, errors.New("invalid check counts")
	}
	return canonical, fields, nil
}

// StateText ports contract.state_text INCLUDING validate_evidence: evidence is
// strictly decoded into MonitorEvidence/PairEvidence (unknown field -> error),
// ints bounded |v| <= 1e8, non-negative except a_leads_seconds, rca failures <= checks.
//
//	rca: "Checks: N. Failed observations: M." then, for each other RCA field in
//	     sorted key order whose value is non-zero/true, "<key with _ -> space>: <v|observed>."
//	dependency: "Co-firing windows: N." then "A precedes B by S seconds." (lead>0) |
//	     "B precedes A by S seconds." (lead<0) | "No clear time direction." (0), then
//	     the labelled fields in sorted key order -> "<label>: <v|observed>."
//
// Sentences are joined by single spaces.
func StateText(task string, evidence json.RawMessage) (string, error) {
	_, fields, err := decodeEvidence(task, evidence)
	if err != nil {
		return "", err
	}
	return stateText(task, fields), nil
}

func stateText(task string, fields map[string]any) string {
	var text []string
	labels := map[string]string{}
	if task == "dependency" {
		lead := fields["a_leads_seconds"].(int64)
		text = append(text, fmt.Sprintf("Co-firing windows: %d.", fields["co_fires"].(int64)))
		switch {
		case lead > 0:
			text = append(text, fmt.Sprintf("A precedes B by %d seconds.", lead))
		case lead < 0:
			text = append(text, fmt.Sprintf("B precedes A by %d seconds.", -lead))
		default:
			text = append(text, "No clear time direction.")
		}
		labels = dependencyStateLabels
	} else {
		text = append(text, fmt.Sprintf("Checks: %d. Failed observations: %d.", fields["checks"].(int64), fields["failures"].(int64)))
		for k := range fields {
			if k != "checks" && k != "failures" {
				labels[k] = strings.ReplaceAll(k, "_", " ")
			}
		}
	}
	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		switch v := fields[k].(type) {
		case bool:
			if v {
				text = append(text, labels[k]+": observed.")
			}
		case int64:
			if v != 0 {
				text = append(text, labels[k]+": "+strconv.FormatInt(v, 10)+".")
			}
		}
	}
	return strings.Join(text, " ")
}

// LabelRecord is one stored label as the exporter needs it.
type LabelRecord struct {
	SubjectType string            // incident_analysis | alert_triage | dependency_pair
	SubjectKey  string            // row-id input
	GroupKey    string            // "incident:<incident uuid>" | "triage:<triage uuid>" | "pair:<lo>:<hi>"
	Verdict     string            //
	Request     DecisionSnapshot  // WithoutPredictions()
	Predicted   *DecisionSnapshot // nil for pairs; only Decisions[].ID/Predicted/Final/Probabilities read
	Answers     map[string]LabelAnswer
	LabeledAt   time.Time
}

type exportQuestion struct {
	Type         string            `json:"type"`
	Instructions string            `json:"instructions"`
	Criteria     map[string]string `json:"criteria"`
}

type exportPredicted struct {
	Label       string  `json:"label"`
	Best        string  `json:"best"`
	Probability float64 `json:"probability"`
}

type exportProvenance struct {
	Source       string           `json:"source"`
	Row          string           `json:"row"`
	SubjectType  string           `json:"subject_type"`
	Verdict      string           `json:"verdict"`
	AnswerSource string           `json:"answer_source"`
	Catalogue    string           `json:"catalogue"`
	ModelVersion string           `json:"model_version,omitempty"`
	ModelSHA256  string           `json:"model_sha256,omitempty"`
	Predicted    *exportPredicted `json:"predicted"`
	LabeledAt    string           `json:"labeled_at"`
}

// exportRow is contract.training_row plus provenance; field order is fixed
// by this struct.
type exportRow struct {
	Task       string                    `json:"task"`
	Group      string                    `json:"group"`
	Evidence   json.RawMessage           `json:"evidence"`
	State      string                    `json:"state"`
	Questions  map[string]exportQuestion `json:"questions"`
	Expected   map[string]string         `json:"expected"`
	Provenance exportProvenance          `json:"provenance"`
}

// labelHMAC is the opaque id derivation for groups and rows: 32 hex chars of
// HMAC-SHA256 keyed by the workspace salt, so ids are stable per workspace
// and reveal nothing about the subject.
func labelHMAC(salt []byte, msg string) string {
	m := hmac.New(sha256.New, salt)
	m.Write([]byte(msg))
	return hex.EncodeToString(m.Sum(nil))[:32]
}

// WriteLabelExport writes one row per decision. Rows whose evidence fails
// StateText validation or whose answer is not in the decision's choices are
// skipped and counted. The encoder does not escape HTML; one '\n' per row.
//
// Redaction: monitor refs, pair UUIDs, decision ids and subject keys never
// appear in the output; group and row ids are HMACs under salt.
func WriteLabelExport(w io.Writer, salt []byte, records []LabelRecord) (rows, skipped int, err error) {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	for _, rec := range records {
		predicted := map[string]SnapshotDecision{}
		if rec.Predicted != nil {
			for _, d := range rec.Predicted.Decisions {
				predicted[d.ID] = d
			}
		}
		for i, d := range rec.Request.Decisions {
			answer, ok := rec.Answers[d.ID]
			if !ok {
				skipped++
				continue
			}
			if _, ok := d.Choices[answer.Label]; !ok {
				skipped++
				continue
			}
			canonical, fields, err := decodeEvidence(d.Task, d.Evidence)
			if err != nil {
				skipped++
				continue
			}
			row := exportRow{
				Task:     d.Task,
				Group:    "op-" + d.Task + "-" + labelHMAC(salt, "group:"+rec.GroupKey),
				Evidence: canonical,
				State:    stateText(d.Task, fields),
				Questions: map[string]exportQuestion{d.Task: {
					Type: "choice", Instructions: LabelInstructions, Criteria: d.Choices,
				}},
				Expected: map[string]string{d.Task: answer.Label},
				Provenance: exportProvenance{
					Source:       "operator",
					Row:          "op-" + labelHMAC(salt, "row:"+rec.SubjectType+":"+rec.SubjectKey+":"+strconv.Itoa(i)),
					SubjectType:  rec.SubjectType,
					Verdict:      rec.Verdict,
					AnswerSource: answer.Source,
					Catalogue:    rec.Request.Catalogue,
					ModelVersion: rec.Request.Model.Version,
					ModelSHA256:  rec.Request.Model.SHA256,
					LabeledAt:    rec.LabeledAt.UTC().Format(time.RFC3339),
				},
			}
			if p, ok := predicted[d.ID]; ok && p.Predicted != "" {
				row.Provenance.Predicted = &exportPredicted{Label: p.Final, Best: p.Predicted, Probability: p.Probabilities[p.Predicted]}
			}
			if err := enc.Encode(row); err != nil {
				return rows, skipped, err
			}
			rows++
		}
	}
	return rows, skipped, nil
}
