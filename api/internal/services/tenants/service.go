package tenants

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/google/uuid"
	"github.com/lib/pq"

	"github.com/yassinebenameur/probara/api/internal/models"
	"github.com/yassinebenameur/probara/shared/db"
)

// Service handles tenant operations.
type Service struct {
	db *db.Client
}

// NewService creates a new tenant service.
func NewService(dbClient *db.Client) *Service {
	return &Service{db: dbClient}
}

// ListTenants returns all tenants.
func (s *Service) ListTenants(ctx context.Context) ([]models.Tenant, error) {
	query := `
		SELECT id, name, data_retention_days, dashboard_group_tags, created_at, updated_at
		FROM tenants
		ORDER BY name ASC
	`

	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to list tenants: %w", err)
	}
	defer rows.Close()

	var tenants []models.Tenant
	for rows.Next() {
		var tenant models.Tenant
		if err := rows.Scan(
			&tenant.ID,
			&tenant.Name,
			&tenant.DataRetentionDays,
			pq.Array(&tenant.DashboardGroupTags),
			&tenant.CreatedAt,
			&tenant.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("failed to scan tenant: %w", err)
		}
		tenants = append(tenants, tenant)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate tenants: %w", err)
	}

	return tenants, nil
}

// GetTenantSettings returns tenant settings by tenant ID.
func (s *Service) GetTenantSettings(ctx context.Context, tenantID uuid.UUID) (*models.TenantSettings, error) {
	query := `
		SELECT data_retention_days, dashboard_group_tags
		FROM tenants
		WHERE id = $1
	`

	var settings models.TenantSettings
	if err := s.db.QueryRowContext(ctx, query, tenantID).Scan(
		&settings.DataRetentionDays,
		pq.Array(&settings.DashboardGroupTags),
	); err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("tenant not found")
		}
		return nil, fmt.Errorf("failed to get tenant settings: %w", err)
	}

	if settings.DashboardGroupTags == nil {
		settings.DashboardGroupTags = []string{}
	}

	return &settings, nil
}

// UpdateTenantSettings updates tenant settings and returns the latest value.
func (s *Service) UpdateTenantSettings(ctx context.Context, tenantID uuid.UUID, req *models.UpdateTenantSettingsRequest) (*models.TenantSettings, error) {
	if req == nil || (req.DataRetentionDays == nil && req.DashboardGroupTags == nil) {
		return s.GetTenantSettings(ctx, tenantID)
	}

	// Read current settings so we can write only the provided fields back.
	current, err := s.GetTenantSettings(ctx, tenantID)
	if err != nil {
		return nil, err
	}

	nextRetention := current.DataRetentionDays
	if req.DataRetentionDays != nil {
		nextRetention = *req.DataRetentionDays
	}

	nextTags := current.DashboardGroupTags
	if req.DashboardGroupTags != nil {
		nextTags = *req.DashboardGroupTags
		if nextTags == nil {
			nextTags = []string{}
		}
	}

	query := `
		UPDATE tenants
		SET data_retention_days = $1,
		    dashboard_group_tags = $2,
		    updated_at = NOW()
		WHERE id = $3
		RETURNING data_retention_days, dashboard_group_tags
	`

	var settings models.TenantSettings
	if err := s.db.QueryRowContext(ctx, query, nextRetention, pq.Array(nextTags), tenantID).Scan(
		&settings.DataRetentionDays,
		pq.Array(&settings.DashboardGroupTags),
	); err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("tenant not found")
		}
		return nil, fmt.Errorf("failed to update tenant settings: %w", err)
	}

	if settings.DashboardGroupTags == nil {
		settings.DashboardGroupTags = []string{}
	}
	return &settings, nil
}
