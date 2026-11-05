package validation

import (
	"fmt"

	sharedmodels "github.com/yassinebenameur/probara/shared/models"
)

// validateHTTPCapture bounds the failure-response capture's body excerpt.
func validateHTTPCapture(config sharedmodels.HTTPMonitorConfig) error {
	if config.CaptureBodyBytes != nil {
		n := *config.CaptureBodyBytes
		if n < 1 || n > sharedmodels.HTTPCaptureMaxBodyBytes {
			return fmt.Errorf("capture_body_bytes must be between 1 and %d", sharedmodels.HTTPCaptureMaxBodyBytes)
		}
	}
	return nil
}
