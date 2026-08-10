// Package oidcmappings manages the oidc_group_mappings rows that map IdP
// groups to platform/tenant roles. The login-time consumer of these rows
// lives in services/oidcauth (groupsync.go).
package oidcmappings

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/lib/pq"

	"github.com/yassinebenameur/probara/api/internal/models"
	"github.com/yassinebenameur/probara/shared/auth"
	"github.com/yassinebenameur/probara/shared/db"
)

var (
	// ErrNotFound is returned when a mapping does not exist.
	ErrNotFound = errors.New("OIDC group mapping not found")
	// ErrDuplicate is returned when an equivalent mapping already exists.
	ErrDuplicate = errors.New("an equivalent OIDC group mapping already exists")
	// ErrTenantNotFound is returned when the referenced tenant does not exist.
	ErrTenantNotFound = errors.New("tenant not found")
	// ErrInvalidMapping is returned for validation failures.
	ErrInvalidMapping = errors.New("invalid OIDC group mapping")
)

const (
	maxGroupNameLength = 255
	maxLabelLength     = 100
)

// normalizeLabel trims and bounds a label; empty means "no label" (NULL).
func normalizeLabel(label string) (*string, error) {
	label = strings.TrimSpace(label)
	if label == "" {
		return nil, nil
	}
	if len(label) > maxLabelLength {
		return nil, fmt.Errorf("%w: label must be at most %d characters", ErrInvalidMapping, maxLabelLength)
	}
	return &label, nil
}

// Service manages OIDC group mappings.
type Service struct {
	db *db.Client
}

// NewService creates a new OIDC group mappings service.
func NewService(database *db.Client) *Service {
	return &Service{db: database}
}

const mappingColumns = `
	m.id, m.group_name, m.label, m.tenant_id, t.name, m.role, m.created_at, m.updated_at
`

func scanMapping(row interface{ Scan(...interface{}) error }) (*models.OIDCGroupMapping, error) {
	var m models.OIDCGroupMapping
	if err := row.Scan(&m.ID, &m.GroupName, &m.Label, &m.TenantID, &m.TenantName, &m.Role, &m.CreatedAt, &m.UpdatedAt); err != nil {
		return nil, err
	}
	return &m, nil
}

// List returns all mappings, platform rows first within each group.
func (s *Service) List(ctx context.Context) ([]models.OIDCGroupMapping, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+mappingColumns+`
		FROM oidc_group_mappings m
		LEFT JOIN tenants t ON t.id = m.tenant_id
		ORDER BY m.group_name, m.tenant_id NULLS FIRST
	`)
	if err != nil {
		return nil, fmt.Errorf("failed to list OIDC group mappings: %w", err)
	}
	defer rows.Close()

	mappings := []models.OIDCGroupMapping{}
	for rows.Next() {
		m, err := scanMapping(rows)
		if err != nil {
			return nil, fmt.Errorf("failed to scan OIDC group mapping: %w", err)
		}
		mappings = append(mappings, *m)
	}
	return mappings, rows.Err()
}

// SeenGroups returns groups observed in ID tokens at past SSO logins, most
// recently seen first, capped to keep the settings payload bounded.
func (s *Service) SeenGroups(ctx context.Context) ([]models.OIDCSeenGroup, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT group_name, first_seen_at, last_seen_at
		FROM oidc_seen_groups
		ORDER BY last_seen_at DESC, group_name
		LIMIT 500
	`)
	if err != nil {
		return nil, fmt.Errorf("failed to list seen OIDC groups: %w", err)
	}
	defer rows.Close()

	groups := []models.OIDCSeenGroup{}
	for rows.Next() {
		var g models.OIDCSeenGroup
		if err := rows.Scan(&g.GroupName, &g.FirstSeen, &g.LastSeen); err != nil {
			return nil, fmt.Errorf("failed to scan seen OIDC group: %w", err)
		}
		groups = append(groups, g)
	}
	return groups, rows.Err()
}

// Create validates and inserts a mapping.
func (s *Service) Create(ctx context.Context, req *models.CreateOIDCGroupMappingRequest) (*models.OIDCGroupMapping, error) {
	groupName := strings.TrimSpace(req.GroupName)
	if groupName == "" {
		return nil, fmt.Errorf("%w: group_name is required", ErrInvalidMapping)
	}
	if len(groupName) > maxGroupNameLength {
		return nil, fmt.Errorf("%w: group_name must be at most %d characters", ErrInvalidMapping, maxGroupNameLength)
	}

	var tenantID *uuid.UUID
	if req.TenantID != nil && strings.TrimSpace(*req.TenantID) != "" {
		parsed, err := uuid.Parse(strings.TrimSpace(*req.TenantID))
		if err != nil {
			return nil, fmt.Errorf("%w: invalid tenant_id", ErrInvalidMapping)
		}
		tenantID = &parsed
	}

	if tenantID == nil {
		if req.Role != auth.PlatformRoleSuperadmin {
			return nil, fmt.Errorf("%w: platform mappings only grant the %q role", ErrInvalidMapping, auth.PlatformRoleSuperadmin)
		}
	} else if !auth.ValidTenantRole(req.Role) {
		return nil, fmt.Errorf("%w: role must be one of admin, editor, viewer", ErrInvalidMapping)
	}

	label, err := normalizeLabel(req.Label)
	if err != nil {
		return nil, err
	}

	row := s.db.QueryRowContext(ctx, `
		WITH inserted AS (
			INSERT INTO oidc_group_mappings (group_name, label, tenant_id, role)
			VALUES ($1, $2, $3, $4)
			RETURNING id, group_name, label, tenant_id, role, created_at, updated_at
		)
		SELECT m.id, m.group_name, m.label, m.tenant_id, t.name, m.role, m.created_at, m.updated_at
		FROM inserted m
		LEFT JOIN tenants t ON t.id = m.tenant_id
	`, groupName, label, tenantID, req.Role)
	mapping, err := scanMapping(row)
	if err != nil {
		if isUniqueViolation(err) {
			return nil, ErrDuplicate
		}
		if isForeignKeyViolation(err) {
			return nil, ErrTenantNotFound
		}
		return nil, fmt.Errorf("failed to create OIDC group mapping: %w", err)
	}
	return mapping, nil
}

// Update changes a mapping's role and/or label; omitted fields keep their
// current value, an empty label clears it.
func (s *Service) Update(ctx context.Context, id uuid.UUID, req *models.UpdateOIDCGroupMappingRequest) (*models.OIDCGroupMapping, error) {
	current, err := s.get(ctx, id)
	if err != nil {
		return nil, err
	}

	role := current.Role
	if req.Role != nil {
		role = *req.Role
		if current.TenantID == nil {
			if role != auth.PlatformRoleSuperadmin {
				return nil, fmt.Errorf("%w: platform mappings only grant the %q role", ErrInvalidMapping, auth.PlatformRoleSuperadmin)
			}
		} else if !auth.ValidTenantRole(role) {
			return nil, fmt.Errorf("%w: role must be one of admin, editor, viewer", ErrInvalidMapping)
		}
	}

	label := current.Label
	if req.Label != nil {
		label, err = normalizeLabel(*req.Label)
		if err != nil {
			return nil, err
		}
	}

	row := s.db.QueryRowContext(ctx, `
		WITH updated AS (
			UPDATE oidc_group_mappings
			SET role = $2, label = $3, updated_at = NOW()
			WHERE id = $1
			RETURNING id, group_name, label, tenant_id, role, created_at, updated_at
		)
		SELECT m.id, m.group_name, m.label, m.tenant_id, t.name, m.role, m.created_at, m.updated_at
		FROM updated m
		LEFT JOIN tenants t ON t.id = m.tenant_id
	`, id, role, label)
	mapping, err := scanMapping(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("failed to update OIDC group mapping: %w", err)
	}
	return mapping, nil
}

// Delete removes a mapping.
func (s *Service) Delete(ctx context.Context, id uuid.UUID) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM oidc_group_mappings WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("failed to delete OIDC group mapping: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to delete OIDC group mapping: %w", err)
	}
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Service) get(ctx context.Context, id uuid.UUID) (*models.OIDCGroupMapping, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT `+mappingColumns+`
		FROM oidc_group_mappings m
		LEFT JOIN tenants t ON t.id = m.tenant_id
		WHERE m.id = $1
	`, id)
	mapping, err := scanMapping(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("failed to get OIDC group mapping: %w", err)
	}
	return mapping, nil
}

func isUniqueViolation(err error) bool {
	var pqErr *pq.Error
	return errors.As(err, &pqErr) && pqErr.Code == "23505"
}

func isForeignKeyViolation(err error) bool {
	var pqErr *pq.Error
	return errors.As(err, &pqErr) && pqErr.Code == "23503"
}
