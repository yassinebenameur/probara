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

	// GetTemplateState returns the custom-template state (draft, published
	// version, history) for a page
	GetTemplateState(ctx context.Context, tenantID, pageID uuid.UUID) (*models.StatusPageTemplateState, error)

	// GetTemplateVersionSource returns the source of a stored template version
	GetTemplateVersionSource(ctx context.Context, tenantID, pageID uuid.UUID, version int) (string, error)

	// SaveTemplateDraft validates and upserts the page's draft template
	SaveTemplateDraft(ctx context.Context, tenantID, pageID uuid.UUID, source string) error

	// DiscardTemplateDraft deletes the page's draft template (idempotent)
	DiscardTemplateDraft(ctx context.Context, tenantID, pageID uuid.UUID) error

	// PublishTemplateDraft promotes the draft to the published template
	PublishTemplateDraft(ctx context.Context, tenantID, pageID uuid.UUID) error

	// RevertTemplate republishes an archived template version
	RevertTemplate(ctx context.Context, tenantID, pageID uuid.UUID, version int) error

	// ResetTemplate archives the published custom template so the page
	// renders with the built-in template again (idempotent)
	ResetTemplate(ctx context.Context, tenantID, pageID uuid.UUID) error

	// ListLibraryTemplates lists the tenant's reusable templates (no source)
	ListLibraryTemplates(ctx context.Context, tenantID uuid.UUID) (*models.StatusPageLibraryTemplateListResponse, error)

	// GetLibraryTemplate returns one library template including its source
	GetLibraryTemplate(ctx context.Context, tenantID, templateID uuid.UUID) (*models.StatusPageLibraryTemplate, error)

	// CreateLibraryTemplate validates and stores a new library template
	CreateLibraryTemplate(ctx context.Context, tenantID uuid.UUID, req *models.CreateStatusPageLibraryTemplateRequest) (*models.StatusPageLibraryTemplate, error)

	// UpdateLibraryTemplate partially updates a library template
	UpdateLibraryTemplate(ctx context.Context, tenantID, templateID uuid.UUID, req *models.UpdateStatusPageLibraryTemplateRequest) (*models.StatusPageLibraryTemplate, error)

	// DeleteLibraryTemplate removes a library template (pages keep applied copies)
	DeleteLibraryTemplate(ctx context.Context, tenantID, templateID uuid.UUID) error
}

// Ensure Service implements StatusPageService
var _ StatusPageService = (*Service)(nil)
