package secrets

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// sensitiveHeaderExact and sensitiveHeaderMarkers decide which request header
// names carry credentials. This is the Go twin of isSensitiveHeaderName in
// web/lib/monitor-form/http.ts: the form uses it to default a header row to
// secret, the API to move such headers out of plaintext config on write. Keep
// the two in step; testdata/sensitive_header_names.json is read by both test
// suites and pins the pair.
var sensitiveHeaderExact = map[string]bool{
	"authorization":       true,
	"proxy-authorization": true,
	"cookie":              true,
	"x-api-key":           true,
	"api-key":             true,
	"x-auth-token":        true,
}

var sensitiveHeaderMarkers = []string{"token", "secret", "password", "apikey", "api-key"}

// IsSensitiveHeaderName reports whether a request header name conventionally
// carries a credential (Authorization, Cookie, X-API-Key, any *token*,
// *secret*, …). Matching is case-insensitive and ignores surrounding space.
func IsSensitiveHeaderName(name string) bool {
	n := strings.ToLower(strings.TrimSpace(name))
	if n == "" {
		return false
	}
	if sensitiveHeaderExact[n] {
		return true
	}
	for _, marker := range sensitiveHeaderMarkers {
		if strings.Contains(n, marker) {
			return true
		}
	}
	return false
}

// syntheticTemplateRef matches a {{name}} reference, the worker's template
// syntax (worker/internal/worker/synthetic_api_checker.go).
var syntheticTemplateRef = regexp.MustCompile(`\{\{\s*([a-zA-Z0-9_.-]+)\s*\}\}`)

// NormalizeSensitiveHeaders moves credential-bearing request headers out of
// plaintext config into the type's secret map, so every writer (the form, the
// REST API, MCP, imports) gets them encrypted at rest and masked on read:
//
//   - http: a sensitive name in `headers` moves to `secret_headers`,
//     replacing any same-named (case-insensitive) secret header.
//   - synthetic_api: a sensitive step header whose value is a literal (no
//     {{ref}}) becomes a generated `secret_variables` entry, and the header
//     value becomes the {{ref}}. A value that already references a variable
//     is left alone — the reference is the protection.
//
// Run it after MergeMonitorConfigSecrets (an absent secret map must first be
// restored from storage, or the moved header would read as an explicit edit
// that drops the other stored keys) and before EncryptMonitorConfig.
// Other types pass through unchanged. changed reports whether raw was
// rewritten.
func NormalizeSensitiveHeaders(monitorType string, raw json.RawMessage) (json.RawMessage, bool, error) {
	if len(raw) == 0 {
		return raw, false, nil
	}
	switch monitorType {
	case "http":
		return normalizeHTTPHeaders(raw)
	case "synthetic_api":
		return normalizeSyntheticAPIHeaders(raw)
	default:
		return raw, false, nil
	}
}

func normalizeHTTPHeaders(raw json.RawMessage) (json.RawMessage, bool, error) {
	cfg, err := unmarshalConfigMap(raw)
	if err != nil {
		return nil, false, err
	}
	headers, ok := cfg["headers"].(map[string]any)
	if !ok || len(headers) == 0 {
		return raw, false, nil
	}
	moved := map[string]string{}
	for name, value := range headers {
		text, isString := value.(string)
		if !isString || !IsSensitiveHeaderName(name) {
			continue
		}
		moved[name] = text
		delete(headers, name)
	}
	if len(moved) == 0 {
		return raw, false, nil
	}

	secretHeaders, _ := stringMap(cfg["secret_headers"])
	if secretHeaders == nil {
		secretHeaders = map[string]string{}
	}
	for name, value := range moved {
		for existing := range secretHeaders {
			if strings.EqualFold(existing, name) {
				delete(secretHeaders, existing)
			}
		}
		if value != "" {
			secretHeaders[name] = value
		}
	}
	if len(headers) == 0 {
		delete(cfg, "headers")
	}
	if len(secretHeaders) == 0 {
		delete(cfg, "secret_headers")
	} else {
		cfg["secret_headers"] = secretHeaders
	}
	out, err := json.Marshal(cfg)
	if err != nil {
		return nil, false, fmt.Errorf("secrets: encode monitor config: %w", err)
	}
	return out, true, nil
}

func normalizeSyntheticAPIHeaders(raw json.RawMessage) (json.RawMessage, bool, error) {
	cfg, err := unmarshalConfigMap(raw)
	if err != nil {
		return nil, false, err
	}
	steps, ok := cfg["steps"].([]any)
	if !ok || len(steps) == 0 {
		return raw, false, nil
	}

	secretVars, _ := stringMap(cfg["secret_variables"])
	if secretVars == nil {
		secretVars = map[string]string{}
	}
	plainVars, _ := cfg["variables"].(map[string]any)
	taken := func(name string) bool {
		if _, ok := secretVars[name]; ok {
			return true
		}
		_, ok := plainVars[name]
		return ok
	}

	changed := false
	for stepIdx, rawStep := range steps {
		step, ok := rawStep.(map[string]any)
		if !ok {
			continue
		}
		request, ok := step["request"].(map[string]any)
		if !ok {
			continue
		}
		headers, ok := request["headers"].(map[string]any)
		if !ok {
			continue
		}
		// Sorted for a deterministic generated name when two headers of
		// one step would collide.
		names := make([]string, 0, len(headers))
		for name := range headers {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			text, isString := headers[name].(string)
			if !isString || text == "" || !IsSensitiveHeaderName(name) || syntheticTemplateRef.MatchString(text) {
				continue
			}
			stepID, _ := step["id"].(string)
			varName := uniqueVariableName(syntheticSecretVariableName(stepID, stepIdx, name), taken)
			secretVars[varName] = text
			headers[name] = "{{" + varName + "}}"
			changed = true
		}
	}
	if !changed {
		return raw, false, nil
	}
	cfg["secret_variables"] = secretVars
	out, err := json.Marshal(cfg)
	if err != nil {
		return nil, false, fmt.Errorf("secrets: encode monitor config: %w", err)
	}
	return out, true, nil
}

var nonVariableChars = regexp.MustCompile(`[^a-z0-9_]+`)

// syntheticSecretVariableName derives a readable variable name for a header
// moved out of a synthetic step: <step id>_<header>, lowercased, restricted to
// the template syntax's character set.
func syntheticSecretVariableName(stepID string, stepIdx int, header string) string {
	clean := func(s string) string {
		return strings.Trim(nonVariableChars.ReplaceAllString(strings.ToLower(strings.TrimSpace(s)), "_"), "_")
	}
	step := clean(stepID)
	if step == "" {
		step = fmt.Sprintf("step%d", stepIdx+1)
	}
	name := clean(header)
	if name == "" {
		name = "header"
	}
	return step + "_" + name
}

func uniqueVariableName(base string, taken func(string) bool) string {
	if !taken(base) {
		return base
	}
	for i := 2; ; i++ {
		candidate := fmt.Sprintf("%s_%d", base, i)
		if !taken(candidate) {
			return candidate
		}
	}
}
