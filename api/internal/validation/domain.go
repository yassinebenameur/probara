package validation

import (
	"encoding/json"
	"fmt"

	"github.com/yassinebenameur/probara/shared/models"
)

// DomainConfigValidator validates domain registration-expiry monitors. The
// rules live on models.DomainMonitorConfig, which the worker applies too.
type DomainConfigValidator struct{}

func (*DomainConfigValidator) ValidateConfig(raw json.RawMessage) error {
	var config models.DomainMonitorConfig
	if err := json.Unmarshal(raw, &config); err != nil {
		return fmt.Errorf("invalid domain config")
	}
	return config.Validate()
}

// ValidateSchedule enforces the one-hour interval floor (registries
// rate-limit lookups).
func (*DomainConfigValidator) ValidateSchedule(raw json.RawMessage, intervalSeconds, timeoutSeconds int) error {
	return models.ValidateDomainSchedule(raw, intervalSeconds, timeoutSeconds)
}
