package incidents

import (
	"context"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/api/internal/models"
)

// IncidentService defines the incident service contract.
type IncidentService interface {
	// CreateIncident creates a new manual incident with optional metadata and initial links.
	CreateIncident(ctx context.Context, tenantID uuid.UUID, req *models.CreateIncidentRequest) (*models.IncidentDetail, error)

	// GetIncident retrieves an incident by ID.
	GetIncident(ctx context.Context, tenantID, incidentID uuid.UUID) (*models.IncidentDetail, error)

	// ListIncidents lists incidents with pagination.
	ListIncidents(ctx context.Context, tenantID uuid.UUID, page, pageSize int) (*models.IncidentListResponse, error)

	// UpdateIncident updates an incident, including metadata.
	UpdateIncident(ctx context.Context, tenantID, incidentID uuid.UUID, req *models.UpdateIncidentRequest) (*models.IncidentDetail, error)

	// TransitionIncidentState moves an incident to another state.
	TransitionIncidentState(ctx context.Context, tenantID, incidentID uuid.UUID, req *models.TransitionIncidentStateRequest) (*models.IncidentDetail, error)

	// CreateIncidentTimelineEntry appends a timeline entry to an incident.
	CreateIncidentTimelineEntry(ctx context.Context, tenantID, incidentID uuid.UUID, req *models.CreateIncidentTimelineEntryRequest) (*models.IncidentDetail, error)

	// AttachAlert links an alert to an incident.
	AttachAlert(ctx context.Context, tenantID, incidentID, alertID uuid.UUID) (*models.IncidentDetail, error)

	// DetachAlert removes an alert link from an incident.
	DetachAlert(ctx context.Context, tenantID, incidentID, alertID uuid.UUID) (*models.IncidentDetail, error)

	// AttachMonitor links a monitor to an incident.
	AttachMonitor(ctx context.Context, tenantID, incidentID, monitorID uuid.UUID) (*models.IncidentDetail, error)

	// DetachMonitor removes a monitor link from an incident.
	DetachMonitor(ctx context.Context, tenantID, incidentID, monitorID uuid.UUID) (*models.IncidentDetail, error)

	// PublishIncidentToStatusPage publishes an incident to a status page.
	PublishIncidentToStatusPage(ctx context.Context, tenantID, incidentID, statusPageID uuid.UUID, req *models.UpsertIncidentPublicationRequest) (*models.IncidentDetail, error)

	// UnpublishIncidentFromStatusPage removes an incident publication from a status page.
	UnpublishIncidentFromStatusPage(ctx context.Context, tenantID, incidentID, statusPageID uuid.UUID) (*models.IncidentDetail, error)

	// RequestAIAnalysis creates a pending AI root cause analysis row for an incident.
	RequestAIAnalysis(ctx context.Context, tenantID, incidentID uuid.UUID, requestedBy *uuid.UUID) (*models.IncidentAIAnalysis, error)

	// GetLatestAIAnalysis returns the most recent AI analysis for an incident (nil if none).
	GetLatestAIAnalysis(ctx context.Context, tenantID, incidentID uuid.UUID) (*models.IncidentAIAnalysis, error)

	// EnsureIncidentForAlert auto-creates or reuses an incident for a firing alert when policy settings allow it.
	EnsureIncidentForAlert(ctx context.Context, tenantID uuid.UUID, alert *models.AlertWithDetails) error

	// RecordAlertRecoveryIfNeeded appends a recovery timeline entry when all linked alerts are resolved.
	RecordAlertRecoveryIfNeeded(ctx context.Context, tenantID, alertID uuid.UUID) error
}

// Ensure Service implements IncidentService.
var _ IncidentService = (*Service)(nil)
