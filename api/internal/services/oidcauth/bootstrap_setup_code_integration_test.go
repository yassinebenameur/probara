//go:build !unit

package oidcauth

import (
	"context"
	"errors"
	"testing"

	"github.com/yassinebenameur/probara/api/internal/models"
	"github.com/yassinebenameur/probara/api/internal/services/adminusers"
	"github.com/yassinebenameur/probara/shared/auth"
	"github.com/yassinebenameur/probara/shared/testutil"
)

// With BOOTSTRAP_SETUP_CODE configured, an SSO sign-in on an empty install
// creates an ordinary member, and the local first-admin form (gated by the
// code in the handler) stays open until a superadmin exists. Without a code,
// the first SSO user becomes superadmin and closes bootstrap, as before.
func TestSetupCodeKeepsSSOFromBootstrapping(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test requires Docker")
	}

	for _, codeSet := range []bool{true, false} {
		name := "setup code unset"
		if codeSet {
			name = "setup code set"
		}
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
			defer cleanup()

			tenantID := testutil.InsertTenant(ctx, t, dbClient, "sso-tenant")
			oidcSvc := newTestService(dbClient, tenantID, true)
			oidcSvc.ConfigureSetupCode(codeSet)
			users := adminusers.NewService(dbClient, 4)
			users.ConfigureSetupCode(codeSet)

			resolved, err := oidcSvc.ResolveUser(ctx, &Claims{Issuer: testIssuer, Subject: "sub-first", Email: "first@example.com", EmailVerified: true})
			if err != nil {
				t.Fatalf("ResolveUser: %v", err)
			}
			closed, err := users.HasActiveUsers(ctx)
			if err != nil {
				t.Fatal(err)
			}

			if !codeSet {
				if resolved.User.PlatformRole != auth.PlatformRoleSuperadmin {
					t.Fatalf("without a setup code the first SSO user must be superadmin, got %q", resolved.User.PlatformRole)
				}
				if !closed {
					t.Fatal("without a setup code bootstrap must close once any user exists")
				}
				if _, err := users.CreateFirstUser(ctx, &models.CreateAdminUserRequest{Username: "local-admin", Password: strPtr("very-secure-password")}); !errors.Is(err, adminusers.ErrBootstrapClosed) {
					t.Fatalf("expected ErrBootstrapClosed, got %v", err)
				}
				return
			}

			if resolved.User.PlatformRole != auth.PlatformRoleMember {
				t.Fatalf("with a setup code the first SSO user must be a member, got %q", resolved.User.PlatformRole)
			}
			var role string
			if err := dbClient.QueryRowContext(ctx, `SELECT role FROM tenant_memberships WHERE admin_user_id = $1 AND tenant_id = $2`, resolved.User.ID, tenantID).Scan(&role); err != nil {
				t.Fatalf("the SSO member did not get the JIT default membership: %v", err)
			}
			if role != auth.RoleViewer {
				t.Fatalf("JIT default membership role = %q, want %q", role, auth.RoleViewer)
			}
			if closed {
				t.Fatal("bootstrap must stay open while no superadmin exists")
			}

			admin, err := users.CreateFirstUser(ctx, &models.CreateAdminUserRequest{Username: "local-admin", Password: strPtr("very-secure-password")})
			if err != nil {
				t.Fatalf("first-admin form with a pre-existing SSO member: %v", err)
			}
			if admin.PlatformRole != auth.PlatformRoleSuperadmin {
				t.Fatalf("first admin role = %q, want superadmin", admin.PlatformRole)
			}
			if closed, err = users.HasActiveUsers(ctx); err != nil || !closed {
				t.Fatalf("bootstrap must close once a superadmin exists (closed=%v err=%v)", closed, err)
			}
			if _, err := users.CreateFirstUser(ctx, &models.CreateAdminUserRequest{Username: "second-admin", Password: strPtr("very-secure-password")}); !errors.Is(err, adminusers.ErrBootstrapClosed) {
				t.Fatalf("expected ErrBootstrapClosed for a second first admin, got %v", err)
			}

			// A later SSO sign-in is still an ordinary member.
			later, err := oidcSvc.ResolveUser(ctx, &Claims{Issuer: testIssuer, Subject: "sub-later", Email: "later@example.com", EmailVerified: true})
			if err != nil {
				t.Fatalf("ResolveUser: %v", err)
			}
			if later.User.PlatformRole != auth.PlatformRoleMember {
				t.Fatalf("later SSO user role = %q, want member", later.User.PlatformRole)
			}
		})
	}
}

func strPtr(s string) *string { return &s }
