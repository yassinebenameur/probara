package alerts

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/api/internal/models"
)

// AlertService defines the interface for alert operations
type AlertService interface {
	// ListAlerts lists alerts with filtering, sorting and pagination
	ListAlerts(ctx context.Context, tenantID uuid.UUID, params *models.AlertListParams, sort ListSort) (*models.AlertListResponse, error)

	// GetAlert retrieves an alert by ID
	GetAlert(ctx context.Context, tenantID, alertID uuid.UUID) (*models.AlertWithDetails, error)

	// GetRecentAlerts retrieves the most recent alerts for dashboard
	GetRecentAlerts(ctx context.Context, tenantID uuid.UUID, limit int) ([]models.AlertWithDetails, error)

	// GetRecentAlertsForTags retrieves recent alerts scoped to monitors carrying any of the tags.
	GetRecentAlertsForTags(ctx context.Context, tenantID uuid.UUID, tags []string, limit int) ([]models.AlertWithDetails, error)

	// AcknowledgeAlert marks an alert as acknowledged
	AcknowledgeAlert(ctx context.Context, tenantID, alertID uuid.UUID) (*models.AlertWithDetails, error)

	// ResolveAlert marks an alert as resolved
	ResolveAlert(ctx context.Context, tenantID, alertID uuid.UUID) (*models.AlertWithDetails, error)

	// BulkAcknowledge acknowledges each listed active alert; the rest are
	// reported per id, never failing the batch.
	BulkAcknowledge(ctx context.Context, tenantID uuid.UUID, alertIDs []uuid.UUID) (*BulkResult, error)

	// BulkResolve resolves each listed open alert; the rest are reported per id.
	BulkResolve(ctx context.Context, tenantID uuid.UUID, alertIDs []uuid.UUID) (*BulkResult, error)

	// UnacknowledgeAlert returns an acknowledged alert to active.
	UnacknowledgeAlert(ctx context.Context, tenantID, alertID uuid.UUID) (*models.AlertWithDetails, error)

	// SnoozeAlert mutes an open alert's notifications until a time.
	SnoozeAlert(ctx context.Context, tenantID, alertID uuid.UUID, until time.Time) (*models.AlertWithDetails, error)

	// UnsnoozeAlert ends an open alert's running snooze now.
	UnsnoozeAlert(ctx context.Context, tenantID, alertID uuid.UUID) (*models.AlertWithDetails, error)

	// BulkUnacknowledge, BulkSnooze and BulkUnsnooze apply those to many
	// alerts; the rest are reported per id, never failing the batch.
	BulkUnacknowledge(ctx context.Context, tenantID uuid.UUID, alertIDs []uuid.UUID) (*BulkResult, error)
	BulkSnooze(ctx context.Context, tenantID uuid.UUID, alertIDs []uuid.UUID, until time.Time) (*BulkResult, error)
	BulkUnsnooze(ctx context.Context, tenantID uuid.UUID, alertIDs []uuid.UUID) (*BulkResult, error)

	// GetActiveAlertForMonitor gets the active alert for a monitor if one exists
	GetActiveAlertForMonitor(ctx context.Context, tenantID, monitorID uuid.UUID) (*models.Alert, error)
}

// Ensure Service implements AlertService
var _ AlertService = (*Service)(nil)
