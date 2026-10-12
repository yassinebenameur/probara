package validation

import (
	"encoding/json"
	"fmt"

	"github.com/yassinebenameur/probara/shared/models"
)

type VaultConfigValidator struct{}

func (*VaultConfigValidator) ValidateConfig(raw json.RawMessage) error {
	var config models.VaultMonitorConfig
	if err := json.Unmarshal(raw, &config); err != nil {
		return fmt.Errorf("invalid vault config")
	}
	return config.Validate()
}
