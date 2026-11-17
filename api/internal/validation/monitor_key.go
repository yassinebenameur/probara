package validation

import (
	"fmt"
	"regexp"
)

// MaxMonitorKeyLength bounds a monitor key.
const MaxMonitorKeyLength = 64

// monitorKeyPattern is a lowercase slug: letters, digits, '.', '_' and '-',
// starting with a letter or digit. Lowercase only, so two keys that differ in
// case never name two monitors in a file meant for review.
var monitorKeyPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`)

// ValidateMonitorKey checks the format of a monitor's config-as-code key
// (POST /monitors/apply). Uniqueness among the workspace's live monitors is
// the database's (idx_monitors_tenant_key).
func ValidateMonitorKey(key string) error {
	if key == "" {
		return fmt.Errorf("key must not be empty")
	}
	if len(key) > MaxMonitorKeyLength {
		return fmt.Errorf("key must be at most %d characters", MaxMonitorKeyLength)
	}
	if !monitorKeyPattern.MatchString(key) {
		return fmt.Errorf("key %q must be lowercase letters, digits, '.', '_' or '-', starting with a letter or digit", key)
	}
	return nil
}
