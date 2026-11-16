package monitors

import (
	"encoding/json"
	"reflect"
	"sort"
	"strings"

	"github.com/yassinebenameur/probara/api/internal/models"
	"github.com/yassinebenameur/probara/shared/secrets"
)

// SecretFieldChanges names the secret config fields whose stored value
// differs between two stored (encrypted) configs: scalar fields by name, map
// fields as "field.key". Stored values are ciphertext envelopes that the
// write-only merge carries forward unchanged, so a kept secret compares equal
// and any set, replace or clear shows up. The fields come from the secret
// registry (secrets.MonitorSecretFields / MonitorSecretMapFields), never a
// per-type list. beforeType/before are empty on create.
func SecretFieldChanges(beforeType models.MonitorType, before json.RawMessage, afterType models.MonitorType, after json.RawMessage) []string {
	if !secrets.HasMonitorSecrets(string(beforeType)) && !secrets.HasMonitorSecrets(string(afterType)) {
		return nil
	}
	prev := decodeConfigMap(before)
	next := decodeConfigMap(after)
	changed := map[string]struct{}{}
	for _, field := range unionFields(secrets.MonitorSecretFields[string(beforeType)], secrets.MonitorSecretFields[string(afterType)]) {
		a, _ := prev[field].(string)
		b, _ := next[field].(string)
		if a != b {
			changed[field] = struct{}{}
		}
	}
	for _, field := range unionFields(secrets.MonitorSecretMapFields[string(beforeType)], secrets.MonitorSecretMapFields[string(afterType)]) {
		a, _ := prev[field].(map[string]any)
		b, _ := next[field].(map[string]any)
		for key, value := range a {
			// DeepEqual: a malformed stored map could hold a non-string
			// value, and comparing those with != could panic.
			if other, ok := b[key]; !ok || !reflect.DeepEqual(other, value) {
				changed[field+"."+key] = struct{}{}
			}
		}
		for key := range b {
			if _, ok := a[key]; !ok {
				changed[field+"."+key] = struct{}{}
			}
		}
	}
	out := make([]string, 0, len(changed))
	for field := range changed {
		out = append(out, field)
	}
	sort.Strings(out)
	return out
}

func unionFields(a, b []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(a)+len(b))
	for _, list := range [][]string{a, b} {
		for _, field := range list {
			if _, ok := seen[field]; !ok {
				seen[field] = struct{}{}
				out = append(out, field)
			}
		}
	}
	return out
}

func decodeConfigMap(raw json.RawMessage) map[string]any {
	var out map[string]any
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &out)
	}
	if out == nil {
		out = map[string]any{}
	}
	return out
}

// secretPath reports whether a snapshot path is a secret config field of
// either type: config.<field> for a registry scalar, config.<map> or
// config.<map>.<key> for a registry map.
func secretPath(path string, types ...models.MonitorType) bool {
	rest, ok := strings.CutPrefix(path, "config.")
	if !ok {
		return false
	}
	head, _, _ := strings.Cut(rest, ".")
	for _, typ := range types {
		for _, field := range secrets.MonitorSecretFields[string(typ)] {
			if rest == field {
				return true
			}
		}
		for _, field := range secrets.MonitorSecretMapFields[string(typ)] {
			if head == field {
				return true
			}
		}
	}
	return false
}

// diffSnapshots lists the paths that differ between two snapshots. Objects
// recurse (config.url, config.secret_headers.Authorization), also when one
// side lacks them, so a change always names its leaf; arrays and scalars
// compare whole. A nil before reads every field as added. Secret paths are
// marked; their values are the mask, never a secret.
func diffSnapshots(before, after *models.MonitorConfigSnapshot) ([]models.MonitorVersionChange, error) {
	a := map[string]any{}
	types := []models.MonitorType{after.Type}
	if before != nil {
		var err error
		if a, err = snapshotMap(before); err != nil {
			return nil, err
		}
		types = append(types, before.Type)
	}
	b, err := snapshotMap(after)
	if err != nil {
		return nil, err
	}
	changes := []models.MonitorVersionChange{}
	diffObjects("", a, b, &changes)
	for i := range changes {
		changes[i].Secret = secretPath(changes[i].Path, types...)
	}
	return changes, nil
}

// withSecretChanges adds a valueless Secret entry for each changed secret
// field the masked diff cannot show (a secret replaced by another reads
// "***" on both sides), in path order.
func withSecretChanges(changes []models.MonitorVersionChange, before, after *models.MonitorConfigSnapshot, secretFields []string) []models.MonitorVersionChange {
	if len(secretFields) == 0 {
		return changes
	}
	present := map[string]bool{}
	for _, change := range changes {
		present[change.Path] = true
	}
	added := false
	for _, field := range secretFields {
		path := "config." + field
		if present[path] {
			continue
		}
		change := models.MonitorVersionChange{Path: path, Secret: true}
		if before != nil && maskedValueSet(before.Config, field) {
			change.Before = secrets.MaskedSecret
		}
		if maskedValueSet(after.Config, field) {
			change.After = secrets.MaskedSecret
		}
		// Unset on both sides (set and cleared again in between): nothing
		// differs.
		if change.Before == nil && change.After == nil {
			continue
		}
		changes = append(changes, change)
		present[path] = true
		added = true
	}
	if added {
		sort.SliceStable(changes, func(i, j int) bool { return changes[i].Path < changes[j].Path })
	}
	return changes
}

// maskedValueSet reports whether a masked config holds field ("password" or
// "secret_headers.Authorization").
func maskedValueSet(config json.RawMessage, field string) bool {
	values := decodeConfigMap(config)
	head, key, nested := strings.Cut(field, ".")
	if !nested {
		value, _ := values[head].(string)
		return value != ""
	}
	inner, _ := values[head].(map[string]any)
	value, _ := inner[key].(string)
	return value != ""
}

func snapshotMap(snapshot *models.MonitorConfigSnapshot) (map[string]any, error) {
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		return nil, err
	}
	value, err := decodeJSONValue(encoded)
	if err != nil {
		return nil, err
	}
	out, _ := value.(map[string]any)
	if out == nil {
		out = map[string]any{}
	}
	return out, nil
}

func diffObjects(prefix string, a, b map[string]any, out *[]models.MonitorVersionChange) {
	keys := make([]string, 0, len(a)+len(b))
	for key := range a {
		keys = append(keys, key)
	}
	for key := range b {
		if _, ok := a[key]; !ok {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	for _, key := range keys {
		path := key
		if prefix != "" {
			path = prefix + "." + key
		}
		va, inA := a[key]
		vb, inB := b[key]
		ma, okA := va.(map[string]any)
		mb, okB := vb.(map[string]any)
		switch {
		case okA && okB, okA && !inB, okB && !inA:
			// An object on one side only recurses against an empty one, so
			// an added secret map lists its keys, not one opaque object.
			if ma == nil {
				ma = map[string]any{}
			}
			if mb == nil {
				mb = map[string]any{}
			}
			diffObjects(path, ma, mb, out)
		case !inA:
			*out = append(*out, models.MonitorVersionChange{Path: path, After: vb})
		case !inB:
			*out = append(*out, models.MonitorVersionChange{Path: path, Before: va})
		case !reflect.DeepEqual(va, vb):
			*out = append(*out, models.MonitorVersionChange{Path: path, Before: va, After: vb})
		}
	}
}
