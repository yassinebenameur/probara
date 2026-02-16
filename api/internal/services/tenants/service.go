package tenants

import (
	"context"
	"fmt"

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
		SELECT id, name, created_at, updated_at
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
		if err := rows.Scan(&tenant.ID, &tenant.Name, &tenant.CreatedAt, &tenant.UpdatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan tenant: %w", err)
		}
		tenants = append(tenants, tenant)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate tenants: %w", err)
	}

	return tenants, nil
}
