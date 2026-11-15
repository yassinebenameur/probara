package main

import (
	"reflect"
	"sort"
	"strconv"
	"strings"

	"github.com/yassinebenameur/probara/shared/secrets"
)

// canonicalizeCredentialKeys renames, in cfg, the miscased spellings of the
// keys credentials live under: a type's registered secret fields and the
// plain keys on secrets.PlainCredentialPaths (`Headers`, `Variables`,
// `Metadata`, a synthetic API step's `Request`/`Headers`). Before validation
// refused them, the API stored such keys as sent; the worker honoured them
// (config structs decode keys case-insensitively) while encryption, masking
// and the header move did not, so those credentials sit in plain text and
// today's validation refuses the row on every save.
//
// When several spellings exist, their values are merged the way the struct
// decode combined them: maps are united and equal values collapse. Spellings
// that disagree (one header with two values, two different step lists) are a
// conflict, returned as paths for an operator to fix by hand. cfg may be
// partly rewritten then, so the caller must discard it.
func canonicalizeCredentialKeys(monitorType string, cfg map[string]any) (changed bool, conflicts []string) {
	fields := append(append([]string(nil), secrets.MonitorSecretFields[monitorType]...),
		secrets.MonitorSecretMapFields[monitorType]...)
	for _, field := range fields {
		if renamed := canonicalizeKey(cfg, field, "", &conflicts); renamed {
			changed = true
		}
	}
	for _, path := range secrets.PlainCredentialPaths[monitorType] {
		if canonicalizePath(cfg, path, "", &conflicts) {
			changed = true
		}
	}
	sort.Strings(conflicts)
	return changed && len(conflicts) == 0, conflicts
}

// canonicalizePath repairs every key on path, the deepest level first, so a
// level is merged only once the levels below it carry exact spellings in
// every one of its variants. "*" visits every element of an array.
func canonicalizePath(node any, path []string, where string, conflicts *[]string) bool {
	if len(path) == 0 {
		return false
	}
	changed := false
	if path[0] == "*" {
		items, _ := node.([]any)
		for i, item := range items {
			if canonicalizePath(item, path[1:], where+"["+strconv.Itoa(i)+"]", conflicts) {
				changed = true
			}
		}
		return changed
	}
	object, ok := node.(map[string]any)
	if !ok {
		return false
	}
	key := path[0]
	for _, candidate := range sortedKeys(object) {
		if strings.EqualFold(candidate, key) && canonicalizePath(object[candidate], path[1:], joinPath(where, candidate), conflicts) {
			changed = true
		}
	}
	if canonicalizeKey(object, key, where, conflicts) {
		changed = true
	}
	return changed
}

// canonicalizeKey merges the miscased spellings of key in object into the
// exact one, reporting the first spelling that disagrees.
func canonicalizeKey(object map[string]any, key, where string, conflicts *[]string) bool {
	var variants []string
	for _, candidate := range sortedKeys(object) {
		if candidate != key && strings.EqualFold(candidate, key) {
			variants = append(variants, candidate)
		}
	}
	if len(variants) == 0 {
		return false
	}
	merged, present := object[key]
	for _, variant := range variants {
		if !present {
			merged, present = object[variant], true
			continue
		}
		next, ok := mergeKeyValues(merged, object[variant])
		if !ok {
			*conflicts = append(*conflicts, joinPath(where, variant))
			return false
		}
		merged = next
	}
	for _, variant := range variants {
		delete(object, variant)
	}
	object[key] = merged
	return true
}

// mergeKeyValues combines the values of two spellings of one key: maps are
// united (a shared entry must agree), anything else must be equal.
func mergeKeyValues(a, b any) (any, bool) {
	left, leftIsMap := a.(map[string]any)
	right, rightIsMap := b.(map[string]any)
	if leftIsMap && rightIsMap {
		out := make(map[string]any, len(left)+len(right))
		for k, v := range left {
			out[k] = v
		}
		for k, v := range right {
			if existing, ok := out[k]; ok && !reflect.DeepEqual(existing, v) {
				return nil, false
			}
			out[k] = v
		}
		return out, true
	}
	if reflect.DeepEqual(a, b) {
		return a, true
	}
	return nil, false
}

func sortedKeys(object map[string]any) []string {
	keys := make([]string, 0, len(object))
	for k := range object {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func joinPath(where, key string) string {
	if where == "" {
		return key
	}
	return where + "." + key
}
