package results

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/api/internal/models"
)

// ResultsService defines the interface for results operations
type ResultsService interface {
	// GetMonitorResults retrieves check results for a monitor
	GetMonitorResults(ctx context.Context, tenantID, monitorID uuid.UUID, limit int, since *time.Time) (*models.MonitorResultsResponse, error)
	GetMonitorAnalytics(ctx context.Context, tenantID, monitorID uuid.UUID, rangeValue models.MonitorAnalyticsRange) (*models.MonitorAnalyticsResponse, error)
}

// Ensure Service implements ResultsService
var _ ResultsService = (*Service)(nil)
