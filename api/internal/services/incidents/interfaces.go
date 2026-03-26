package incidents

import (
	"context"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/api/internal/models"
)

// statusPublisher is a placeholder for incident-driven status fan-out wiring.
// The MVP service does not publish updates yet, but the constructor keeps the
// dependency shape for later iterations.
type statusPublisher interface{}

// IncidentService defines the incident service contract.
type IncidentService interface {
	// CreateIncident creates a new manual incident.
	CreateIncident(ctx context.Context, tenantID uuid.UUID, req *models.CreateIncidentRequest) (*models.IncidentDetail, error)

	// GetIncident retrieves an incident by ID.
	GetIncident(ctx context.Context, tenantID, incidentID uuid.UUID) (*models.IncidentDetail, error)

	// ListIncidents lists incidents with pagination.
	ListIncidents(ctx context.Context, tenantID uuid.UUID, page, pageSize int) (*models.IncidentListResponse, error)

	// UpdateIncident updates an incident.
	UpdateIncident(ctx context.Context, tenantID, incidentID uuid.UUID, req *models.UpdateIncidentRequest) (*models.IncidentDetail, error)

	// TransitionIncidentState moves an incident to another state.
	TransitionIncidentState(ctx context.Context, tenantID, incidentID uuid.UUID, req *models.TransitionIncidentStateRequest) (*models.IncidentDetail, error)
}

// Ensure Service implements IncidentService.
var _ IncidentService = (*Service)(nil)
