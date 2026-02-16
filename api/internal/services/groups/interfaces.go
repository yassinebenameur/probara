package groups

import (
	"context"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/api/internal/models"
)

// GroupService defines the interface for group operations
type GroupService interface {
	// AddMonitorsToGroup adds monitors to a group
	AddMonitorsToGroup(ctx context.Context, tenantID, groupID uuid.UUID, monitorIDs []uuid.UUID) error

	// RemoveMonitorsFromGroup removes monitors from a group
	RemoveMonitorsFromGroup(ctx context.Context, tenantID, groupID uuid.UUID, monitorIDs []uuid.UUID) error

	// GetGroupMembers retrieves all monitors in a group
	GetGroupMembers(ctx context.Context, tenantID, groupID uuid.UUID) ([]models.Monitor, error)

	// GetMonitorGroups retrieves all groups a monitor belongs to
	GetMonitorGroups(ctx context.Context, tenantID, monitorID uuid.UUID) ([]models.Monitor, error)

	// GetGroupStatus calculates the aggregated status for a group
	GetGroupStatus(ctx context.Context, tenantID, groupID uuid.UUID) (string, error)
}

// Ensure Service implements GroupService
var _ GroupService = (*Service)(nil)
