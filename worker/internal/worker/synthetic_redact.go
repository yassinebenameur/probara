package worker

import (
	"bytes"
	"encoding/json"
	"net/url"
	"sort"
	"strconv"
	"strings"
)

// syntheticRedactedValue replaces secret material in reported text. It is the
// token extractSyntheticVariables writes for a sensitive extraction.
const syntheticRedactedValue = "[REDACTED]"

// secretRedactor scrubs the values of a synthetic monitor's secret_variables
// from text a check reports. A secret reaches reported text through a {{ref}}
// in a step URL (recorded as the resolved URL), in an error that quotes a
// request, or in a server's echo of it, so redaction runs over the finished
// CheckResult rather than at each place text is built: a new field cannot
// forget it.
type secretRedactor struct {
	needles []string
}

// newSecretRedactor returns nil when there is nothing to redact. Besides each
// value verbatim it matches the forms a value takes once it passes through a
// URL (query and path escaping) or a %q-quoted message, longest first so a
// value never leaves a fragment of a longer one behind.
func newSecretRedactor(secretVariables map[string]string) *secretRedactor {
	seen := map[string]bool{}
	var needles []string
	add := func(s string) {
		if s != "" && !seen[s] {
			seen[s] = true
			needles = append(needles, s)
		}
	}
	for _, value := range secretVariables {
		if value == "" {
			continue
		}
		add(value)
		add(url.QueryEscape(value))
		add(url.PathEscape(value))
		quoted := strconv.Quote(value)
		add(quoted[1 : len(quoted)-1])
	}
	if len(needles) == 0 {
		return nil
	}
	sort.SliceStable(needles, func(i, j int) bool { return len(needles[i]) > len(needles[j]) })
	return &secretRedactor{needles: needles}
}

func (r *secretRedactor) redact(text string) string {
	if r == nil {
		return text
	}
	for _, needle := range r.needles {
		if strings.Contains(text, needle) {
			text = strings.ReplaceAll(text, needle, syntheticRedactedValue)
		}
	}
	return text
}

// redactResult scrubs the error message and every string in the metrics
// document (step URLs, errors, assertion text, extracted values, artifact
// paths and warnings). On a metrics document that does not decode, the
// metrics are dropped rather than reported unredacted.
func (r *secretRedactor) redactResult(result CheckResult) CheckResult {
	if r == nil {
		return result
	}
	if result.ErrorMessage != nil {
		redacted := r.redact(*result.ErrorMessage)
		result.ErrorMessage = &redacted
	}
	if len(result.MetricsData) > 0 {
		decoder := json.NewDecoder(bytes.NewReader(result.MetricsData))
		decoder.UseNumber()
		var doc any
		if err := decoder.Decode(&doc); err != nil {
			result.MetricsData = nil
			return result
		}
		out, err := json.Marshal(r.redactValue(doc))
		if err != nil {
			result.MetricsData = nil
			return result
		}
		result.MetricsData = out
	}
	return result
}

func (r *secretRedactor) redactValue(value any) any {
	switch v := value.(type) {
	case string:
		return r.redact(v)
	case map[string]any:
		for key, inner := range v {
			v[key] = r.redactValue(inner)
		}
		return v
	case []any:
		for i, inner := range v {
			v[i] = r.redactValue(inner)
		}
		return v
	default:
		return value
	}
}

// syntheticSecretVariables reads only the secret_variables of a synthetic
// config, for the redaction wrapper around a checker's Check.
func syntheticSecretVariables(configRaw json.RawMessage) map[string]string {
	var cfg struct {
		SecretVariables map[string]string `json:"secret_variables"`
	}
	if err := json.Unmarshal(configRaw, &cfg); err != nil {
		return nil
	}
	return cfg.SecretVariables
}

// mergeSyntheticVariables builds the template namespace of a journey: plain
// variables plus secret variables (validation keeps the names disjoint).
func mergeSyntheticVariables(variables, secretVariables map[string]string) map[string]string {
	vars := make(map[string]string, len(variables)+len(secretVariables))
	for k, v := range variables {
		vars[k] = v
	}
	for k, v := range secretVariables {
		vars[k] = v
	}
	return vars
}
