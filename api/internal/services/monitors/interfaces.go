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
}

// Ensure Service implements MonitorService
var _ MonitorService = (*Service)(nil)
