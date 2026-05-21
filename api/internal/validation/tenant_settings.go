package validation

import (
	"fmt"
	"strings"
)

const (
	maxDashboardGroupTags      = 50
	maxDashboardGroupTagLength = 64
)

// ValidateDashboardGroupTags ensures the curated dashboard group tag list is well-formed.
// Existence in the tenant's tag universe is checked at the service layer, not here.
func ValidateDashboardGroupTags(tags []string) error {
	if len(tags) > maxDashboardGroupTags {
		return fmt.Errorf("dashboard_group_tags must contain at most %d entries", maxDashboardGroupTags)
	}
	seen := make(map[string]struct{}, len(tags))
	for _, t := range tags {
		trimmed := strings.TrimSpace(t)
		if trimmed == "" {
			return fmt.Errorf("dashboard_group_tags must not contain empty entries")
		}
		if len(t) > maxDashboardGroupTagLength {
			return fmt.Errorf("dashboard_group_tags entries must be at most %d characters", maxDashboardGroupTagLength)
		}
		if _, dup := seen[t]; dup {
			return fmt.Errorf("dashboard_group_tags must not contain duplicates: %q", t)
		}
		seen[t] = struct{}{}
	}
	return nil
}
