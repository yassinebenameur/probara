package importservice

import (
	"context"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/api/internal/models"
)

// ImportService defines the interface for import operations
type ImportService interface {
	// ParseFile auto-detects the file format and parses its contents
	ParseFile(data []byte, filename string) (*models.ImportPreviewResponse, error)

	// ExecuteImport executes the import with the given rows and mapping
	ExecuteImport(ctx context.Context, tenantID uuid.UUID, req *models.ImportExecuteRequest) (*models.ImportExecuteResponse, error)

	// ExportMonitors exports the tenant's monitors as portable YAML
	// (version 2), all of them or only the keyed ones
	ExportMonitors(ctx context.Context, tenantID uuid.UUID, opts ExportOptions) ([]byte, error)

	// PlanApply computes what applying a monitors document would do,
	// writing nothing
	PlanApply(ctx context.Context, tenantID uuid.UUID, req *models.MonitorApplyRequest) (*models.MonitorApplyPlan, error)

	// Apply brings the tenant's keyed monitors in line with a monitors
	// document, all or nothing
	Apply(ctx context.Context, tenantID uuid.UUID, req *models.MonitorApplyRequest) (*models.MonitorApplyResult, error)
}

// Ensure Service implements ImportService
var _ ImportService = (*Service)(nil)
