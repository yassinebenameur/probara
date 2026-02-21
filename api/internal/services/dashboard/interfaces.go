package dashboard

import (
	"context"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/api/internal/models"
)

// DashboardService defines dashboard overview operations.
type DashboardService interface {
	// GetOverview returns aggregated dashboard data for a tenant.
	GetOverview(ctx context.Context, tenantID uuid.UUID, params *models.DashboardOverviewQuery) (*models.DashboardOverviewResponse, error)
}

// Ensure Service implements DashboardService.
var _ DashboardService = (*Service)(nil)
