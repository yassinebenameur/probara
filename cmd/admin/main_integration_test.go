//go:build !unit

package main

import (
	"context"
	"testing"

	"github.com/yassinebenameur/probara/shared/auth"
	"github.com/yassinebenameur/probara/shared/testutil"
)

func TestUpsertSuperadmin_FreshDatabaseGetsSuperadmin(t *testing.T) {
	ctx := context.Background()
	client, teardown := testutil.SetupPostgresDB(ctx, t)
	defer teardown()

	if err := upsertSuperadmin(ctx, client, "admin", "hash-1"); err != nil {
		t.Fatalf("upsertSuperadmin() error = %v", err)
	}

	var role string
	var memberships int
	if err := client.QueryRowContext(ctx, `
		SELECT u.platform_role, (SELECT COUNT(*) FROM tenant_memberships m WHERE m.admin_user_id = u.id)
		FROM admin_users u WHERE u.username = 'admin'
	`).Scan(&role, &memberships); err != nil {
		t.Fatalf("load user: %v", err)
	}
	if role != auth.PlatformRoleSuperadmin {
		t.Fatalf("platform_role = %q, want %q", role, auth.PlatformRoleSuperadmin)
	}
	// Parity with the API's first-admin form: a superadmin reaches every
	// tenant, so it gets no membership rows.
	if memberships != 0 {
		t.Fatalf("tenant memberships = %d, want 0", memberships)
	}
}

func TestUpsertSuperadmin_ExistingUserIsResetPromotedAndReenabled(t *testing.T) {
	ctx := context.Background()
	client, teardown := testutil.SetupPostgresDB(ctx, t)
	defer teardown()

	if _, err := client.ExecContext(ctx, `
		INSERT INTO admin_users (username, password_hash, platform_role, disabled_at)
		VALUES ('ops', 'old-hash', 'member', NOW())
	`); err != nil {
		t.Fatalf("seed user: %v", err)
	}

	if err := upsertSuperadmin(ctx, client, "ops", "new-hash"); err != nil {
		t.Fatalf("upsertSuperadmin() error = %v", err)
	}

	var role, hash string
	var disabled bool
	if err := client.QueryRowContext(ctx, `
		SELECT platform_role, password_hash, disabled_at IS NOT NULL FROM admin_users WHERE username = 'ops'
	`).Scan(&role, &hash, &disabled); err != nil {
		t.Fatalf("load user: %v", err)
	}
	if role != auth.PlatformRoleSuperadmin || hash != "new-hash" || disabled {
		t.Fatalf("got role=%q hash=%q disabled=%v, want superadmin/new-hash/enabled", role, hash, disabled)
	}
}

func TestUpsertSuperadmin_LiftsLockoutAndEndsSessions(t *testing.T) {
	ctx := context.Background()
	client, teardown := testutil.SetupPostgresDB(ctx, t)
	defer teardown()

	var userID string
	if err := client.QueryRowContext(ctx, `
		INSERT INTO admin_users (username, password_hash, platform_role, failed_login_count, locked_until)
		VALUES ('locked-ops', 'old-hash', 'superadmin', 3, NOW() + INTERVAL '10 minutes')
		RETURNING id
	`).Scan(&userID); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	var familyID string
	if err := client.QueryRowContext(ctx, `
		INSERT INTO admin_session_families (admin_user_id, auth_method, expires_at)
		VALUES ($1, 'password', NOW() + INTERVAL '1 day') RETURNING id
	`, userID).Scan(&familyID); err != nil {
		t.Fatalf("seed session: %v", err)
	}
	if _, err := client.ExecContext(ctx, `
		INSERT INTO admin_sessions (admin_user_id, refresh_token_hash, expires_at, family_id)
		VALUES ($1, 'h', NOW() + INTERVAL '1 day', $2)
	`, userID, familyID); err != nil {
		t.Fatalf("seed token: %v", err)
	}

	if err := upsertSuperadmin(ctx, client, "locked-ops", "new-hash"); err != nil {
		t.Fatalf("upsertSuperadmin() error = %v", err)
	}

	var count int
	var locked, familyRevoked, tokenRevoked bool
	if err := client.QueryRowContext(ctx, `
		SELECT u.failed_login_count, u.locked_until IS NOT NULL,
		       (SELECT revoked_at IS NOT NULL FROM admin_session_families WHERE id = $2),
		       (SELECT revoked_at IS NOT NULL FROM admin_sessions WHERE family_id = $2)
		FROM admin_users u WHERE u.id = $1
	`, userID, familyID).Scan(&count, &locked, &familyRevoked, &tokenRevoked); err != nil {
		t.Fatalf("load state: %v", err)
	}
	if count != 0 || locked {
		t.Fatalf("lockout = %d/%v, want cleared", count, locked)
	}
	if !familyRevoked || !tokenRevoked {
		t.Fatalf("session revoked = %v/%v, want both", familyRevoked, tokenRevoked)
	}
}
