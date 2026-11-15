package secrets

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// sensitiveHeaderExact and sensitiveHeaderMarkers decide which request header
// names carry credentials. This is the one Go source of the rule; its TS twin
// is isSensitiveHeaderName in web/lib/monitor-form/http.ts (the form locks
// such rows secret, the API moves them out of plaintext config on write).
// Keep the two in step: testdata/sensitive_header_names.json is read by both
// test suites and pins the pair.
var sensitiveHeaderExact = map[string]bool{
	"authorization":       true,
	"proxy-authorization": true,
	"cookie":              true,
	"x-api-key":           true,
	"api-key":             true,
	"x-auth-token":        true,
}

var sensitiveHeaderMarkers = []string{"token", "secret", "password", "apikey", "api-key"}

// IsSensitiveHeaderName reports whether a request header (or gRPC metadata)
// name conventionally carries a credential: Authorization,
// Proxy-Authorization, Cookie, X-API-Key, any *token*, *secret*, *password*,
// *apikey*. Matching is case-insensitive and ignores surrounding space.
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
// syntax (syntheticTemplateRegex in worker/internal/worker/synthetic_api_checker.go).
var syntheticTemplateRef = regexp.MustCompile(`\{\{\s*([a-zA-Z0-9_.-]+)\s*\}\}`)

// NormalizeSensitiveHeaders moves credential-bearing request headers out of
// plaintext config into the type's secret map, so every writer (the form, the
// REST API, MCP, imports) gets them encrypted at rest and masked on read:
//
//   - http: a sensitive name in `headers` moves to `secret_headers`,
//     replacing any same-named (case-insensitive) secret header.
//   - grpc: the same for `metadata` and `secret_metadata` (gRPC metadata
//     keys are case-insensitive too).
//   - synthetic_api: a sensitive step header whose value is a literal (no
//     {{ref}}) becomes a generated `secret_variables` entry, and the header
//     value becomes the {{ref}}. A value that already references a variable
//     is left alone: the reference is the protection.
//   - synthetic_api and synthetic_browser: a credential-named plain variable
//     (`password`, `api_token`, …) moves to `secret_variables` under the same
//     name, so its {{ref}}s keep resolving (moveSensitiveVariables).
//
// The cmd/admin/migrate_sensitive_headers backfill applies this same function
// to stored rows, so a write and the backfill always agree.
//
// Run it after MergeMonitorConfigSecrets: an absent secret map must first be
// restored from storage, or the moved header would read as an explicit edit
// of the map that drops every other stored key. Run it before
// EncryptMonitorConfig. Other types pass through unchanged. changed reports
// whether raw was rewritten.
func NormalizeSensitiveHeaders(monitorType string, raw json.RawMessage) (json.RawMessage, bool, error) {
	if len(raw) == 0 {
		return raw, false, nil
	}
	switch monitorType {
	case "http":
		return moveSensitiveKeys(raw, "headers", "secret_headers")
	case "grpc":
		return moveSensitiveKeys(raw, "metadata", "secret_metadata")
	case "synthetic_api":
		out, headersMoved, err := normalizeSyntheticAPIHeaders(raw)
		if err != nil {
			return nil, false, err
		}
		out, varsMoved, err := moveSensitiveVariables(out)
		if err != nil {
			return nil, false, err
		}
		return out, headersMoved || varsMoved, nil
	case "synthetic_browser":
		return moveSensitiveVariables(raw)
	default:
		return raw, false, nil
	}
}

// moveSensitiveKeys moves credential-named entries of the plain map field into
// its secret twin.
func moveSensitiveKeys(raw json.RawMessage, plainField, secretField string) (json.RawMessage, bool, error) {
	// Byte-level pre-check: no plain map, nothing to move (most configs).
	if !containsJSONKey(raw, plainField) && !strings.Contains(string(raw), `\u`) {
		return raw, false, nil
	}
	cfg, err := unmarshalConfigMap(raw)
	if err != nil {
		return nil, false, err
	}
	plain, ok := cfg[plainField].(map[string]any)
	if !ok || len(plain) == 0 {
		return raw, false, nil
	}
	moved := map[string]string{}
	for name, value := range plain {
		text, isString := value.(string)
		if !isString || !IsSensitiveHeaderName(name) {
			continue
		}
		moved[name] = text
		delete(plain, name)
	}
	if len(moved) == 0 {
		return raw, false, nil
	}

	secretValues, _ := stringMap(cfg[secretField])
	if secretValues == nil {
		secretValues = map[string]string{}
	}
	for name, value := range moved {
		// A placeholder in the plain map has nothing behind it to move: the
		// write-only merge only resolves "***" inside the secret map. Drop it,
		// leaving any stored same-named secret as the merge restored it.
		if value == MaskedSecret {
			continue
		}
		for existing := range secretValues {
			if strings.EqualFold(existing, name) {
				delete(secretValues, existing)
			}
		}
		if value != "" {
			secretValues[name] = value
		}
	}
	if len(plain) == 0 {
		delete(cfg, plainField)
	}
	if len(secretValues) == 0 {
		delete(cfg, secretField)
	} else {
		cfg[secretField] = secretValues
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
			if !isString || text == "" || text == MaskedSecret || !IsSensitiveHeaderName(name) || syntheticTemplateRef.MatchString(text) {
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

// moveSensitiveVariables moves credential-named entries of a synthetic
// journey's plain `variables` (the IsSensitiveHeaderName rule: password,
// token, secret, …) into `secret_variables` under the same name, so every
// {{name}} reference keeps resolving while the value becomes encrypted,
// masked and redacted. The moved value replaces a same-named secret, as a
// moved header does; an empty value has nothing to protect and stays plain,
// and a "***" placeholder has nothing behind it there and is dropped, leaving
// any stored secret of that name as the merge restored it.
func moveSensitiveVariables(raw json.RawMessage) (json.RawMessage, bool, error) {
	if !containsJSONKey(raw, "variables") && !strings.Contains(string(raw), `\u`) {
		return raw, false, nil
	}
	cfg, err := unmarshalConfigMap(raw)
	if err != nil {
		return nil, false, err
	}
	variables, ok := cfg["variables"].(map[string]any)
	if !ok || len(variables) == 0 {
		return raw, false, nil
	}
	secretVars, _ := stringMap(cfg["secret_variables"])
	if secretVars == nil {
		secretVars = map[string]string{}
	}
	changed := false
	for name, value := range variables {
		text, isString := value.(string)
		if !isString || text == "" || !IsSensitiveHeaderName(name) {
			continue
		}
		delete(variables, name)
		changed = true
		if text != MaskedSecret {
			secretVars[name] = text
		}
	}
	if !changed {
		return raw, false, nil
	}
	if len(variables) == 0 {
		delete(cfg, "variables")
	}
	if len(secretVars) == 0 {
		delete(cfg, "secret_variables")
	} else {
		cfg["secret_variables"] = secretVars
	}
	out, err := json.Marshal(cfg)
	if err != nil {
		return nil, false, fmt.Errorf("secrets: encode monitor config: %w", err)
	}
	return out, true, nil
}
