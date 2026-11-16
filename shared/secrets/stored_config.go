package secrets

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// plainCredentialLeaves are the last keys of the PlainCredentialPaths that
// name maps of credential-bearing entries (the others, `steps` and a step's
// `request`, are only on the way to one).
var plainCredentialLeaves = map[string]bool{"headers": true, "metadata": true, "variables": true}

// WalkPlainCredentials calls fn for every entry of a plain config map on the
// type's PlainCredentialPaths (`headers`, `metadata`, `variables`, a synthetic
// API step's request `headers`) whose name the move rules treat as a
// credential (IsSensitiveHeaderName, IsSensitiveVariableName for variables)
// and whose value is a non-empty string. Keys on the path match
// case-insensitively, so a miscased spelling stored before validation refused
// it is visited too. A step header whose value is a {{ref}} is skipped: the
// reference is the protection. where is the map's path ("headers",
// "steps[0].request.headers"); fn may change or delete entries[name].
func WalkPlainCredentials(monitorType string, cfg map[string]any, fn func(where string, entries map[string]any, name string)) {
	for _, path := range PlainCredentialPaths[monitorType] {
		leaf := path[len(path)-1]
		if !plainCredentialLeaves[leaf] {
			continue
		}
		templated := len(path) > 1
		walkPlainPath(cfg, path, "", func(where string, entries map[string]any) {
			for _, name := range sortedObjectKeys(entries) {
				text, ok := entries[name].(string)
				if !ok || text == "" {
					continue
				}
				sensitive := IsSensitiveHeaderName(name)
				if leaf == "variables" {
					sensitive = IsSensitiveVariableName(name)
				}
				if !sensitive || (templated && text != MaskedSecret && syntheticTemplateRef.MatchString(text)) {
					continue
				}
				fn(where, entries, name)
			}
		})
	}
}

func walkPlainPath(node any, path []string, where string, visit func(where string, entries map[string]any)) {
	if len(path) == 0 {
		if entries, ok := node.(map[string]any); ok {
			visit(where, entries)
		}
		return
	}
	if path[0] == "*" {
		items, _ := node.([]any)
		for i, item := range items {
			walkPlainPath(item, path[1:], where+"["+strconv.Itoa(i)+"]", visit)
		}
		return
	}
	object, ok := node.(map[string]any)
	if !ok {
		return
	}
	for _, key := range sortedObjectKeys(object) {
		if strings.EqualFold(key, path[0]) {
			walkPlainPath(object[key], path[1:], joinPath(where, key), visit)
		}
	}
}

// MaskStoredMonitorConfig masks a stored monitor config so that nothing in it
// is a value the secret registry or the credential name rules would treat as
// secret, also for rows stored before those rules applied on every save: a
// credential under a miscased key or in a plain map. It folds miscased keys
// to their exact spelling (CanonicalizeCredentialKeys), moves credential
// entries into the secret maps as a save would (NormalizeSensitiveHeaders),
// masks every secret (MaskMonitorConfig), and finally replaces any
// credential the steps above could not place — a miscased spelling in
// conflict, a credential-named variable that cannot move — with "***", as
// well as URL credentials anywhere in the config: a userinfo password and
// credential-named query parameter values (MaskURLCredentials). Request
// bodies, browser fill values and other free text are not inspected.
// Configuration history snapshots go through it, so a version never holds a
// secret whatever the stored row looks like.
func MaskStoredMonitorConfig(monitorType string, raw json.RawMessage) (json.RawMessage, error) {
	if len(raw) == 0 {
		return raw, nil
	}
	cfg, err := unmarshalConfigMap(raw)
	if err != nil {
		return nil, err
	}
	canonical := raw
	if changed, conflicts := CanonicalizeCredentialKeys(monitorType, cfg); changed && len(conflicts) == 0 {
		if canonical, err = json.Marshal(cfg); err != nil {
			return nil, fmt.Errorf("secrets: encode monitor config: %w", err)
		}
	}
	normalized, _, err := NormalizeSensitiveHeaders(monitorType, canonical)
	if err != nil {
		return nil, err
	}
	masked, err := MaskMonitorConfig(monitorType, normalized)
	if err != nil {
		return nil, err
	}
	out, err := unmarshalConfigMap(masked)
	if err != nil {
		return nil, err
	}
	if !scrubCredentials(monitorType, out) {
		return masked, nil
	}
	encoded, err := json.Marshal(out)
	if err != nil {
		return nil, fmt.Errorf("secrets: encode monitor config: %w", err)
	}
	return encoded, nil
}

// scrubCredentials replaces with "***" every value left under a registry
// secret key in any spelling, and every plain credential entry. It reports
// whether cfg changed.
func scrubCredentials(monitorType string, cfg map[string]any) bool {
	changed := false
	mask := func(value any) (any, bool) {
		if value == nil || value == "" || value == MaskedSecret {
			return value, false
		}
		return MaskedSecret, true
	}
	for key, value := range cfg {
		for _, field := range MonitorSecretFields[monitorType] {
			if strings.EqualFold(key, field) {
				if masked, did := mask(value); did {
					cfg[key] = masked
					changed = true
				}
			}
		}
		for _, field := range MonitorSecretMapFields[monitorType] {
			if !strings.EqualFold(key, field) {
				continue
			}
			entries, ok := value.(map[string]any)
			if !ok {
				if masked, did := mask(value); did {
					cfg[key] = masked
					changed = true
				}
				continue
			}
			for name, entry := range entries {
				if masked, did := mask(entry); did {
					entries[name] = masked
					changed = true
				}
			}
		}
	}
	WalkPlainCredentials(monitorType, cfg, func(_ string, entries map[string]any, name string) {
		if entries[name] != MaskedSecret {
			entries[name] = MaskedSecret
			changed = true
		}
	})
	if _, did := maskURLStrings(cfg); did {
		changed = true
	}
	return changed
}
