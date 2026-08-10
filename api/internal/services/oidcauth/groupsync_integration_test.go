package oidcauth

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/shared/auth"
	"github.com/yassinebenameur/probara/shared/db"
	"github.com/yassinebenameur/probara/shared/testutil"
)

func insertMapping(ctx context.Context, t *testing.T, dbClient *db.Client, groupName string, tenantID *uuid.UUID, role string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if _, err := dbClient.ExecContext(ctx, `
		INSERT INTO oidc_group_mappings (id, group_name, tenant_id, role)
		VALUES ($1, $2, $3, $4)
	`, id, groupName, tenantID, role); err != nil {
		t.Fatalf("insert mapping: %v", err)
	}
	return id
}

func clearMappings(ctx context.Context, t *testing.T, dbClient *db.Client) {
	t.Helper()
	if _, err := dbClient.ExecContext(ctx, `DELETE FROM oidc_group_mappings`); err != nil {
		t.Fatalf("clear mappings: %v", err)
	}
}

func membershipRoles(ctx context.Context, t *testing.T, dbClient *db.Client, userID uuid.UUID) map[uuid.UUID]string {
	t.Helper()
	rows, err := dbClient.QueryContext(ctx, `
		SELECT tenant_id, role FROM tenant_memberships WHERE admin_user_id = $1
	`, userID)
	if err != nil {
		t.Fatalf("query memberships: %v", err)
	}
	defer rows.Close()
	out := map[uuid.UUID]string{}
	for rows.Next() {
		var tenantID uuid.UUID
		var role string
		if err := rows.Scan(&tenantID, &role); err != nil {
			t.Fatalf("scan membership: %v", err)
		}
		out[tenantID] = role
	}
	return out
}

func TestGroupSync(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test requires Docker")
	}

	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "sync-tenant")
	otherTenantID := testutil.InsertTenant(ctx, t, dbClient, "sync-tenant-2")
	svc := newTestService(dbClient, tenantID, true)

	// A standing superadmin so bootstrap parity and the last-superadmin guard
	// don't interfere with member-level scenarios.
	insertAdmin(ctx, t, dbClient, "sync-root", nil, auth.PlatformRoleSuperadmin)

	t.Run("existing SSO user gains and loses mapped roles across logins", func(t *testing.T) {
		clearMappings(ctx, t, dbClient)

		// First login with zero mappings: JIT default applies (regression).
		claims := &Claims{Issuer: testIssuer, Subject: "sub-sync", Groups: []string{"ops"}}
		resolved, err := svc.ResolveUser(ctx, claims)
		if err != nil {
			t.Fatalf("initial login: %v", err)
		}
		userID := resolved.User.ID
		if resolved.Sync != nil {
			t.Fatal("expected no sync result with zero mappings")
		}
		if got := membershipRoles(ctx, t, dbClient, userID); got[tenantID] != auth.RoleViewer {
			t.Fatalf("expected JIT default viewer membership, got %v", got)
		}

		// Mapping appears: next login replaces the membership set.
		insertMapping(ctx, t, dbClient, "ops", &otherTenantID, auth.RoleAdmin)
		resolved, err = svc.ResolveUser(ctx, claims)
		if err != nil {
			t.Fatalf("login after mapping added: %v", err)
		}
		if resolved.Sync == nil || !resolved.Sync.Applied {
			t.Fatalf("expected an applied sync, got %+v", resolved.Sync)
		}
		got := membershipRoles(ctx, t, dbClient, userID)
		if len(got) != 1 || got[otherTenantID] != auth.RoleAdmin {
			t.Fatalf("expected only mapped admin membership on other tenant, got %v", got)
		}

		// Dropped from the group: membership revoked, user ends up role-less.
		claims.Groups = nil
		resolved, err = svc.ResolveUser(ctx, claims)
		if err != nil {
			t.Fatalf("login after group removal: %v", err)
		}
		if resolved.Sync == nil || !resolved.Sync.Applied {
			t.Fatalf("expected an applied sync on revocation, got %+v", resolved.Sync)
		}
		if got := membershipRoles(ctx, t, dbClient, userID); len(got) != 0 {
			t.Fatalf("expected zero memberships after revocation, got %v", got)
		}

		// Same groups again: idempotent, nothing applied.
		resolved, err = svc.ResolveUser(ctx, claims)
		if err != nil {
			t.Fatalf("idempotent login: %v", err)
		}
		if resolved.Sync == nil || resolved.Sync.Applied {
			t.Fatalf("expected a no-op sync, got %+v", resolved.Sync)
		}
	})

	t.Run("platform mapping promotes and demotes", func(t *testing.T) {
		clearMappings(ctx, t, dbClient)
		insertMapping(ctx, t, dbClient, "root-squad", nil, auth.PlatformRoleSuperadmin)

		claims := &Claims{Issuer: testIssuer, Subject: "sub-platform", Groups: []string{"root-squad"}}
		resolved, err := svc.ResolveUser(ctx, claims)
		if err != nil {
			t.Fatalf("provision: %v", err)
		}
		if resolved.User.PlatformRole != auth.PlatformRoleSuperadmin {
			t.Fatalf("expected mapped superadmin at JIT, got %q", resolved.User.PlatformRole)
		}

		// Group removed: demoted to member (another superadmin exists).
		claims.Groups = nil
		resolved, err = svc.ResolveUser(ctx, claims)
		if err != nil {
			t.Fatalf("demotion login: %v", err)
		}
		if resolved.User.PlatformRole != auth.PlatformRoleMember {
			t.Fatalf("expected demotion to member, got %q", resolved.User.PlatformRole)
		}
		if resolved.Sync == nil || resolved.Sync.SkippedDemotion {
			t.Fatalf("demotion must not be skipped with another superadmin present, got %+v", resolved.Sync)
		}
	})

	t.Run("last superadmin is not demoted", func(t *testing.T) {
		clearMappings(ctx, t, dbClient)
		insertMapping(ctx, t, dbClient, "anything", &tenantID, auth.RoleViewer)

		// Make the SSO user the sole active superadmin.
		var soloID uuid.UUID
		if err := dbClient.QueryRowContext(ctx, `
			SELECT id FROM admin_users WHERE username = 'sync-solo-check'
		`).Scan(&soloID); err != nil {
			claims := &Claims{Issuer: testIssuer, Subject: "sub-solo", Groups: nil}
			resolved, rerr := svc.ResolveUser(ctx, claims)
			if rerr != nil {
				t.Fatalf("provision solo user: %v", rerr)
			}
			soloID = resolved.User.ID
			if _, err := dbClient.ExecContext(ctx, `
				UPDATE admin_users SET username = 'sync-solo-check', platform_role = $2 WHERE id = $1
			`, soloID, auth.PlatformRoleSuperadmin); err != nil {
				t.Fatalf("promote solo user: %v", err)
			}
		}
		if _, err := dbClient.ExecContext(ctx, `
			UPDATE admin_users SET disabled_at = NOW()
			WHERE platform_role = $1 AND id <> $2 AND disabled_at IS NULL
		`, auth.PlatformRoleSuperadmin, soloID); err != nil {
			t.Fatalf("disable other superadmins: %v", err)
		}
		defer func() {
			if _, err := dbClient.ExecContext(ctx, `
				UPDATE admin_users SET disabled_at = NULL WHERE username = 'sync-root'
			`); err != nil {
				t.Fatalf("re-enable root: %v", err)
			}
		}()

		claims := &Claims{Issuer: testIssuer, Subject: "sub-solo", Groups: nil}
		resolved, err := svc.ResolveUser(ctx, claims)
		if err != nil {
			t.Fatalf("solo login: %v", err)
		}
		if resolved.User.PlatformRole != auth.PlatformRoleSuperadmin {
			t.Fatalf("last superadmin was demoted to %q", resolved.User.PlatformRole)
		}
		if resolved.Sync == nil || !resolved.Sync.SkippedDemotion {
			t.Fatalf("expected SkippedDemotion, got %+v", resolved.Sync)
		}
	})

	t.Run("JIT with mappings and no matched groups provisions role-less member", func(t *testing.T) {
		clearMappings(ctx, t, dbClient)
		insertMapping(ctx, t, dbClient, "elsewhere", &tenantID, auth.RoleAdmin)

		claims := &Claims{Issuer: testIssuer, Subject: "sub-roleless", Groups: []string{"unmapped"}}
		resolved, err := svc.ResolveUser(ctx, claims)
		if err != nil {
			t.Fatalf("provision: %v", err)
		}
		if !resolved.JITCreated {
			t.Fatal("expected JIT provisioning")
		}
		if resolved.User.PlatformRole != auth.PlatformRoleMember {
			t.Fatalf("expected member, got %q", resolved.User.PlatformRole)
		}
		if got := membershipRoles(ctx, t, dbClient, resolved.User.ID); len(got) != 0 {
			t.Fatalf("expected zero memberships (no JIT-default fallback), got %v", got)
		}
	})

	t.Run("presented groups are catalogued for the mapping editor", func(t *testing.T) {
		clearMappings(ctx, t, dbClient) // recording is independent of mappings

		claims := &Claims{Issuer: testIssuer, Subject: "sub-seen", Groups: []string{"catalogued-group"}}
		if _, err := svc.ResolveUser(ctx, claims); err != nil {
			t.Fatalf("login: %v", err)
		}

		var firstSeen, lastSeen string
		if err := dbClient.QueryRowContext(ctx, `
			SELECT first_seen_at, last_seen_at FROM oidc_seen_groups WHERE group_name = 'catalogued-group'
		`).Scan(&firstSeen, &lastSeen); err != nil {
			t.Fatalf("seen group not recorded: %v", err)
		}

		// Second login bumps last_seen_at, keeps first_seen_at.
		if _, err := svc.ResolveUser(ctx, claims); err != nil {
			t.Fatalf("second login: %v", err)
		}
		var firstSeen2, lastSeen2 string
		if err := dbClient.QueryRowContext(ctx, `
			SELECT first_seen_at, last_seen_at FROM oidc_seen_groups WHERE group_name = 'catalogued-group'
		`).Scan(&firstSeen2, &lastSeen2); err != nil {
			t.Fatalf("seen group lookup after re-login: %v", err)
		}
		if firstSeen2 != firstSeen {
			t.Fatalf("first_seen_at changed on re-login: %s → %s", firstSeen, firstSeen2)
		}
		if lastSeen2 < lastSeen {
			t.Fatalf("last_seen_at went backwards: %s → %s", lastSeen, lastSeen2)
		}
	})

	t.Run("JIT with matched groups uses mapped memberships", func(t *testing.T) {
		clearMappings(ctx, t, dbClient)
		insertMapping(ctx, t, dbClient, "dual", &tenantID, auth.RoleEditor)
		insertMapping(ctx, t, dbClient, "dual", &otherTenantID, auth.RoleViewer)

		claims := &Claims{Issuer: testIssuer, Subject: "sub-mapped-jit", Groups: []string{"dual"}}
		resolved, err := svc.ResolveUser(ctx, claims)
		if err != nil {
			t.Fatalf("provision: %v", err)
		}
		got := membershipRoles(ctx, t, dbClient, resolved.User.ID)
		if got[tenantID] != auth.RoleEditor || got[otherTenantID] != auth.RoleViewer || len(got) != 2 {
			t.Fatalf("expected mapped editor+viewer memberships, got %v", got)
		}
	})
}
