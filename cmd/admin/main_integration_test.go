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
