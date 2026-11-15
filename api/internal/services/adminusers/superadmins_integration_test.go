//go:build !unit

package adminusers

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/api/internal/models"
	"github.com/yassinebenameur/probara/shared/auth"
	"github.com/yassinebenameur/probara/shared/db"
	"github.com/yassinebenameur/probara/shared/testutil"
)

func createSuperadmin(ctx context.Context, t *testing.T, svc *Service, name string) uuid.UUID {
	t.Helper()
	u, err := svc.CreateUser(ctx, &models.CreateAdminUserRequest{Username: name, Password: strPtr("very-secure-password"), PlatformRole: auth.PlatformRoleSuperadmin})
	if err != nil {
		t.Fatalf("create %s: %v", name, err)
	}
	return u.ID
}

func activeSuperadmins(ctx context.Context, t *testing.T, dbClient *db.Client) int {
	t.Helper()
	var n int
	if err := dbClient.QueryRowContext(ctx, `SELECT COUNT(*) FROM admin_users WHERE platform_role = 'superadmin' AND disabled_at IS NULL`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// Two superadmins removing each other at the same moment: each check alone
// sees one other superadmin left, so without the superadmin-set lock both
// could succeed and leave none. Exactly one must win, every round.
func TestLastSuperadminGuardIsRaceFree(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test requires Docker")
	}
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()
	svc := NewService(dbClient, 4)
	member := auth.PlatformRoleMember
	superadmin := auth.PlatformRoleSuperadmin

	type op func(self, other uuid.UUID) error
	demote := func(_, other uuid.UUID) error {
		_, err := svc.UpdateUser(ctx, other, &models.UpdateAdminUserRequest{PlatformRole: &member})
		return err
	}
	remove := func(self, other uuid.UUID) error { return svc.DeleteUser(ctx, self, other) }
	refused := func(err error) bool {
		return errors.Is(err, ErrCannotDemoteLastAdmin) || errors.Is(err, ErrCannotDeleteLastUser)
	}

	for name, ops := range map[string][2]op{
		"demote each other":     {demote, demote},
		"delete each other":     {remove, remove},
		"demote while deleting": {demote, remove},
	} {
		t.Run(name, func(t *testing.T) {
			for round := 0; round < 10; round++ {
				if _, err := dbClient.ExecContext(ctx, `DELETE FROM admin_users`); err != nil {
					t.Fatal(err)
				}
				a := createSuperadmin(ctx, t, svc, "alice")
				b := createSuperadmin(ctx, t, svc, "bob")

				var start, done sync.WaitGroup
				start.Add(1)
				errs := make([]error, 2)
				for i, pair := range [2][2]uuid.UUID{{a, b}, {b, a}} {
					done.Add(1)
					go func(i int, self, other uuid.UUID) {
						defer done.Done()
						start.Wait()
						errs[i] = ops[i](self, other)
					}(i, pair[0], pair[1])
				}
				start.Done()
				done.Wait()

				succeeded := 0
				for _, err := range errs {
					switch {
					case err == nil:
						succeeded++
					case !refused(err):
						t.Fatalf("round %d: unexpected error %v", round, err)
					}
				}
				if succeeded != 1 {
					t.Fatalf("round %d: %d of 2 removals succeeded (errors %v), want exactly 1", round, succeeded, errs)
				}
				if n := activeSuperadmins(ctx, t, dbClient); n != 1 {
					t.Fatalf("round %d: %d active superadmins left, want 1", round, n)
				}
			}
		})
	}

	// Promoting is never refused, and demoting a member is a no-op change.
	if _, err := dbClient.ExecContext(ctx, `DELETE FROM admin_users`); err != nil {
		t.Fatal(err)
	}
	only := createSuperadmin(ctx, t, svc, "only")
	if _, err := svc.UpdateUser(ctx, only, &models.UpdateAdminUserRequest{PlatformRole: &superadmin}); err != nil {
		t.Fatalf("re-asserting superadmin on the last one: %v", err)
	}
	if _, err := svc.UpdateUser(ctx, only, &models.UpdateAdminUserRequest{PlatformRole: &member}); !errors.Is(err, ErrCannotDemoteLastAdmin) {
		t.Fatalf("demoting the only superadmin: %v, want ErrCannotDemoteLastAdmin", err)
	}
}

// Once a superadmin has existed, first-admin bootstrap stays closed even if
// no active user or superadmin is left (users disabled or demoted out of
// band), with or without a setup code.
func TestBootstrapClosesOnceASuperadminExisted(t *testing.T) {
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
			svc := NewService(dbClient, 4)
			svc.ConfigureSetupCode(codeSet)

			if closed, err := svc.HasActiveUsers(ctx); err != nil || closed {
				t.Fatalf("fresh install: closed=%v err=%v, want open", closed, err)
			}
			if ever, err := SuperadminEverExisted(ctx, dbClient); err != nil || ever {
				t.Fatalf("fresh install: ever=%v err=%v", ever, err)
			}
			first, err := svc.CreateFirstUser(ctx, &models.CreateAdminUserRequest{Username: "first-admin", Password: strPtr("very-secure-password")})
			if err != nil {
				t.Fatalf("CreateFirstUser: %v", err)
			}
			if ever, err := SuperadminEverExisted(ctx, dbClient); err != nil || !ever {
				t.Fatalf("after the first admin: ever=%v err=%v, want true", ever, err)
			}

			// Out of band: the superadmin is demoted and disabled, as a
			// database edit or restore could leave it.
			if _, err := dbClient.ExecContext(ctx, `UPDATE admin_users SET platform_role = 'member', disabled_at = NOW() WHERE id = $1`, first.ID); err != nil {
				t.Fatal(err)
			}
			if closed, err := svc.HasActiveUsers(ctx); err != nil || !closed {
				t.Fatalf("no active superadmin left: closed=%v err=%v, want still closed", closed, err)
			}
			if _, err := svc.CreateFirstUser(ctx, &models.CreateAdminUserRequest{Username: "claimer", Password: strPtr("very-secure-password")}); !errors.Is(err, ErrBootstrapClosed) {
				t.Fatalf("CreateFirstUser after a superadmin existed: %v, want ErrBootstrapClosed", err)
			}
			if _, err := dbClient.ExecContext(ctx, `DELETE FROM admin_users`); err != nil {
				t.Fatal(err)
			}
			if _, err := svc.CreateFirstUser(ctx, &models.CreateAdminUserRequest{Username: "claimer", Password: strPtr("very-secure-password")}); !errors.Is(err, ErrBootstrapClosed) {
				t.Fatalf("CreateFirstUser with every user deleted: %v, want ErrBootstrapClosed", err)
			}
		})
	}
}

// A member created before any superadmin does not set the flag; promoting
// one does, whatever path writes the role.
func TestSuperadminFlagFollowsRoleWrites(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test requires Docker")
	}
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()
	svc := NewService(dbClient, 4)
	m, err := svc.CreateUser(ctx, &models.CreateAdminUserRequest{Username: "member", Password: strPtr("very-secure-password")})
	if err != nil {
		t.Fatal(err)
	}
	if ever, err := SuperadminEverExisted(ctx, dbClient); err != nil || ever {
		t.Fatalf("after a member: ever=%v err=%v, want false", ever, err)
	}
	if _, err := dbClient.ExecContext(ctx, `UPDATE admin_users SET platform_role = 'superadmin' WHERE id = $1`, m.ID); err != nil {
		t.Fatal(err)
	}
	if ever, err := SuperadminEverExisted(ctx, dbClient); err != nil || !ever {
		t.Fatalf("after a promotion: ever=%v err=%v, want true", ever, err)
	}
}
