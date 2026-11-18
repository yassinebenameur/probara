//go:build !unit

package main

import (
	"context"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"

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

func TestResetTwoFactorTurnsItOffAndRetiresChallenges(t *testing.T) {
	ctx := context.Background()
	client, teardown := testutil.SetupPostgresDB(ctx, t)
	defer teardown()

	var userID string
	if err := client.QueryRowContext(ctx, `
		INSERT INTO admin_users (username, password_hash, platform_role, totp_secret_enc, totp_enabled_at, totp_last_step)
		VALUES ('ops', 'hash', 'superadmin', '{"v":1}', NOW(), 1) RETURNING id
	`).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	if _, err := client.ExecContext(ctx, `
		INSERT INTO admin_user_recovery_codes (admin_user_id, code_hash) VALUES ($1, 'h1'), ($1, 'h2');
	`, userID); err != nil {
		t.Fatal(err)
	}
	if _, err := client.ExecContext(ctx, `
		INSERT INTO admin_account_tokens (admin_user_id, purpose, token_hash, expires_at, new_email)
		VALUES ($1, 'mfa_challenge', 'c1', NOW() + INTERVAL '5 minutes', NULL),
		       ($1, 'email_change', 'e1', NOW() + INTERVAL '1 day', 'x@example.com')
	`, userID); err != nil {
		t.Fatal(err)
	}

	was, err := resetTwoFactor(ctx, client, "ops")
	if err != nil || !was {
		t.Fatalf("resetTwoFactor() = %v, %v; want true", was, err)
	}
	var enabled, secret bool
	var codes, open int
	if err := client.QueryRowContext(ctx, `
		SELECT totp_enabled_at IS NOT NULL, totp_secret_enc IS NOT NULL,
		       (SELECT COUNT(*) FROM admin_user_recovery_codes WHERE admin_user_id = $1),
		       (SELECT COUNT(*) FROM admin_account_tokens WHERE admin_user_id = $1 AND revoked_at IS NULL)
		FROM admin_users WHERE id = $1
	`, userID).Scan(&enabled, &secret, &codes, &open); err != nil {
		t.Fatal(err)
	}
	if enabled || secret || codes != 0 || open != 0 {
		t.Fatalf("after reset: enabled=%v secret=%v codes=%d open tokens=%d", enabled, secret, codes, open)
	}
	if was, err := resetTwoFactor(ctx, client, "ops"); err != nil || was {
		t.Fatalf("second reset = %v, %v; want false", was, err)
	}
	if _, err := resetTwoFactor(ctx, client, "nobody"); err != errUnknownUser {
		t.Fatalf("unknown user: %v", err)
	}

	// The password upsert retires open links too.
	if _, err := client.ExecContext(ctx, `
		INSERT INTO admin_account_tokens (admin_user_id, purpose, token_hash, expires_at)
		VALUES ($1, 'password_reset', 'r1', NOW() + INTERVAL '1 hour')
	`, userID); err != nil {
		t.Fatal(err)
	}
	// A lock held after repeated failed codes, and an MCP connection.
	if _, err := client.ExecContext(ctx, `
		UPDATE admin_users SET lockout_held = TRUE, second_factor_lockouts = 3, second_factor_lockout_at = NOW(),
		                       locked_until = NOW() + INTERVAL '100 years'
		WHERE id = $1
	`, userID); err != nil {
		t.Fatal(err)
	}
	tenant := testutil.InsertTenant(ctx, t, client, "T")
	var grant string
	if err := client.QueryRowContext(ctx, `
		WITH c AS (INSERT INTO mcp_oauth_clients (client_name, redirect_uris) VALUES ('a', ARRAY['http://x']) RETURNING id)
		INSERT INTO mcp_oauth_grants (client_id, admin_user_id, tenant_id, scope) SELECT id, $1, $2, 'read' FROM c RETURNING id
	`, userID, tenant).Scan(&grant); err != nil {
		t.Fatal(err)
	}
	if err := upsertSuperadmin(ctx, client, "ops", "new-hash"); err != nil {
		t.Fatal(err)
	}
	if err := client.QueryRowContext(ctx, `SELECT COUNT(*) FROM admin_account_tokens WHERE admin_user_id = $1 AND revoked_at IS NULL`, userID).Scan(&open); err != nil || open != 0 {
		t.Fatalf("open tokens after the upsert: %d %v", open, err)
	}
	var held, locked, grantRevoked bool
	var lockouts int
	if err := client.QueryRowContext(ctx, `
		SELECT lockout_held, locked_until IS NOT NULL, second_factor_lockouts,
		       (SELECT revoked_at IS NOT NULL FROM mcp_oauth_grants WHERE id = $2)
		FROM admin_users WHERE id = $1
	`, userID, grant).Scan(&held, &locked, &lockouts, &grantRevoked); err != nil {
		t.Fatal(err)
	}
	if held || locked || lockouts != 0 || !grantRevoked {
		t.Fatalf("after the upsert: held=%v locked=%v lockouts=%d grant revoked=%v", held, locked, lockouts, grantRevoked)
	}
}

// The break-glass tool must work on a database whose migration job failed
// before the lockout and session migrations (000116, 000117) ran.
func TestUpsertSuperadmin_WorksBeforeTheIdentityMigrations(t *testing.T) {
	ctx := context.Background()
	client, teardown := testutil.SetupPostgresDB(ctx, t)
	defer teardown()

	_, file, _, _ := runtime.Caller(0)
	driver, err := postgres.WithInstance(client.DB, &postgres.Config{})
	if err != nil {
		t.Fatal(err)
	}
	m, err := migrate.NewWithDatabaseInstance("file://"+filepath.Join(filepath.Dir(file), "..", "..", "shared", "db", "migrations"), "postgres", driver)
	if err != nil {
		t.Fatal(err)
	}
	// Not m.Close(): the driver would close client.DB, which teardown owns.
	if err := m.Migrate(113); err != nil {
		t.Fatalf("migrate down to 113: %v", err)
	}

	if _, err := client.ExecContext(ctx, `
		INSERT INTO admin_users (username, password_hash, platform_role) VALUES ('ops', 'old-hash', 'member')
	`); err != nil {
		t.Fatal(err)
	}
	if err := upsertSuperadmin(ctx, client, "ops", "new-hash"); err != nil {
		t.Fatalf("upsertSuperadmin() on the old schema: %v", err)
	}
	if err := upsertSuperadmin(ctx, client, "fresh", "hash"); err != nil {
		t.Fatalf("upsertSuperadmin() of a new user on the old schema: %v", err)
	}
	var role, hash string
	if err := client.QueryRowContext(ctx, `SELECT platform_role, password_hash FROM admin_users WHERE username = 'ops'`).Scan(&role, &hash); err != nil {
		t.Fatal(err)
	}
	if role != auth.PlatformRoleSuperadmin || hash != "new-hash" {
		t.Fatalf("got %s/%s, want superadmin/new-hash", role, hash)
	}
	// -reset-2fa on the old schema: nothing to reset, the user must exist.
	if was, err := resetTwoFactor(ctx, client, "ops"); err != nil || was {
		t.Fatalf("resetTwoFactor() on the old schema = %v, %v", was, err)
	}
	if _, err := resetTwoFactor(ctx, client, "nobody"); err != errUnknownUser {
		t.Fatalf("unknown user on the old schema: %v", err)
	}
}

// Migrations 000123 and 000124 go down and back up cleanly, and -reset-2fa
// works between them (tokens table present, two-factor columns not).
func TestAccountMigrationsDownAndUp(t *testing.T) {
	ctx := context.Background()
	client, teardown := testutil.SetupPostgresDB(ctx, t)
	defer teardown()

	_, file, _, _ := runtime.Caller(0)
	driver, err := postgres.WithInstance(client.DB, &postgres.Config{})
	if err != nil {
		t.Fatal(err)
	}
	m, err := migrate.NewWithDatabaseInstance("file://"+filepath.Join(filepath.Dir(file), "..", "..", "shared", "db", "migrations"), "postgres", driver)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.ExecContext(ctx, `INSERT INTO admin_users (username, password_hash, platform_role) VALUES ('ops', 'h', 'superadmin')`); err != nil {
		t.Fatal(err)
	}
	if err := m.Steps(-1); err != nil {
		t.Fatalf("down 000124: %v", err)
	}
	if was, err := resetTwoFactor(ctx, client, "ops"); err != nil || was {
		t.Fatalf("resetTwoFactor() without 000124 = %v, %v", was, err)
	}
	if err := upsertSuperadmin(ctx, client, "ops", "h2"); err != nil {
		t.Fatalf("upsert without 000124: %v", err)
	}
	if err := m.Steps(-1); err != nil {
		t.Fatalf("down 000123: %v", err)
	}
	if err := upsertSuperadmin(ctx, client, "ops", "h3"); err != nil {
		t.Fatalf("upsert without 000123: %v", err)
	}
	if err := m.Steps(2); err != nil {
		t.Fatalf("up again: %v", err)
	}
	var tokens, codes bool
	if err := client.QueryRowContext(ctx, `
		SELECT to_regclass('admin_account_tokens') IS NOT NULL, to_regclass('admin_user_recovery_codes') IS NOT NULL
	`).Scan(&tokens, &codes); err != nil || !tokens || !codes {
		t.Fatalf("after up: tokens=%v codes=%v err=%v", tokens, codes, err)
	}
}
