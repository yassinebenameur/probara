package validation

import (
	"fmt"

	"github.com/google/uuid"
)

// ValidateEscalationPolicyField checks a monitor's escalation_policy_id
// against its notification_mode: a UUID when set, and required when a
// monitor is created in mode 'policy'. On an update the service checks the
// stored mode too (a monitor already in 'policy' mode may switch policy
// alone), and that the policy is the tenant's.
func ValidateEscalationPolicyField(mode, policyID *string, creating bool) error {
	hasPolicy := policyID != nil && *policyID != ""
	if hasPolicy {
		if _, err := uuid.Parse(*policyID); err != nil {
			return fmt.Errorf("invalid escalation_policy_id %q", *policyID)
		}
	}
	if mode == nil {
		return nil
	}
	if *mode == "policy" && !hasPolicy && creating {
		return fmt.Errorf("notification_mode 'policy' needs an escalation_policy_id")
	}
	if *mode != "policy" && hasPolicy {
		return fmt.Errorf("escalation_policy_id applies to notification_mode 'policy' only")
	}
	return nil
}
