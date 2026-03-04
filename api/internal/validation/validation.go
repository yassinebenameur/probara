package validation

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/google/uuid"
	"github.com/yassinebenameur/probara/api/internal/models"
	"github.com/yassinebenameur/probara/shared/notifications"
)

const (
	minIntervalSeconds = 10
	maxIntervalSeconds = 86400
)

var (
	slugRegex = regexp.MustCompile(`^[a-z0-9-]+$`)
)

// activeCheckTypes are monitor types that require timeout validation
var activeCheckTypes = map[models.MonitorType]bool{
	models.MonitorTypeHTTP:             true,
	models.MonitorTypePing:             true,
	models.MonitorTypeSIP:              true,
	models.MonitorTypeDNS:              true,
	models.MonitorTypeGRPC:             true,
	models.MonitorTypeSyntheticAPI:     true,
	models.MonitorTypeSyntheticBrowser: true,
}

// ValidateMonitor validates a CreateMonitorRequest
func ValidateMonitor(req *models.CreateMonitorRequest) error {
	return ValidateMonitorWithRegistry(req, DefaultRegistry)
}

// ValidateMonitorWithRegistry validates a CreateMonitorRequest using a custom registry
func ValidateMonitorWithRegistry(req *models.CreateMonitorRequest, registry *ValidatorRegistry) error {
	if req.Name == "" {
		return fmt.Errorf("name is required")
	}

	// Validate type using registry
	if !registry.Has(req.Type) {
		return fmt.Errorf("unknown monitor type: %s", req.Type)
	}

	// Validate config is present
	if len(req.Config) == 0 {
		return fmt.Errorf("config is required")
	}

	// Validate type-specific config using registry
	if err := registry.Validate(req.Type, req.Config); err != nil {
		return fmt.Errorf("%s config validation failed: %w", req.Type, err)
	}

	if req.IntervalSeconds < minIntervalSeconds || req.IntervalSeconds > maxIntervalSeconds {
		return fmt.Errorf("interval_seconds must be between %d and %d", minIntervalSeconds, maxIntervalSeconds)
	}

	// Timeout validation only applies to active check types (HTTP, Ping)
	if activeCheckTypes[req.Type] {
		if req.TimeoutSeconds <= 0 {
			return fmt.Errorf("timeout_seconds must be greater than 0")
		}

		if req.TimeoutSeconds >= req.IntervalSeconds {
			return fmt.Errorf("timeout_seconds must be less than interval_seconds")
		}
	}

	return nil
}

// ValidateMonitorUpdate validates an UpdateMonitorRequest
func ValidateMonitorUpdate(req *models.UpdateMonitorRequest, existingMonitor *models.Monitor) error {
	return ValidateMonitorUpdateWithRegistry(req, existingMonitor, DefaultRegistry)
}

// ValidateMonitorUpdateWithRegistry validates an UpdateMonitorRequest using a custom registry
func ValidateMonitorUpdateWithRegistry(req *models.UpdateMonitorRequest, existingMonitor *models.Monitor, registry *ValidatorRegistry) error {
	// Validate type if being updated
	if req.Type != nil {
		if !registry.Has(*req.Type) {
			return fmt.Errorf("unknown monitor type: %s", *req.Type)
		}
	}

	// Validate config if being updated
	if len(req.Config) > 0 {
		monitorType := existingMonitor.Type
		if req.Type != nil {
			monitorType = *req.Type
		}

		if err := registry.Validate(monitorType, req.Config); err != nil {
			return fmt.Errorf("%s config validation failed: %w", monitorType, err)
		}
	}

	if req.IntervalSeconds != nil {
		if *req.IntervalSeconds < minIntervalSeconds || *req.IntervalSeconds > maxIntervalSeconds {
			return fmt.Errorf("interval_seconds must be between %d and %d", minIntervalSeconds, maxIntervalSeconds)
		}
	}

	// Determine the monitor type
	monitorType := existingMonitor.Type
	if req.Type != nil {
		monitorType = *req.Type
	}

	// Timeout validation only applies to active check types
	if activeCheckTypes[monitorType] {
		if req.TimeoutSeconds != nil {
			if *req.TimeoutSeconds <= 0 {
				return fmt.Errorf("timeout_seconds must be greater than 0")
			}
		}

		// If both interval and timeout are being updated
		if req.IntervalSeconds != nil && req.TimeoutSeconds != nil {
			if *req.TimeoutSeconds >= *req.IntervalSeconds {
				return fmt.Errorf("timeout_seconds must be less than interval_seconds")
			}
		}

		// If only timeout is being updated
		if req.TimeoutSeconds != nil && req.IntervalSeconds == nil {
			if *req.TimeoutSeconds >= existingMonitor.IntervalSeconds {
				return fmt.Errorf("timeout_seconds must be less than interval_seconds")
			}
		}

		// If only interval is being updated
		if req.IntervalSeconds != nil && req.TimeoutSeconds == nil {
			if existingMonitor.TimeoutSeconds >= *req.IntervalSeconds {
				return fmt.Errorf("timeout_seconds must be less than interval_seconds")
			}
		}
	}

	return nil
}

// ValidateAlertPolicy validates a CreateAlertPolicyRequest
func ValidateAlertPolicy(req *models.CreateAlertPolicyRequest) error {
	if req.Name == "" {
		return fmt.Errorf("name is required")
	}

	if req.FailureThreshold <= 0 {
		return fmt.Errorf("failure_threshold must be greater than 0")
	}

	if req.FailureWindowSeconds <= 0 {
		return fmt.Errorf("failure_window_seconds must be greater than 0")
	}

	return nil
}

// ValidateAlertPolicyUpdate validates an UpdateAlertPolicyRequest
func ValidateAlertPolicyUpdate(req *models.UpdateAlertPolicyRequest) error {
	if req.FailureThreshold != nil && *req.FailureThreshold <= 0 {
		return fmt.Errorf("failure_threshold must be greater than 0")
	}

	if req.FailureWindowSeconds != nil && *req.FailureWindowSeconds <= 0 {
		return fmt.Errorf("failure_window_seconds must be greater than 0")
	}

	return nil
}

// ValidateStatusPage validates a CreateStatusPageRequest
func ValidateStatusPage(req *models.CreateStatusPageRequest) error {
	if req.Slug == "" {
		return fmt.Errorf("slug is required")
	}

	if !slugRegex.MatchString(req.Slug) {
		return fmt.Errorf("slug must contain only lowercase letters, numbers, and hyphens")
	}

	if req.Title == "" {
		return fmt.Errorf("title is required")
	}

	if req.PrimaryColor != nil && !isValidColor(*req.PrimaryColor) {
		return fmt.Errorf("primary_color must be a valid hex color (e.g., #1e90ff)")
	}

	if req.SecondaryColor != nil && !isValidColor(*req.SecondaryColor) {
		return fmt.Errorf("secondary_color must be a valid hex color (e.g., #ffffff)")
	}

	if err := validateStatusPageSettings(req.Settings); err != nil {
		return err
	}

	if err := validateStatusPageDisplayNames(req.MonitorDisplayNames); err != nil {
		return err
	}

	return nil
}

// ValidateStatusPageUpdate validates an UpdateStatusPageRequest
func ValidateStatusPageUpdate(req *models.UpdateStatusPageRequest) error {
	if req.Slug != nil {
		if !slugRegex.MatchString(*req.Slug) {
			return fmt.Errorf("slug must contain only lowercase letters, numbers, and hyphens")
		}
	}

	if req.PrimaryColor != nil && !isValidColor(*req.PrimaryColor) {
		return fmt.Errorf("primary_color must be a valid hex color (e.g., #1e90ff)")
	}

	if req.SecondaryColor != nil && !isValidColor(*req.SecondaryColor) {
		return fmt.Errorf("secondary_color must be a valid hex color (e.g., #ffffff)")
	}

	if err := validateStatusPageSettings(req.Settings); err != nil {
		return err
	}

	if req.MonitorDisplayNames != nil {
		if err := validateStatusPageDisplayNames(*req.MonitorDisplayNames); err != nil {
			return err
		}
	}

	return nil
}

func isValidColor(color string) bool {
	hexRegex := regexp.MustCompile(`^#[0-9A-Fa-f]{6}$`)
	return hexRegex.MatchString(color)
}

func validateStatusPageSettings(settings *models.StatusPageSettings) error {
	if settings == nil {
		return nil
	}
	if settings.FooterText != nil {
		if len(*settings.FooterText) > 250 {
			return fmt.Errorf("footer_text must be 250 characters or less")
		}
	}
	return nil
}

func validateStatusPageDisplayNames(displayNames map[string]string) error {
	for idStr, name := range displayNames {
		if _, err := uuid.Parse(idStr); err != nil {
			return fmt.Errorf("monitor_display_names contains invalid monitor id: %s", idStr)
		}
		if len(strings.TrimSpace(name)) > 80 {
			return fmt.Errorf("monitor_display_names display name must be 80 characters or less")
		}
	}
	return nil
}

// ValidateAlertChannel validates a CreateAlertChannelRequest
func ValidateAlertChannel(req *models.CreateAlertChannelRequest) error {
	if strings.TrimSpace(req.Name) == "" {
		return fmt.Errorf("name is required")
	}

	switch req.Type {
	case models.AlertChannelTypeTeams, models.AlertChannelTypeEmail:
		// ok
	default:
		return fmt.Errorf("invalid alert channel type")
	}

	if len(req.Config) == 0 {
		return fmt.Errorf("config is required")
	}

	if req.Type == models.AlertChannelTypeTeams {
		if _, err := notifications.ParseTeamsWebhookConfig(req.Config); err != nil {
			return err
		}
	} else if req.Type == models.AlertChannelTypeEmail {
		if _, err := notifications.ParseEmailConfig(req.Config); err != nil {
			return err
		}
	}

	return nil
}

// ValidateAlertChannelUpdate validates an UpdateAlertChannelRequest
func ValidateAlertChannelUpdate(req *models.UpdateAlertChannelRequest) error {
	if req.Name != nil && strings.TrimSpace(*req.Name) == "" {
		return fmt.Errorf("name cannot be empty")
	}

	if len(req.Config) > 0 {
		var tmp map[string]interface{}
		if err := json.Unmarshal(req.Config, &tmp); err != nil {
			return fmt.Errorf("invalid config: %w", err)
		}
	}

	return nil
}

// ValidateTenantDataRetentionDays validates tenant data retention settings.
func ValidateTenantDataRetentionDays(days int) error {
	if days == models.DataRetentionUnlimited {
		return nil
	}
	if days < models.MinDataRetentionDays || days > models.MaxDataRetentionDays {
		return fmt.Errorf(
			"data_retention_days must be 0 or between %d and %d",
			models.MinDataRetentionDays,
			models.MaxDataRetentionDays,
		)
	}
	return nil
}
