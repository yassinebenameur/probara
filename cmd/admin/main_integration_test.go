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

func TestUpsertSuperadmin_ClearsLoginLockout(t *testing.T) {
	ctx := context.Background()
	client, teardown := testutil.SetupPostgresDB(ctx, t)
	defer teardown()

	if _, err := client.ExecContext(ctx, `
		INSERT INTO admin_users (username, password_hash, platform_role, failed_login_count, last_failed_login_at, locked_until)
		VALUES ('locked', 'old-hash', 'superadmin', 10, NOW(), NOW() + INTERVAL '15 minutes')
	`); err != nil {
		t.Fatalf("seed user: %v", err)
	}

	if err := upsertSuperadmin(ctx, client, "locked", "new-hash"); err != nil {
		t.Fatalf("upsertSuperadmin() error = %v", err)
	}

	var failures int
	var locked, passwordChanged bool
	if err := client.QueryRowContext(ctx, `
		SELECT failed_login_count, locked_until IS NOT NULL, password_changed_at IS NOT NULL
		FROM admin_users WHERE username = 'locked'
	`).Scan(&failures, &locked, &passwordChanged); err != nil {
		t.Fatalf("load user: %v", err)
	}
	if failures != 0 || locked || !passwordChanged {
		t.Fatalf("got failures=%d locked=%v password_changed=%v, want 0/false/true", failures, locked, passwordChanged)
	}
}

func TestResetTwoFactorClearsEnrollment(t *testing.T) {
	ctx := context.Background()
	client, teardown := testutil.SetupPostgresDB(ctx, t)
	defer teardown()

	var id string
	if err := client.QueryRowContext(ctx, `
		INSERT INTO admin_users (username, password_hash, platform_role, totp_secret_enc, totp_enabled_at, totp_last_step)
		VALUES ('ops', 'hash', 'member', 'sealed', NOW(), 42) RETURNING id
	`).Scan(&id); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	if _, err := client.ExecContext(ctx, `INSERT INTO admin_user_recovery_codes (admin_user_id, code_hash) VALUES ($1, 'h1'), ($1, 'h2')`, id); err != nil {
		t.Fatal(err)
	}
	if _, err := client.ExecContext(ctx, `
		INSERT INTO admin_user_tokens (admin_user_id, purpose, token_hash, new_email, expires_at)
		VALUES ($1, 'email_change', 'th', 'new@example.com', NOW() + INTERVAL '1 day')
	`, id); err != nil {
		t.Fatal(err)
	}

	was, err := resetTwoFactor(ctx, client, "ops")
	if err != nil || !was {
		t.Fatalf("resetTwoFactor = %v, %v; want true", was, err)
	}
	var secretSet, enabled bool
	var codes int
	if err := client.QueryRowContext(ctx, `
		SELECT totp_secret_enc IS NOT NULL, totp_enabled_at IS NOT NULL,
		       (SELECT COUNT(*) FROM admin_user_recovery_codes WHERE admin_user_id = u.id)
		FROM admin_users u WHERE username = 'ops'
	`).Scan(&secretSet, &enabled, &codes); err != nil {
		t.Fatal(err)
	}
	if secretSet || enabled || codes != 0 {
		t.Fatalf("after reset: secret=%v enabled=%v codes=%d", secretSet, enabled, codes)
	}
	var openChanges int
	if err := client.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM admin_user_tokens WHERE admin_user_id = $1 AND used_at IS NULL
	`, id).Scan(&openChanges); err != nil {
		t.Fatal(err)
	}
	if openChanges != 0 {
		t.Fatalf("pending email change survived the 2FA reset")
	}
	if was, err := resetTwoFactor(ctx, client, "ops"); err != nil || was {
		t.Fatalf("second reset = %v, %v; want false", was, err)
	}
	if _, err := resetTwoFactor(ctx, client, "nobody"); err == nil {
		t.Fatal("unknown username accepted")
	}
}
