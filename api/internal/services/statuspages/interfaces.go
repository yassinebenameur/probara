package statuspages

import (
	"context"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/api/internal/models"
)

// StatusPageService defines the interface for status page operations
type StatusPageService interface {
	// CreateStatusPage creates a new status page
	CreateStatusPage(ctx context.Context, tenantID uuid.UUID, req *models.CreateStatusPageRequest) (*models.StatusPage, error)

	// GetStatusPage retrieves a status page by ID
	GetStatusPage(ctx context.Context, tenantID, pageID uuid.UUID) (*models.StatusPage, error)

	// ListStatusPages lists status pages with pagination
	ListStatusPages(ctx context.Context, tenantID uuid.UUID, page, pageSize int) (*models.StatusPageListResponse, error)

	// UpdateStatusPage updates a status page (partial update)
	UpdateStatusPage(ctx context.Context, tenantID, pageID uuid.UUID, req *models.UpdateStatusPageRequest) (*models.StatusPage, error)

	// DeleteStatusPage deletes a status page
	DeleteStatusPage(ctx context.Context, tenantID, pageID uuid.UUID) error
}

// Ensure Service implements StatusPageService
var _ StatusPageService = (*Service)(nil)
