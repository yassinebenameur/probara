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
	"github.com/yassinebenameur/probara/shared/db/pgerr"
	"github.com/yassinebenameur/probara/shared/tenancy"
)

// Workspace management errors. Their messages reach API clients.
var (
	ErrWorkspaceNotFound  = errors.New("workspace not found")
	ErrWorkspaceNameTaken = errors.New("a workspace with this name already exists")
	ErrInvalidName        = fmt.Errorf("workspace name must be 1-%d characters, with no control characters", MaxWorkspaceNameLength)
	ErrConfirmMismatch    = errors.New("confirm_name does not match the workspace name")
	ErrProtectedWorkspace = errors.New("this workspace cannot be deleted: it is the default workspace, or the one new single sign-on users join")
	ErrLastWorkspace      = errors.New("the last workspace cannot be deleted")
)

// MaxWorkspaceNameLength bounds a workspace name, in characters.
const MaxWorkspaceNameLength = 100

// tenantNameLiveKey is migration 000115's unique index on live workspace
// names; tenants_name_key is the full-table constraint it replaced.
const (
	tenantNameLiveKey = "tenants_name_live_key"
	tenantNameLegacy  = "tenants_name_key"
)

// workspaceDeleteLock serializes workspace deletes (a transaction-scoped
// advisory lock, released at commit), so two concurrent deletes cannot both
// pass the last-workspace check.
const workspaceDeleteLock = int64(901_337_421)

// ProtectWorkspaces adds workspaces that can never be deleted on top of
// tenancy.DefaultTenantID: the OIDC just-in-time provisioning tenant, whose
// deletion would leave every new SSO user without a workspace. Invalid IDs
// are ignored.
func (s *Service) ProtectWorkspaces(ids ...string) *Service {
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

// IsProtected reports whether a workspace can never be deleted.
func (s *Service) IsProtected(id uuid.UUID) bool {
	if id.String() == tenancy.DefaultTenantID {
		return true
	}
	_, ok := s.protected[id]
	return ok
}

// NormalizeWorkspaceName trims a workspace name and validates it.
func NormalizeWorkspaceName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || utf8.RuneCountInString(name) > MaxWorkspaceNameLength || !utf8.ValidString(name) {
		return "", ErrInvalidName
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return "", ErrInvalidName
		}
	}
	return name, nil
}

// CreateWorkspace creates a workspace. Only superadmins reach it (route
// gate), and they act in every workspace, so no membership is added. A new
// workspace starts from the column defaults: no alert channels, the shared
// worker fleet, default notification settings and AI on (no ai_settings row).
func (s *Service) CreateWorkspace(ctx context.Context, name string) (*models.Tenant, error) {
	name, err := NormalizeWorkspaceName(name)
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
			return nil, ErrWorkspaceNameTaken
		}
		return nil, fmt.Errorf("failed to create workspace: %w", err)
	}
	finishWorkspace(&tenant)
	return &tenant, nil
}

// RenameWorkspace renames a live workspace and returns it with its previous
// name.
func (s *Service) RenameWorkspace(ctx context.Context, id uuid.UUID, name string) (*models.Tenant, string, error) {
	name, err := NormalizeWorkspaceName(name)
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
			return nil, "", ErrWorkspaceNotFound
		case isNameConflict(err):
			return nil, "", ErrWorkspaceNameTaken
		}
		return nil, "", fmt.Errorf("failed to rename workspace: %w", err)
	}
	finishWorkspace(&tenant)
	return &tenant, previous, nil
}

// DeleteWorkspace soft-deletes a workspace once confirmName repeats its name.
//
// One transaction stamps tenants.deleted_at, which every lookup refuses from
// the moment it commits (shared/tenancy), removes the workspace's memberships
// and SSO group mappings (so no switcher lists it and no sign-in re-grants
// it) and revokes its MCP connections (so their refresh tokens fail too) and
// its API keys. It
// never deletes the tenant row: the scheduler's workspace purge removes the
// data in bounded batches and the row last. There is no undo.
func (s *Service) DeleteWorkspace(ctx context.Context, id uuid.UUID, confirmName string, deletedBy *uuid.UUID) (*models.DeleteWorkspaceResponse, error) {
	if s.IsProtected(id) {
		return nil, ErrProtectedWorkspace
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin workspace delete: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, workspaceDeleteLock); err != nil {
		return nil, fmt.Errorf("lock workspace delete: %w", err)
	}

	var name string
	if err := tx.QueryRowContext(ctx, `
		SELECT name FROM tenants WHERE id = $1 AND deleted_at IS NULL FOR UPDATE
	`, id).Scan(&name); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrWorkspaceNotFound
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
		return nil, ErrLastWorkspace
	}

	resp := models.DeleteWorkspaceResponse{ID: id, Name: name}
	if err := tx.QueryRowContext(ctx, `
		UPDATE tenants SET deleted_at = NOW(), deleted_by = $2, updated_at = NOW()
		WHERE id = $1
		RETURNING deleted_at
	`, id, deletedBy).Scan(&resp.DeletedAt); err != nil {
		return nil, fmt.Errorf("mark workspace deleted: %w", err)
	}

	for _, c := range []struct{ name, sql string }{
		{"memberships", `DELETE FROM tenant_memberships WHERE tenant_id = $1`},
		{"sso group mappings", `DELETE FROM oidc_group_mappings WHERE tenant_id = $1`},
		{"mcp connections", `UPDATE mcp_oauth_grants SET revoked_at = NOW() WHERE tenant_id = $1 AND revoked_at IS NULL`},
		// Already refused by every lookup (LiveTenantSQL); revoking them too
		// makes the refusal hold without the tenant join and shows in the
		// key's own row.
		{"api keys", `UPDATE api_keys SET revoked_at = NOW() WHERE tenant_id = $1 AND revoked_at IS NULL`},
	} {
		if _, err := tx.ExecContext(ctx, c.sql, id); err != nil {
			return nil, fmt.Errorf("remove workspace %s: %w", c.name, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit workspace delete: %w", err)
	}
	return &resp, nil
}

// ListWorkspaceOverview lists the live workspaces with their member and
// monitor counts, for the superadmin Workspaces page.
func (s *Service) ListWorkspaceOverview(ctx context.Context) ([]models.WorkspaceOverview, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT t.id, t.name, t.created_at, t.updated_at,
			(SELECT COUNT(*) FROM tenant_memberships m WHERE m.tenant_id = t.id),
			(SELECT COUNT(*) FROM monitors mo WHERE mo.tenant_id = t.id AND mo.deleted_at IS NULL)
		FROM tenants t
		WHERE t.deleted_at IS NULL
		ORDER BY t.name ASC
	`)
	if err != nil {
		return nil, fmt.Errorf("failed to list workspaces: %w", err)
	}
	defer rows.Close()
	items := []models.WorkspaceOverview{}
	for rows.Next() {
		var w models.WorkspaceOverview
		if err := rows.Scan(&w.ID, &w.Name, &w.CreatedAt, &w.UpdatedAt, &w.MemberCount, &w.MonitorCount); err != nil {
			return nil, fmt.Errorf("failed to scan workspace: %w", err)
		}
		w.Protected = s.IsProtected(w.ID)
		items = append(items, w)
	}
	return items, rows.Err()
}

func finishWorkspace(t *models.Tenant) {
	if t.DashboardGroupTags == nil {
		t.DashboardGroupTags = []string{}
	}
	// Only superadmins manage workspaces, and they act as admin everywhere.
	t.Role = auth.RoleAdmin
}

func isNameConflict(err error) bool {
	return pgerr.IsUniqueViolationOf(err, tenantNameLiveKey) || pgerr.IsUniqueViolationOf(err, tenantNameLegacy)
}
