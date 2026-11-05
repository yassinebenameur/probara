package worker

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf16"
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
// value verbatim it matches the forms a value takes on its way into reported
// text (secretValueForms), longest first so a value never leaves a fragment of
// a longer one behind.
func newSecretRedactor(secretVariables map[string]string) *secretRedactor {
	seen := map[string]bool{}
	var needles []string
	for _, value := range secretVariables {
		if value == "" {
			continue
		}
		for _, form := range secretValueForms(value) {
			if form != "" && !seen[form] {
				seen[form] = true
				needles = append(needles, form)
			}
		}
	}
	if len(needles) == 0 {
		return nil
	}
	sort.SliceStable(needles, func(i, j int) bool { return len(needles[i]) > len(needles[j]) })
	return &secretRedactor{needles: needles}
}

var percentEscape = regexp.MustCompile(`%[0-9A-F]{2}`)

// secretValueForms lists the spellings of one value worth matching, a bounded
// set (at most ~20, deduplicated by the caller): verbatim; URL query and path
// escaping, spaces as %20 with punctuation left raw (how Chromium writes a
// URL), each percent-encoded form again with lowercase hex; Go %q quoting;
// JSON string escaping as Go writes it (with and without HTML escaping), with
// every non-ASCII rune as \uXXXX, and each with "/" as "\/"; HTML escaping
// with numeric and named quote entities.
func secretValueForms(value string) []string {
	forms := []string{value}
	percent := []string{url.QueryEscape(value), url.PathEscape(value), strings.ReplaceAll(value, " ", "%20")}
	for _, form := range percent {
		forms = append(forms, form, percentEscape.ReplaceAllStringFunc(form, strings.ToLower))
	}
	quoted := strconv.Quote(value)
	forms = append(forms, quoted[1:len(quoted)-1])

	jsonForms := []string{jsonStringBody(value, true), jsonStringBody(value, false), asciiJSONStringBody(value)}
	for _, form := range jsonForms {
		forms = append(forms, form, strings.ReplaceAll(form, "/", `\/`))
	}

	escaped := html.EscapeString(value)
	forms = append(forms, escaped, strings.NewReplacer("&#34;", "&quot;", "&#39;", "&#x27;").Replace(escaped))
	return forms
}

// jsonStringBody is value encoded as a JSON string by encoding/json, without
// the surrounding quotes.
func jsonStringBody(value string, escapeHTML bool) string {
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(escapeHTML)
	if err := encoder.Encode(value); err != nil {
		return ""
	}
	out := strings.TrimSuffix(buf.String(), "\n")
	return out[1 : len(out)-1]
}

// asciiJSONStringBody is value as an ASCII-only JSON string (the form
// Python's json.dumps and many loggers write): non-ASCII runes as \uXXXX,
// surrogate pairs above the BMP.
func asciiJSONStringBody(value string) string {
	var b strings.Builder
	for _, r := range jsonStringBody(value, false) {
		switch {
		case r < 0x80:
			b.WriteRune(r)
		case r > 0xFFFF:
			hi, lo := utf16.EncodeRune(r)
			fmt.Fprintf(&b, "\\u%04x\\u%04x", hi, lo)
		default:
			fmt.Fprintf(&b, "\\u%04x", r)
		}
	}
	return b.String()
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
