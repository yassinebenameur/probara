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
	// GetSummary returns the lightweight dashboard data for first paint.
	GetSummary(ctx context.Context, tenantID uuid.UUID, params *models.DashboardOverviewQuery) (*models.DashboardSummaryResponse, error)
	// GetProblemMonitors returns the problem monitors dashboard section.
	GetProblemMonitors(ctx context.Context, tenantID uuid.UUID, params *models.DashboardListQuery) (*models.DashboardProblemMonitorsResponse, error)
	// GetRecentFailures returns the recent failures dashboard section.
	GetRecentFailures(ctx context.Context, tenantID uuid.UUID, params *models.DashboardListQuery) (*models.DashboardRecentFailuresResponse, error)
	// GetRecentAlerts returns the recent alerts dashboard section.
	GetRecentAlerts(ctx context.Context, tenantID uuid.UUID, params *models.DashboardListQuery) (*models.DashboardRecentAlertsResponse, error)
	// GetGroupSparkline returns a 12-bucket uptime series for a single group.
	GetGroupSparkline(ctx context.Context, tenantID uuid.UUID, params *models.DashboardGroupSparklineQuery) (*models.DashboardGroupSparklineResponse, error)
}

// Ensure Service implements DashboardService.
var _ DashboardService = (*Service)(nil)
