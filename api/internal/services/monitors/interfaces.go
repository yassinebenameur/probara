package monitors

import (
	"context"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/api/internal/models"
)

// MonitorService defines the interface for monitor operations
type MonitorService interface {
	// CreateMonitor creates a new monitor
	CreateMonitor(ctx context.Context, tenantID uuid.UUID, req *models.CreateMonitorRequest) (*models.Monitor, error)

	// GetMonitor retrieves a monitor by ID
	GetMonitor(ctx context.Context, tenantID, monitorID uuid.UUID) (*models.Monitor, error)

	// ListMonitors lists monitors with optional filters and pagination
	ListMonitors(ctx context.Context, tenantID uuid.UUID, tag *string, enabled *bool, page, pageSize int) (*models.MonitorListResponse, error)

	// UpdateMonitor updates a monitor (partial update)
	UpdateMonitor(ctx context.Context, tenantID, monitorID uuid.UUID, req *models.UpdateMonitorRequest) (*models.Monitor, error)

	// DeleteMonitor deletes a monitor
	DeleteMonitor(ctx context.Context, tenantID, monitorID uuid.UUID) error

	// BulkDeleteMonitors soft-deletes many monitors in a single statement.
	// Returns the number of newly tombstoned rows.
	BulkDeleteMonitors(ctx context.Context, tenantID uuid.UUID, monitorIDs []uuid.UUID) (int64, error)

	// DeleteMonitorHistory clears check, alert, and analytics history while preserving the monitor.
	DeleteMonitorHistory(ctx context.Context, tenantID, monitorID uuid.UUID) error

	// BulkUpdateAlertPolicy attaches or detaches a single alert policy across many monitors.
	BulkUpdateAlertPolicy(
		ctx context.Context,
		tenantID uuid.UUID,
		monitorIDs []uuid.UUID,
		policyID uuid.UUID,
		op models.BulkAlertPolicyOp,
	) (*models.BulkUpdateAlertPolicyResponse, error)

	// BulkUpdateAlerting applies notification-routing fields to many monitors.
	BulkUpdateAlerting(
		ctx context.Context,
		tenantID uuid.UUID,
		monitorIDs []uuid.UUID,
		threshold *int,
		mode *string,
		channels []models.MonitorChannelAssignment,
	) (int, error)
}

// Ensure Service implements MonitorService
var _ MonitorService = (*Service)(nil)
