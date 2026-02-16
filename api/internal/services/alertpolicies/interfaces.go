package alertpolicies

import (
	"context"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/api/internal/models"
)

// AlertPolicyService defines the interface for alert policy operations
type AlertPolicyService interface {
	// CreateAlertPolicy creates a new alert policy
	CreateAlertPolicy(ctx context.Context, tenantID uuid.UUID, req *models.CreateAlertPolicyRequest) (*models.AlertPolicy, error)

	// GetAlertPolicy retrieves an alert policy by ID
	GetAlertPolicy(ctx context.Context, tenantID, policyID uuid.UUID) (*models.AlertPolicy, error)

	// ListAlertPolicies lists alert policies with pagination
	ListAlertPolicies(ctx context.Context, tenantID uuid.UUID, page, pageSize int) (*models.AlertPolicyListResponse, error)

	// UpdateAlertPolicy updates an alert policy (partial update)
	UpdateAlertPolicy(ctx context.Context, tenantID, policyID uuid.UUID, req *models.UpdateAlertPolicyRequest) (*models.AlertPolicy, error)

	// DeleteAlertPolicy deletes an alert policy
	DeleteAlertPolicy(ctx context.Context, tenantID, policyID uuid.UUID) error
}

// Ensure Service implements AlertPolicyService
var _ AlertPolicyService = (*Service)(nil)
