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

	// ExportMonitors exports all tenant monitors as portable YAML
	ExportMonitors(ctx context.Context, tenantID uuid.UUID) ([]byte, error)
}

// Ensure Service implements ImportService
var _ ImportService = (*Service)(nil)
