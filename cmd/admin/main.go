package main

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"os"
	"strconv"

	"github.com/yassinebenameur/probara/shared/auth"
	"github.com/yassinebenameur/probara/shared/db"
)

func main() {
	reset2FA := flag.String("reset-2fa", "", "turn off two-factor sign-in for this username (lost authenticator) and exit, leaving the password as it is")
	flag.Parse()

	postgresURL := os.Getenv("POSTGRES_URL")
	if postgresURL == "" {
		fmt.Fprintln(os.Stderr, "POSTGRES_URL is required")
		os.Exit(1)
	}

	if *reset2FA != "" {
		client, err := db.NewClient(postgresURL)
		if err != nil {
			fmt.Fprintln(os.Stderr, "Failed to connect to database:", err)
			os.Exit(1)
		}
		defer client.Close()
		wasEnabled, err := resetTwoFactor(context.Background(), client, *reset2FA)
		if err != nil {
			fmt.Fprintln(os.Stderr, "Failed to reset two-factor sign-in:", err)
			client.Close()
			os.Exit(1)
		}
		if wasEnabled {
			fmt.Printf("Two-factor sign-in turned off for %s; they sign in with their password and can enrol again\n", *reset2FA)
		} else {
			fmt.Printf("%s had no two-factor sign-in on; any pending enrolment was cleared\n", *reset2FA)
		}
		return
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
// For the same reason it lifts a sign-in lockout (migration 000116), and, as
// any password reset does, it ends the account's existing sessions (session
// families, migration 000117; the API's adminauth.RevokeUserSessions), its
// MCP connections (000096), and retires its open invite, reset and
// email-change links and sign-in challenges (migration 000123;
// adminauth.RetireAccountTokens). It also lifts a lock held after repeated
// failed two-factor codes (000124). Two-factor sign-in stays on: -reset-2fa
// turns it off.
//
// It must work before those migrations have run too -- a failed migration
// job is exactly when an operator reaches for it -- so it looks for their
// columns and table first and leaves out what is not there yet.
func upsertSuperadmin(ctx context.Context, client *db.Client, username, passwordHash string) error {
	var hasLockout, hasHold, hasFamilies, hasTokens, hasMCP bool
	if err := client.QueryRowContext(ctx, `
		SELECT EXISTS (
		           SELECT 1 FROM information_schema.columns
		           WHERE table_schema = current_schema() AND table_name = 'admin_users'
		             AND column_name = 'locked_until'),
		       EXISTS (
		           SELECT 1 FROM information_schema.columns
		           WHERE table_schema = current_schema() AND table_name = 'admin_users'
		             AND column_name = 'lockout_held'),
		       to_regclass('admin_session_families') IS NOT NULL,
		       to_regclass('admin_account_tokens') IS NOT NULL,
		       to_regclass('mcp_oauth_grants') IS NOT NULL
	`).Scan(&hasLockout, &hasHold, &hasFamilies, &hasTokens, &hasMCP); err != nil {
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
	if hasHold {
		// A lock held after repeated failed two-factor codes too.
		clearLock += `,
		              lockout_held = FALSE,
		              second_factor_lockouts = 0,
		              second_factor_lockout_at = NULL`
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
	if hasTokens {
		if _, err := tx.ExecContext(ctx, `
			UPDATE admin_account_tokens SET revoked_at = NOW()
			WHERE admin_user_id = $1 AND used_at IS NULL AND revoked_at IS NULL
		`, userID); err != nil {
			return err
		}
	}
	if hasMCP {
		// As the API's adminauth.RevokeMCPAccess: MCP clients connected
		// under the old password stop acting as the user.
		if _, err := tx.ExecContext(ctx, `
			UPDATE mcp_oauth_grants SET revoked_at = NOW() WHERE admin_user_id = $1 AND revoked_at IS NULL
		`, userID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			DELETE FROM mcp_oauth_codes WHERE admin_user_id = $1 AND grant_id IS NULL
		`, userID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// errUnknownUser: -reset-2fa named no account.
var errUnknownUser = errors.New("no such user")

// resetTwoFactor is the break-glass twin of POST /users/{id}/reset-2fa
// (adminauth.ResetTwoFactor): it drops one user's TOTP secret, enrolment and
// recovery codes, and retires their pending email change and open sign-in
// challenges. It reports whether two-factor was on. Before migration 000124
// nobody has a second factor, so it only checks that the user exists.
func resetTwoFactor(ctx context.Context, client *db.Client, username string) (bool, error) {
	var hasTwoFactor, hasTokens bool
	if err := client.QueryRowContext(ctx, `
		SELECT EXISTS (
		           SELECT 1 FROM information_schema.columns
		           WHERE table_schema = current_schema() AND table_name = 'admin_users'
		             AND column_name = 'totp_enabled_at'),
		       to_regclass('admin_account_tokens') IS NOT NULL
	`).Scan(&hasTwoFactor, &hasTokens); err != nil {
		return false, err
	}

	tx, err := client.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()

	var userID string
	err = tx.QueryRowContext(ctx, `SELECT id FROM admin_users WHERE username = $1 FOR UPDATE`, username).Scan(&userID)
	if errors.Is(err, sql.ErrNoRows) {
		return false, errUnknownUser
	}
	if err != nil {
		return false, err
	}
	if !hasTwoFactor {
		return false, nil
	}
	var wasEnabled bool
	if err := tx.QueryRowContext(ctx, `
		WITH prior AS (SELECT totp_enabled_at IS NOT NULL AS was FROM admin_users WHERE id = $1)
		UPDATE admin_users
		SET totp_secret_enc = NULL, totp_enabled_at = NULL, totp_last_step = NULL, updated_at = NOW()
		WHERE id = $1
		RETURNING (SELECT was FROM prior)
	`, userID).Scan(&wasEnabled); err != nil {
		return false, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM admin_user_recovery_codes WHERE admin_user_id = $1`, userID); err != nil {
		return false, err
	}
	if hasTokens {
		if _, err := tx.ExecContext(ctx, `
			UPDATE admin_account_tokens SET revoked_at = NOW()
			WHERE admin_user_id = $1 AND purpose IN ('email_change', 'mfa_challenge')
			  AND used_at IS NULL AND revoked_at IS NULL
		`, userID); err != nil {
			return false, err
		}
	}
	return wasEnabled, tx.Commit()
}
