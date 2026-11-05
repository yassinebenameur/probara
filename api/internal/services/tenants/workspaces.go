package tenants

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/lib/pq"

	"github.com/yassinebenameur/probara/api/internal/models"
	"github.com/yassinebenameur/probara/shared/auth"
	"github.com/yassinebenameur/probara/shared/tenancy"
)

// Workspace management errors. Handlers map them to HTTP statuses.
var (
	ErrTenantNotFound  = errors.New("workspace not found")
	ErrTenantNameTaken = errors.New("a workspace with this name already exists")
	ErrInvalidName     = errors.New("workspace name must be 1-100 characters with no control characters")
	ErrConfirmMismatch = errors.New("confirm_name does not match the workspace name")
	ErrProtectedTenant = errors.New("this workspace is the default workspace and cannot be deleted")
	ErrLastLiveTenant  = errors.New("the last workspace cannot be deleted")
)

const (
	maxTenantNameLength = 100
	// tenantNameUnique is migration 000105's partial unique index on live
	// workspace names.
	tenantNameUnique = "tenants_name_live_key"
)

// tenantDeleteLock serializes workspace deletes (a transaction-scoped
// advisory lock, released at commit) so two concurrent deletes cannot both
// pass the last-live-workspace check.
const tenantDeleteLock = int64(901_337_451)

// WithProtectedTenants adds workspaces that can never be deleted on top of
// tenancy.DefaultTenantID: the OIDC JIT default tenant, whose deletion would
// leave new SSO users without a workspace. Invalid IDs are ignored.
func (s *Service) WithProtectedTenants(ids ...string) *Service {
	if s.protected == nil {
		s.protected = map[uuid.UUID]struct{}{}
	}
	for _, raw := range append([]string{tenancy.DefaultTenantID}, ids...) {
		if id, err := uuid.Parse(strings.TrimSpace(raw)); err == nil {
			s.protected[id] = struct{}{}
		}
	}
	return s
}

func (s *Service) isProtected(id uuid.UUID) bool {
	if id.String() == tenancy.DefaultTenantID {
		return true
	}
	_, ok := s.protected[id]
	return ok
}

// NormalizeTenantName trims a workspace name and validates it.
func NormalizeTenantName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || utf8.RuneCountInString(name) > maxTenantNameLength {
		return "", ErrInvalidName
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return "", ErrInvalidName
		}
	}
	return name, nil
}

// CreateTenant creates a workspace. Only superadmins reach it (route gate),
// and superadmins act in every workspace, so no membership is seeded.
func (s *Service) CreateTenant(ctx context.Context, name string) (*models.Tenant, error) {
	name, err := NormalizeTenantName(name)
	if err != nil {
		return nil, err
	}
	var tenant models.Tenant
	err = s.db.QueryRowContext(ctx, `
		INSERT INTO tenants (id, name, created_at, updated_at)
		VALUES ($1, $2, NOW(), NOW())
		RETURNING id, name, data_retention_days, dashboard_group_tags, created_at, updated_at
	`, uuid.New(), name).Scan(
		&tenant.ID, &tenant.Name, &tenant.DataRetentionDays,
		pq.Array(&tenant.DashboardGroupTags), &tenant.CreatedAt, &tenant.UpdatedAt,
	)
	if err != nil {
		if isNameConflict(err) {
			return nil, ErrTenantNameTaken
		}
		return nil, fmt.Errorf("failed to create workspace: %w", err)
	}
	finishTenant(&tenant)
	return &tenant, nil
}

// RenameTenant renames a live workspace and returns it with its previous name.
func (s *Service) RenameTenant(ctx context.Context, id uuid.UUID, name string) (*models.Tenant, string, error) {
	name, err := NormalizeTenantName(name)
	if err != nil {
		return nil, "", err
	}
	var tenant models.Tenant
	var previous string
	err = s.db.QueryRowContext(ctx, `
		WITH previous AS (
			SELECT id, name FROM tenants
			WHERE id = $1 AND deleted_at IS NULL
			FOR UPDATE
		)
		UPDATE tenants t
		SET name = $2, updated_at = NOW()
		FROM previous
		WHERE t.id = previous.id
		RETURNING previous.name, t.id, t.name, t.data_retention_days, t.dashboard_group_tags, t.created_at, t.updated_at
	`, id, name).Scan(
		&previous, &tenant.ID, &tenant.Name, &tenant.DataRetentionDays,
		pq.Array(&tenant.DashboardGroupTags), &tenant.CreatedAt, &tenant.UpdatedAt,
	)
	if err != nil {
		switch {
		case errors.Is(err, sql.ErrNoRows):
			return nil, "", ErrTenantNotFound
		case isNameConflict(err):
			return nil, "", ErrTenantNameTaken
		}
		return nil, "", fmt.Errorf("failed to rename workspace: %w", err)
	}
	finishTenant(&tenant)
	return &tenant, previous, nil
}

// DeleteTenant soft-deletes a workspace once confirmName matches its name.
//
// One transaction stamps tenants.deleted_at, tombstones every monitor and
// location, and cuts off every credential scoped to the workspace (API keys,
// MCP grants, memberships, SSO group mappings, discovery collector tokens).
// It never deletes the tenant row: that cascade would remove the workspace's
// whole check history in one statement. The scheduler purger drains the data
// in bounded batches and removes the row afterwards (purger.go, tenant phase).
func (s *Service) DeleteTenant(ctx context.Context, id uuid.UUID, confirmName string, deletedBy *uuid.UUID) (*models.DeleteTenantResponse, error) {
	if s.isProtected(id) {
		return nil, ErrProtectedTenant
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin workspace delete: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, tenantDeleteLock); err != nil {
		return nil, fmt.Errorf("lock workspace delete: %w", err)
	}

	var name string
	if err := tx.QueryRowContext(ctx, `
		SELECT name FROM tenants WHERE id = $1 AND deleted_at IS NULL FOR UPDATE
	`, id).Scan(&name); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrTenantNotFound
		}
		return nil, fmt.Errorf("load workspace: %w", err)
	}
	if strings.TrimSpace(confirmName) != name {
		return nil, ErrConfirmMismatch
	}

	var others int
	if err := tx.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM tenants WHERE deleted_at IS NULL AND id <> $1
	`, id).Scan(&others); err != nil {
		return nil, fmt.Errorf("count workspaces: %w", err)
	}
	if others == 0 {
		return nil, ErrLastLiveTenant
	}

	if _, err := tx.ExecContext(ctx, `
		UPDATE tenants SET deleted_at = NOW(), deleted_by = $2, updated_at = NOW() WHERE id = $1
	`, id, deletedBy); err != nil {
		return nil, fmt.Errorf("mark workspace deleted: %w", err)
	}

	result, err := tx.ExecContext(ctx, `
		UPDATE monitors SET deleted_at = NOW(), updated_at = NOW()
		WHERE tenant_id = $1 AND deleted_at IS NULL
	`, id)
	if err != nil {
		return nil, fmt.Errorf("tombstone workspace monitors: %w", err)
	}
	monitors, _ := result.RowsAffected()

	// Cut-offs. Each statement is scoped to this workspace only.
	cutoffs := []struct{ name, sql string }{
		// Stops mesh probes and makes result ingest refuse the workspace's
		// private-location workers.
		{"locations", `UPDATE locations SET deleted_at = NOW(), updated_at = NOW() WHERE tenant_id = $1 AND deleted_at IS NULL`},
		{"api keys", `UPDATE api_keys SET revoked_at = NOW() WHERE tenant_id = $1 AND revoked_at IS NULL`},
		{"mcp grants", `UPDATE mcp_oauth_grants SET revoked_at = NOW() WHERE tenant_id = $1 AND revoked_at IS NULL`},
		// Members lose the workspace from their switcher at once; the SSO
		// mappings go too, so a login's group sync cannot re-grant it.
		{"memberships", `DELETE FROM tenant_memberships WHERE tenant_id = $1`},
		{"sso group mappings", `DELETE FROM oidc_group_mappings WHERE tenant_id = $1`},
		// Collector tokens are compared as hex digests, which never equal
		// this marker, so the workspace's Kubernetes collectors are refused.
		{"discovery collector tokens", `UPDATE discovery_integrations SET token_hash = 'revoked' WHERE tenant_id = $1`},
	}
	for _, c := range cutoffs {
		if _, err := tx.ExecContext(ctx, c.sql, id); err != nil {
			return nil, fmt.Errorf("revoke workspace %s: %w", c.name, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit workspace delete: %w", err)
	}
	return &models.DeleteTenantResponse{ID: id, Name: name, Monitors: monitors}, nil
}

func finishTenant(t *models.Tenant) {
	if t.DashboardGroupTags == nil {
		t.DashboardGroupTags = []string{}
	}
	// Only superadmins manage workspaces, and they act as admin everywhere.
	t.Role = auth.RoleAdmin
}

func isNameConflict(err error) bool {
	var pqErr *pq.Error
	return errors.As(err, &pqErr) && pqErr.Code == "23505" &&
		(pqErr.Constraint == tenantNameUnique || pqErr.Constraint == "tenants_name_key")
}
