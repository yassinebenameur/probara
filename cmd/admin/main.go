package main

import (
	"context"
	"fmt"
	"os"
	"strconv"

	"github.com/yassinebenameur/probara/shared/auth"
	"github.com/yassinebenameur/probara/shared/db"
)

func main() {
	postgresURL := os.Getenv("POSTGRES_URL")
	if postgresURL == "" {
		fmt.Fprintln(os.Stderr, "POSTGRES_URL is required")
		os.Exit(1)
	}

	username := os.Getenv("ADMIN_USERNAME")
	password := os.Getenv("ADMIN_PASSWORD")
	if username == "" || password == "" {
		fmt.Fprintln(os.Stderr, "ADMIN_USERNAME and ADMIN_PASSWORD are required")
		os.Exit(1)
	}

	bcryptCost := 12
	if costStr := os.Getenv("ADMIN_BCRYPT_COST"); costStr != "" {
		cost, err := strconv.Atoi(costStr)
		if err != nil {
			fmt.Fprintln(os.Stderr, "Invalid ADMIN_BCRYPT_COST")
			os.Exit(1)
		}
		bcryptCost = cost
	}

	client, err := db.NewClient(postgresURL)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Failed to connect to database:", err)
		os.Exit(1)
	}
	defer client.Close()

	passwordHash, err := auth.HashPassword(password, bcryptCost)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Failed to hash password:", err)
		os.Exit(1)
	}

	if err := upsertSuperadmin(context.Background(), client, username, passwordHash); err != nil {
		fmt.Fprintln(os.Stderr, "Failed to create admin user:", err)
		os.Exit(1)
	}

	fmt.Printf("Admin user created/updated: %s (platform role %s)\n", username, auth.PlatformRoleSuperadmin)
}

// upsertSuperadmin creates the user, or resets an existing one's password and
// re-enables it, and in both cases makes it a platform superadmin.
//
// The role must be explicit: admin_users.platform_role defaults to 'member'
// (migration 000073), and a member with no tenant_memberships can do nothing,
// so relying on the default leaves a fresh install unadministrable. This
// matches the API's first-admin form (adminusers CreateFirstUser) and the
// first OIDC login: a superadmin with no tenant memberships, which it does not
// need — superadmins reach every tenant. Promoting on conflict is deliberate:
// this is the break-glass path for an operator with database access, and its
// whole point is to leave behind an account that can administer the install.
// For the same reason it lifts a sign-in lockout (migration 000117), and, as
// any password reset does, it ends the account's existing sessions (session
// families, migration 000118; the API's adminauth.RevokeUserSessions).
//
// It must work before those migrations have run too -- a failed migration
// job is exactly when an operator reaches for it -- so it looks for their
// columns and table first and leaves out what is not there yet.
func upsertSuperadmin(ctx context.Context, client *db.Client, username, passwordHash string) error {
	var hasLockout, hasFamilies bool
	if err := client.QueryRowContext(ctx, `
		SELECT EXISTS (
		           SELECT 1 FROM information_schema.columns
		           WHERE table_schema = current_schema() AND table_name = 'admin_users'
		             AND column_name = 'locked_until'),
		       to_regclass('admin_session_families') IS NOT NULL
	`).Scan(&hasLockout, &hasFamilies); err != nil {
		return err
	}

	tx, err := client.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	clearLock := ""
	if hasLockout {
		clearLock = `,
		              failed_login_count = 0,
		              last_failed_login_at = NULL,
		              locked_until = NULL`
	}
	var userID string
	if err := tx.QueryRowContext(ctx, `
		INSERT INTO admin_users (username, password_hash, platform_role)
		VALUES ($1, $2, $3)
		ON CONFLICT (username)
		DO UPDATE SET password_hash = EXCLUDED.password_hash,
		              platform_role = EXCLUDED.platform_role,
		              updated_at = NOW(),
		              disabled_at = NULL`+clearLock+`
		RETURNING id
	`, username, passwordHash, auth.PlatformRoleSuperadmin).Scan(&userID); err != nil {
		return err
	}
	if hasFamilies {
		if _, err := tx.ExecContext(ctx, `
			UPDATE admin_session_families
			SET revoked_at = NOW(), revoke_reason = 'password_reset'
			WHERE admin_user_id = $1 AND revoked_at IS NULL
		`, userID); err != nil {
			return err
		}
	}
	// admin_sessions predates both migrations.
	if _, err := tx.ExecContext(ctx, `
		UPDATE admin_sessions SET revoked_at = NOW()
		WHERE admin_user_id = $1 AND revoked_at IS NULL
	`, userID); err != nil {
		return err
	}
	return tx.Commit()
}
