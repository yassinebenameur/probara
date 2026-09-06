package validation

import (
	"encoding/json"
	"fmt"
	"github.com/yassinebenameur/probara/shared/models"
)

type PrometheusConfigValidator struct{}

func (*PrometheusConfigValidator) ValidateConfig(raw json.RawMessage) error {
	var config models.PrometheusMonitorConfig
	if err := json.Unmarshal(raw, &config); err != nil {
		return fmt.Errorf("invalid prometheus config")
	}
	return config.Validate()
}
