package oidcauth

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/shared/auth"
	"github.com/yassinebenameur/probara/shared/config"
	"github.com/yassinebenameur/probara/shared/db"
	"github.com/yassinebenameur/probara/shared/logger"
	"github.com/yassinebenameur/probara/shared/testutil"
)

const testIssuer = "https://idp.example.com"

func newTestService(dbClient *db.Client, tenantID uuid.UUID, jit bool) *Service {
	return NewService(config.OIDCConfig{
		Enabled:            true,
		IssuerURL:          testIssuer,
		ClientID:           "probara",
		ClientSecret:       "secret",
		JITProvision:       jit,
		JITDefaultRole:     auth.RoleViewer,
		JITDefaultTenantID: tenantID.String(),
	}, dbClient, logger.New("oidc-test", "error"))
}

func insertAdmin(ctx context.Context, t *testing.T, dbClient *db.Client, username string, email *string, platformRole string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if _, err := dbClient.ExecContext(ctx, `
		INSERT INTO admin_users (id, username, email, password_hash, platform_role, created_at, updated_at)
		VALUES ($1, $2, $3, 'x', $4, NOW(), NOW())
	`, id, username, email, platformRole); err != nil {
		t.Fatalf("insert admin: %v", err)
	}
	return id
}

func insertPasswordlessAdmin(ctx context.Context, t *testing.T, dbClient *db.Client, username string, email *string, platformRole string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if _, err := dbClient.ExecContext(ctx, `
		INSERT INTO admin_users (id, username, email, password_hash, platform_role, created_at, updated_at)
		VALUES ($1, $2, $3, NULL, $4, NOW(), NOW())
	`, id, username, email, platformRole); err != nil {
		t.Fatalf("insert passwordless admin: %v", err)
	}
	return id
}

func TestResolveUser(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test requires Docker")
	}

	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "oidc-tenant")
	svc := newTestService(dbClient, tenantID, true)

	// An existing active admin so JIT provisions members, not superadmins.
	insertAdmin(ctx, t, dbClient, "root", nil, auth.PlatformRoleSuperadmin)

	t.Run("email link binds identity to pre-created user", func(t *testing.T) {
		email := "invited@example.com"
		userID := insertPasswordlessAdmin(ctx, t, dbClient, "invited", &email, auth.PlatformRoleMember)

		claims := &Claims{Issuer: testIssuer, Subject: "sub-invited", Email: email, EmailVerified: true}
		user, jit, err := svc.ResolveUser(ctx, claims)
		if err != nil {
			t.Fatalf("ResolveUser: %v", err)
		}
		if jit {
			t.Fatal("expected email link, not JIT")
		}
		if user.ID != userID {
			t.Fatalf("linked wrong user: %s != %s", user.ID, userID)
		}
		if user.AuthMethod != "oidc" {
			t.Fatalf("expected auth method oidc after link, got %q", user.AuthMethod)
		}

		// Second login matches by (issuer, subject).
		again, jit, err := svc.ResolveUser(ctx, claims)
		if err != nil || jit || again.ID != userID {
			t.Fatalf("repeat login: user=%v jit=%v err=%v", again, jit, err)
		}
	})

	t.Run("verified email does not link password account or duplicate it", func(t *testing.T) {
		email := "password@example.com"
		userID := insertAdmin(ctx, t, dbClient, "password-user", &email, auth.PlatformRoleMember)

		claims := &Claims{Issuer: testIssuer, Subject: "sub-password", Email: email, EmailVerified: true}
		_, _, err := svc.ResolveUser(ctx, claims)
		if !errors.Is(err, ErrNotProvisioned) {
			t.Fatalf("expected ErrNotProvisioned, got %v", err)
		}

		var externalIssuer, externalSubject *string
		if err := dbClient.QueryRowContext(ctx, `
			SELECT external_issuer, external_subject FROM admin_users WHERE id = $1
		`, userID).Scan(&externalIssuer, &externalSubject); err != nil {
			t.Fatalf("lookup password user: %v", err)
		}
		if externalIssuer != nil || externalSubject != nil {
			t.Fatalf("password account was linked: issuer=%v subject=%v", externalIssuer, externalSubject)
		}

		var count int
		if err := dbClient.QueryRowContext(ctx, `
			SELECT COUNT(*) FROM admin_users WHERE lower(email) = lower($1)
		`, email).Scan(&count); err != nil {
			t.Fatalf("count users by email: %v", err)
		}
		if count != 1 {
			t.Fatalf("expected exactly one user for email, got %d", count)
		}
	})

	t.Run("unverified email neither links nor duplicates", func(t *testing.T) {
		email := "unverified@example.com"
		insertAdmin(ctx, t, dbClient, "unverified", &email, auth.PlatformRoleMember)

		// Without email_verified we must not bind the IdP identity to the
		// existing account, and the email unique index blocks a JIT
		// doppelgänger — the only safe outcome is refusing the login.
		claims := &Claims{Issuer: testIssuer, Subject: "sub-unverified", Email: email, EmailVerified: false}
		_, _, err := svc.ResolveUser(ctx, claims)
		if !errors.Is(err, ErrNotProvisioned) {
			t.Fatalf("expected ErrNotProvisioned, got %v", err)
		}
	})

	t.Run("JIT provisions viewer with default membership", func(t *testing.T) {
		claims := &Claims{Issuer: testIssuer, Subject: "sub-new", Email: "new@example.com", EmailVerified: true}
		user, jit, err := svc.ResolveUser(ctx, claims)
		if err != nil {
			t.Fatalf("ResolveUser: %v", err)
		}
		if !jit {
			t.Fatal("expected JIT provisioning")
		}
		if user.PlatformRole != auth.PlatformRoleMember {
			t.Fatalf("expected member, got %q", user.PlatformRole)
		}

		var role string
		if err := dbClient.QueryRowContext(ctx, `
			SELECT role FROM tenant_memberships WHERE admin_user_id = $1 AND tenant_id = $2
		`, user.ID, tenantID).Scan(&role); err != nil {
			t.Fatalf("membership lookup: %v", err)
		}
		if role != auth.RoleViewer {
			t.Fatalf("expected viewer membership, got %q", role)
		}
	})

	t.Run("username collision gets suffixed", func(t *testing.T) {
		email := "taken@example.com"
		insertAdmin(ctx, t, dbClient, email, nil, auth.PlatformRoleMember) // username == the email JIT will derive

		claims := &Claims{Issuer: testIssuer, Subject: "sub-collision", Email: email, EmailVerified: false}
		user, jit, err := svc.ResolveUser(ctx, claims)
		if err != nil {
			t.Fatalf("ResolveUser: %v", err)
		}
		if !jit {
			t.Fatal("expected JIT provisioning")
		}
		if user.Username == email {
			t.Fatal("expected suffixed username on collision")
		}
	})

	t.Run("JIT disabled yields ErrNotProvisioned", func(t *testing.T) {
		strictSvc := newTestService(dbClient, tenantID, false)
		claims := &Claims{Issuer: testIssuer, Subject: "sub-stranger", Email: "stranger@example.com", EmailVerified: true}
		_, _, err := strictSvc.ResolveUser(ctx, claims)
		if !errors.Is(err, ErrNotProvisioned) {
			t.Fatalf("expected ErrNotProvisioned, got %v", err)
		}
	})

	t.Run("disabled account cannot log back in", func(t *testing.T) {
		claims := &Claims{Issuer: testIssuer, Subject: "sub-disabled", Email: "disabled@example.com", EmailVerified: true}
		user, _, err := svc.ResolveUser(ctx, claims)
		if err != nil {
			t.Fatalf("initial provisioning: %v", err)
		}
		if _, err := dbClient.ExecContext(ctx, `UPDATE admin_users SET disabled_at = NOW() WHERE id = $1`, user.ID); err != nil {
			t.Fatalf("disable user: %v", err)
		}

		_, _, err = svc.ResolveUser(ctx, claims)
		if !errors.Is(err, ErrNotProvisioned) {
			t.Fatalf("expected ErrNotProvisioned for disabled account, got %v", err)
		}
	})
}

func TestResolveUserZeroAdminsBecomesSuperadmin(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test requires Docker")
	}

	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "fresh-tenant")
	svc := newTestService(dbClient, tenantID, true)

	claims := &Claims{Issuer: testIssuer, Subject: "sub-first", Email: "first@example.com", EmailVerified: true}
	user, jit, err := svc.ResolveUser(ctx, claims)
	if err != nil {
		t.Fatalf("ResolveUser: %v", err)
	}
	if !jit {
		t.Fatal("expected JIT provisioning")
	}
	if user.PlatformRole != auth.PlatformRoleSuperadmin {
		t.Fatalf("first user on a fresh install must be superadmin, got %q", user.PlatformRole)
	}
}
