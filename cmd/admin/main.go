package main

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"os"
	"strconv"

	"github.com/lib/pq"

	"github.com/yassinebenameur/probara/shared/auth"
	"github.com/yassinebenameur/probara/shared/db"
)

func main() {
	reset2FA := flag.String("reset-2fa", "", "turn off two-factor authentication for this username (lost authenticator) and exit")
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
			fmt.Fprintln(os.Stderr, "Failed to reset two-factor authentication:", err)
			os.Exit(1)
		}
		if wasEnabled {
			fmt.Printf("Two-factor authentication turned off for %s; they sign in with their password and can enroll again\n", *reset2FA)
		} else {
			fmt.Printf("%s had no two-factor authentication enabled; any pending enrollment was cleared\n", *reset2FA)
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
// re-enables it, and in both cases makes it a platform superadmin. It also
// clears any login lockout on the account.
//
// The role must be explicit: admin_users.platform_role defaults to 'member'
// (migration 000073), and a member with no tenant_memberships can do nothing,
// so relying on the default leaves a fresh install unadministrable. This
// matches the API's first-admin form (adminusers CreateFirstUser) and the
// first OIDC login: a superadmin with no tenant memberships, which it does not
// need — superadmins reach every tenant. Promoting on conflict is deliberate:
// this is the break-glass path for an operator with database access, and its
// whole point is to leave behind an account that can administer the install.
func upsertSuperadmin(ctx context.Context, client *db.Client, username, passwordHash string) error {
	_, err := client.ExecContext(ctx, `
		INSERT INTO admin_users (username, password_hash, platform_role)
		VALUES ($1, $2, $3)
		ON CONFLICT (username)
		DO UPDATE SET password_hash = EXCLUDED.password_hash,
		              platform_role = EXCLUDED.platform_role,
		              updated_at = NOW(),
		              disabled_at = NULL
	`, username, passwordHash, auth.PlatformRoleSuperadmin)
	if err != nil {
		return err
	}
	return clearLockout(ctx, client, username)
}

// clearLockout lifts a login lockout and records the password change, so the
// break-glass account can sign in at once. Before migration 000103 there is
// no lockout state, which is not an error.
func clearLockout(ctx context.Context, client *db.Client, username string) error {
	_, err := client.ExecContext(ctx, `
		UPDATE admin_users
		SET failed_login_count = 0, last_failed_login_at = NULL, locked_until = NULL,
		    password_changed_at = NOW()
		WHERE username = $1
	`, username)
	var pqErr *pq.Error
	if errors.As(err, &pqErr) && (pqErr.Code == "42P01" || pqErr.Code == "42703") {
		return nil
	}
	return err
}

// errUnknownUser: -reset-2fa named no account.
var errUnknownUser = errors.New("no such user")

// resetTwoFactor is the break-glass twin of POST /users/{id}/reset-2fa
// (adminauth.ResetMFA): it drops the TOTP secret, enrollment and recovery
// codes of one user. Before migration 000118 there is nothing to reset.
func resetTwoFactor(ctx context.Context, client *db.Client, username string) (bool, error) {
	tx, err := client.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	var wasEnabled bool
	err = tx.QueryRowContext(ctx, `
		WITH prior AS (
			SELECT id, totp_enabled_at IS NOT NULL AS was FROM admin_users WHERE username = $1 FOR UPDATE
		)
		UPDATE admin_users u
		SET totp_secret_enc = NULL, totp_enabled_at = NULL, totp_last_step = NULL, updated_at = NOW()
		FROM prior WHERE u.id = prior.id
		RETURNING prior.was
	`, username).Scan(&wasEnabled)
	var pqErr *pq.Error
	switch {
	case errors.As(err, &pqErr) && (pqErr.Code == "42P01" || pqErr.Code == "42703"):
		return false, nil
	case errors.Is(err, sql.ErrNoRows):
		return false, errUnknownUser
	case err != nil:
		return false, err
	}
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM admin_user_recovery_codes
		WHERE admin_user_id = (SELECT id FROM admin_users WHERE username = $1)
	`, username); err != nil {
		return false, err
	}
	return wasEnabled, tx.Commit()
}
