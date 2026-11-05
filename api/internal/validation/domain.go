package validation

import (
	"encoding/json"
	"fmt"
	"reflect"

	sharedmodels "github.com/yassinebenameur/probara/shared/models"
)

// DomainConfigValidator validates domain (registration expiry) monitors
// through sharedmodels.DomainMonitorConfig.Validate, which the worker runs
// too.
type DomainConfigValidator struct{}

func (*DomainConfigValidator) ValidateConfig(raw json.RawMessage) error {
	var config sharedmodels.DomainMonitorConfig
	if err := json.Unmarshal(raw, &config); err != nil {
		return fmt.Errorf("invalid domain config: %w", err)
	}
	return config.Validate()
}

// ValidateSchedule enforces the one-hour interval floor
// (sharedmodels.ValidateDomainSchedule).
func (*DomainConfigValidator) ValidateSchedule(raw json.RawMessage, intervalSeconds, timeoutSeconds int) error {
	return sharedmodels.ValidateDomainSchedule(raw, intervalSeconds, timeoutSeconds)
}

func (*DomainConfigValidator) ConfigType() reflect.Type {
	return reflect.TypeFor[sharedmodels.DomainMonitorConfig]()
}

var _ ScheduleAwareValidator = (*DomainConfigValidator)(nil)
