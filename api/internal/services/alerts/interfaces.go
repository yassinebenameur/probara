package alerts

import (
	"context"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/api/internal/models"
)

// AlertService defines the interface for alert operations
type AlertService interface {
	// ListAlerts lists alerts with filtering and pagination
	ListAlerts(ctx context.Context, tenantID uuid.UUID, params *models.AlertListParams) (*models.AlertListResponse, error)

	// GetAlert retrieves an alert by ID
	GetAlert(ctx context.Context, tenantID, alertID uuid.UUID) (*models.AlertWithDetails, error)

	// GetRecentAlerts retrieves the most recent alerts for dashboard
	GetRecentAlerts(ctx context.Context, tenantID uuid.UUID, limit int) ([]models.AlertWithDetails, error)

	// GetRecentAlertsForTags retrieves recent alerts scoped to monitors matching all tags.
	GetRecentAlertsForTags(ctx context.Context, tenantID uuid.UUID, tags []string, limit int) ([]models.AlertWithDetails, error)

	// AcknowledgeAlert marks an alert as acknowledged
	AcknowledgeAlert(ctx context.Context, tenantID, alertID uuid.UUID) (*models.AlertWithDetails, error)

	// ResolveAlert marks an alert as resolved
	ResolveAlert(ctx context.Context, tenantID, alertID uuid.UUID) (*models.AlertWithDetails, error)

	// CreateAlert creates a new alert (used by alerter service)
	CreateAlert(ctx context.Context, tenantID, monitorID, policyID uuid.UUID, failureCount int, lastError *string) (*models.AlertWithDetails, error)

	// GetActiveAlertForMonitor gets the active alert for a monitor if one exists
	GetActiveAlertForMonitor(ctx context.Context, tenantID, monitorID uuid.UUID) (*models.Alert, error)

	// GetAlertCountsByPolicy gets alert counts grouped by policy
	GetAlertCountsByPolicy(ctx context.Context, tenantID uuid.UUID) (map[uuid.UUID]int, error)

	// GetMonitorCountsByPolicy gets monitor counts grouped by policy
	GetMonitorCountsByPolicy(ctx context.Context, tenantID uuid.UUID) (map[uuid.UUID]int, error)

}

// Ensure Service implements AlertService
var _ AlertService = (*Service)(nil)
