package oidcauth

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"

	"github.com/yassinebenameur/probara/api/internal/models"
	"github.com/yassinebenameur/probara/shared/auth"
)

// GroupMapping is one oidc_group_mappings row. A nil TenantID marks a
// platform-level mapping, whose only valid role is superadmin.
type GroupMapping struct {
	ID        uuid.UUID
	GroupName string
	TenantID  *uuid.UUID
	Role      string
}

// ResolveResult is the outcome of mapping validated claims to an admin user.
type ResolveResult struct {
	User       *models.AdminUser
	JITCreated bool
	// Sync is nil when no mapping rows exist (feature off) or the user was
	// JIT-created (mapped roles were applied at creation instead).
	Sync *RoleSyncResult
}

// RoleSyncResult describes what a login-time group sync did.
type RoleSyncResult struct {
	Applied            bool
	OldPlatformRole    string
	NewPlatformRole    string
	AddedMemberships   []string // "tenantID:role"
	RemovedMemberships []string // "tenantID:role"
	ChangedMemberships []string // "tenantID:old→new"
	SkippedDemotion    bool
	MatchedGroups      []string
	// ReceivedGroups is everything the token presented (matched or not) —
	// the audit trail for diagnosing name mismatches.
	ReceivedGroups []string
}

// parseGroupsClaim decodes a groups claim: a JSON array of strings
// (standard), tolerating a bare string (some IdPs). Anything else — including
// an absent claim — yields nil, never an error: installs without mappings
// must keep logging in regardless of what the IdP sends.
func parseGroupsClaim(raw json.RawMessage) []string {
	if len(raw) == 0 {
		return nil
	}

	var list []string
	if err := json.Unmarshal(raw, &list); err != nil {
		var single string
		if err := json.Unmarshal(raw, &single); err != nil {
			return nil
		}
		list = []string{single}
	}

	seen := make(map[string]bool, len(list))
	groups := make([]string, 0, len(list))
	for _, g := range list {
		g = strings.TrimSpace(g)
		if g == "" || seen[g] {
			continue
		}
		seen[g] = true
		groups = append(groups, g)
	}
	if len(groups) == 0 {
		return nil
	}
	return groups
}

func tenantRoleRank(role string) int {
	switch role {
	case auth.RoleAdmin:
		return 3
	case auth.RoleEditor:
		return 2
	case auth.RoleViewer:
		return 1
	}
	return 0
}

// resolveMappedRoles reduces the user's IdP groups against the mapping rows:
// any matched platform mapping grants superadmin, and per tenant the highest
// matched role wins (admin > editor > viewer). Group names match
// case-sensitively — they are opaque IdP identifiers.
func resolveMappedRoles(groups []string, mappings []GroupMapping) (string, map[uuid.UUID]string) {
	platformRole := auth.PlatformRoleMember
	memberships := map[uuid.UUID]string{}

	groupSet := make(map[string]bool, len(groups))
	for _, g := range groups {
		groupSet[g] = true
	}

	for _, m := range mappings {
		if !groupSet[m.GroupName] {
			continue
		}
		if m.TenantID == nil {
			platformRole = auth.PlatformRoleSuperadmin
			continue
		}
		if tenantRoleRank(m.Role) > tenantRoleRank(memberships[*m.TenantID]) {
			memberships[*m.TenantID] = m.Role
		}
	}
	return platformRole, memberships
}

func matchedGroups(groups []string, mappings []GroupMapping) []string {
	mapped := make(map[string]bool, len(mappings))
	for _, m := range mappings {
		mapped[m.GroupName] = true
	}
	var matched []string
	for _, g := range groups {
		if mapped[g] {
			matched = append(matched, g)
		}
	}
	sort.Strings(matched)
	return matched
}

func sortedTenantIDs(memberships map[uuid.UUID]string) []uuid.UUID {
	ids := make([]uuid.UUID, 0, len(memberships))
	for id := range memberships {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i].String() < ids[j].String() })
	return ids
}

// recordSeenGroups upserts the groups a verified ID token presented into
// oidc_seen_groups. Best-effort: a failure here must never break a login.
func (s *Service) recordSeenGroups(ctx context.Context, groups []string) {
	kept := make([]string, 0, len(groups))
	for _, g := range groups {
		if len(g) <= 255 {
			kept = append(kept, g)
		}
	}
	if len(kept) == 0 {
		return
	}
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO oidc_seen_groups (group_name)
		SELECT DISTINCT g FROM unnest($1::text[]) AS g
		ON CONFLICT (group_name) DO UPDATE SET last_seen_at = NOW()
	`, pq.Array(kept)); err != nil {
		s.log.WithError(err).Warn("Failed to record seen OIDC groups")
	}
}

func (s *Service) loadGroupMappings(ctx context.Context) ([]GroupMapping, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, group_name, tenant_id, role FROM oidc_group_mappings
	`)
	if err != nil {
		return nil, fmt.Errorf("failed to load OIDC group mappings: %w", err)
	}
	defer rows.Close()

	var mappings []GroupMapping
	for rows.Next() {
		var m GroupMapping
		if err := rows.Scan(&m.ID, &m.GroupName, &m.TenantID, &m.Role); err != nil {
			return nil, fmt.Errorf("failed to scan OIDC group mapping: %w", err)
		}
		mappings = append(mappings, m)
	}
	return mappings, rows.Err()
}

// syncMappedRoles re-derives the user's platform role and tenant memberships
// from their IdP groups. No mapping rows → no-op (nil result): deleting all
// mappings is the runtime kill switch that restores pre-mapping behavior.
//
// The mapped set fully replaces the current one — a user in no mapped groups
// ends up a member with zero memberships. The one exception is the
// last-superadmin guard: rather than leaving the install unadministrable (or
// blocking the login), the demotion is skipped and flagged for the audit log.
// On success user.PlatformRole is updated in place.
func (s *Service) syncMappedRoles(ctx context.Context, user *models.AdminUser, groups []string) (*RoleSyncResult, error) {
	mappings, err := s.loadGroupMappings(ctx)
	if err != nil {
		return nil, err
	}
	if len(mappings) == 0 {
		return nil, nil
	}

	if len(groups) == 0 {
		s.log.WithFields(map[string]interface{}{
			"username": user.Username,
		}).Warn("OIDC group mappings exist but the ID token carried no groups claim; check OIDC_SCOPES includes \"groups\" and OIDC_GROUPS_CLAIM matches the IdP")
	}

	wantPlatform, wantMemberships := resolveMappedRoles(groups, mappings)
	result := &RoleSyncResult{
		OldPlatformRole: user.PlatformRole,
		MatchedGroups:   matchedGroups(groups, mappings),
		ReceivedGroups:  groups,
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to begin group sync transaction: %w", err)
	}
	defer tx.Rollback()

	// Lock the user row so concurrent logins of the same user serialize.
	var curPlatform string
	if err := tx.QueryRowContext(ctx, `
		SELECT platform_role FROM admin_users WHERE id = $1 FOR UPDATE
	`, user.ID).Scan(&curPlatform); err != nil {
		return nil, fmt.Errorf("failed to lock user for group sync: %w", err)
	}
	result.OldPlatformRole = curPlatform

	curMemberships := map[uuid.UUID]string{}
	rows, err := tx.QueryContext(ctx, `
		SELECT tenant_id, role FROM tenant_memberships WHERE admin_user_id = $1
	`, user.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to load memberships for group sync: %w", err)
	}
	for rows.Next() {
		var tenantID uuid.UUID
		var role string
		if err := rows.Scan(&tenantID, &role); err != nil {
			rows.Close()
			return nil, fmt.Errorf("failed to scan membership for group sync: %w", err)
		}
		curMemberships[tenantID] = role
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to read memberships for group sync: %w", err)
	}

	if wantPlatform == auth.PlatformRoleMember && curPlatform == auth.PlatformRoleSuperadmin {
		var otherSuperadmins int
		if err := tx.QueryRowContext(ctx, `
			SELECT COUNT(*) FROM admin_users
			WHERE platform_role = $1 AND disabled_at IS NULL AND id <> $2
		`, auth.PlatformRoleSuperadmin, user.ID).Scan(&otherSuperadmins); err != nil {
			return nil, fmt.Errorf("failed to count superadmins for group sync: %w", err)
		}
		if otherSuperadmins == 0 {
			wantPlatform = auth.PlatformRoleSuperadmin
			result.SkippedDemotion = true
			s.log.WithFields(map[string]interface{}{
				"username": user.Username,
			}).Warn("OIDC group sync would demote the last active superadmin; keeping the role")
		}
	}
	result.NewPlatformRole = wantPlatform

	membershipsChanged := !membershipSetsEqual(curMemberships, wantMemberships)

	if wantPlatform != curPlatform {
		if _, err := tx.ExecContext(ctx, `
			UPDATE admin_users SET platform_role = $2, updated_at = NOW() WHERE id = $1
		`, user.ID, wantPlatform); err != nil {
			return nil, fmt.Errorf("failed to update platform role: %w", err)
		}
	}

	if membershipsChanged {
		if _, err := tx.ExecContext(ctx, `
			DELETE FROM tenant_memberships WHERE admin_user_id = $1
		`, user.ID); err != nil {
			return nil, fmt.Errorf("failed to clear memberships for group sync: %w", err)
		}
		now := time.Now()
		for _, tenantID := range sortedTenantIDs(wantMemberships) {
			// EXISTS guard mirrors provisionUser: a tenant deleted between
			// mapping load and insert must not fail the login.
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO tenant_memberships (id, admin_user_id, tenant_id, role, created_at, updated_at)
				SELECT $1, $2, $3, $4, $5, $5
				WHERE EXISTS (SELECT 1 FROM tenants WHERE id = $3)
			`, uuid.New(), user.ID, tenantID, wantMemberships[tenantID], now); err != nil {
				return nil, fmt.Errorf("failed to insert mapped membership: %w", err)
			}
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("failed to commit group sync: %w", err)
	}

	result.Applied = wantPlatform != curPlatform || membershipsChanged
	diffMemberships(result, curMemberships, wantMemberships)
	user.PlatformRole = wantPlatform

	if result.Applied {
		s.log.WithFields(map[string]interface{}{
			"username":      user.Username,
			"platform_role": wantPlatform,
			"added":         result.AddedMemberships,
			"removed":       result.RemovedMemberships,
			"changed":       result.ChangedMemberships,
		}).Info("Synced roles from OIDC group mappings")
	}
	return result, nil
}

func membershipSetsEqual(a, b map[uuid.UUID]string) bool {
	if len(a) != len(b) {
		return false
	}
	for tenantID, role := range a {
		if b[tenantID] != role {
			return false
		}
	}
	return true
}

func diffMemberships(result *RoleSyncResult, current, wanted map[uuid.UUID]string) {
	for _, tenantID := range sortedTenantIDs(wanted) {
		role := wanted[tenantID]
		cur, ok := current[tenantID]
		switch {
		case !ok:
			result.AddedMemberships = append(result.AddedMemberships, tenantID.String()+":"+role)
		case cur != role:
			result.ChangedMemberships = append(result.ChangedMemberships, tenantID.String()+":"+cur+"→"+role)
		}
	}
	for _, tenantID := range sortedTenantIDs(current) {
		if _, ok := wanted[tenantID]; !ok {
			result.RemovedMemberships = append(result.RemovedMemberships, tenantID.String()+":"+current[tenantID])
		}
	}
}
