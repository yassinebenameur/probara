package maintenancewindows

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/api/internal/models"
)

// MaintenanceWindowService defines the interface for maintenance window operations.
type MaintenanceWindowService interface {
	Create(ctx context.Context, tenantID uuid.UUID, req *models.CreateMaintenanceWindowRequest) (*models.MaintenanceWindow, error)
	Get(ctx context.Context, tenantID, windowID uuid.UUID) (*models.MaintenanceWindow, error)
	List(ctx context.Context, tenantID uuid.UUID, status string, monitorID uuid.UUID, page, pageSize int) (*models.MaintenanceWindowListResponse, error)
	Update(ctx context.Context, tenantID, windowID uuid.UUID, req *models.UpdateMaintenanceWindowRequest) (*models.MaintenanceWindow, error)
	Delete(ctx context.Context, tenantID, windowID uuid.UUID) error
	SnoozeMonitor(ctx context.Context, tenantID, monitorID uuid.UUID, until time.Time) (*models.MaintenanceWindow, error)
}

// Ensure Service implements MaintenanceWindowService
var _ MaintenanceWindowService = (*Service)(nil)
