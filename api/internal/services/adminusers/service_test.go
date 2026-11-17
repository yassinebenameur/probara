package adminusers

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"

	"github.com/yassinebenameur/probara/api/internal/models"
	"github.com/yassinebenameur/probara/shared/auth"
)

type mockRepository struct {
	listUsersFn               func(ctx context.Context, page, pageSize int) ([]models.AdminUser, int, error)
	getUserFn                 func(ctx context.Context, userID uuid.UUID) (*models.AdminUser, error)
	createUserFn              func(ctx context.Context, id uuid.UUID, username string, email, passwordHash *string, platformRole string, memberships []models.MembershipInput, now time.Time) (*models.AdminUser, error)
	createFirstUserFn         func(ctx context.Context, id uuid.UUID, username, passwordHash string, now time.Time) (*models.AdminUser, error)
	updateUserFn              func(ctx context.Context, userID uuid.UUID, username, email, passwordHash, platformRole *string, memberships *[]models.MembershipInput, now time.Time) (*models.AdminUser, error)
	revokeSessionsByAdminIDFn func(ctx context.Context, adminID uuid.UUID) error
	bootstrapClosedFn         func(ctx context.Context) (bool, error)
	deleteUserFn              func(ctx context.Context, userID uuid.UUID) error
	// untilSuperadmin records the bootstrap scope the service last asked for.
	untilSuperadmin bool
}

func (m *mockRepository) ListUsers(ctx context.Context, page, pageSize int) ([]models.AdminUser, int, error) {
	if m.listUsersFn != nil {
		return m.listUsersFn(ctx, page, pageSize)
	}
	return nil, 0, nil
}

func (m *mockRepository) GetUser(ctx context.Context, userID uuid.UUID) (*models.AdminUser, error) {
	if m.getUserFn != nil {
		return m.getUserFn(ctx, userID)
	}
	return nil, nil
}

func (m *mockRepository) CreateUser(ctx context.Context, id uuid.UUID, username string, email, passwordHash *string, platformRole string, memberships []models.MembershipInput, now time.Time) (*models.AdminUser, error) {
	if m.createUserFn != nil {
		return m.createUserFn(ctx, id, username, email, passwordHash, platformRole, memberships, now)
	}
	return nil, nil
}

func (m *mockRepository) CreateFirstUser(ctx context.Context, id uuid.UUID, username, passwordHash string, now time.Time, untilSuperadmin bool) (*models.AdminUser, error) {
	m.untilSuperadmin = untilSuperadmin
	if m.createFirstUserFn != nil {
		return m.createFirstUserFn(ctx, id, username, passwordHash, now)
	}
	return nil, nil
}

func (m *mockRepository) UpdateUser(ctx context.Context, userID uuid.UUID, username, email, passwordHash, platformRole *string, memberships *[]models.MembershipInput, now time.Time) (*models.AdminUser, error) {
	if m.updateUserFn != nil {
		return m.updateUserFn(ctx, userID, username, email, passwordHash, platformRole, memberships, now)
	}
	return nil, nil
}

func (m *mockRepository) RevokeSessionsByAdminID(ctx context.Context, adminID, keep uuid.UUID) error {
	if m.revokeSessionsByAdminIDFn != nil {
		return m.revokeSessionsByAdminIDFn(ctx, adminID)
	}
	return nil
}

func (m *mockRepository) BootstrapClosed(ctx context.Context, untilSuperadmin bool) (bool, error) {
	m.untilSuperadmin = untilSuperadmin
	if m.bootstrapClosedFn != nil {
		return m.bootstrapClosedFn(ctx)
	}
	return false, nil
}

func (m *mockRepository) DeleteUser(ctx context.Context, userID uuid.UUID) error {
	if m.deleteUserFn != nil {
		return m.deleteUserFn(ctx, userID)
	}
	return nil
}

func (m *mockRepository) AttachMemberships(ctx context.Context, users []models.AdminUser) error {
	return nil
}

func (m *mockRepository) AttachLockState(ctx context.Context, users []models.AdminUser) error {
	return nil
}

func strPtr(s string) *string { return &s }

func TestService_CreateUser_HashesPassword(t *testing.T) {
	var capturedHash *string
	var capturedRole string
	svc := &Service{
		repo: &mockRepository{
			createUserFn: func(ctx context.Context, id uuid.UUID, username string, email, passwordHash *string, platformRole string, memberships []models.MembershipInput, now time.Time) (*models.AdminUser, error) {
				capturedHash = passwordHash
				capturedRole = platformRole
				return &models.AdminUser{
					ID:        id,
					Username:  username,
					CreatedAt: now,
					UpdatedAt: now,
				}, nil
			},
		},
		bcryptCost: 4,
	}

	req := &models.CreateAdminUserRequest{
		Username: "  admin.user  ",
		Password: strPtr("super-secure-password"),
	}

	user, err := svc.CreateUser(context.Background(), req)
	if err != nil {
		t.Fatalf("CreateUser returned error: %v", err)
	}
	if user.Username != "admin.user" {
		t.Fatalf("expected trimmed username, got %q", user.Username)
	}
	if capturedRole != auth.PlatformRoleMember {
		t.Fatalf("expected default platform role member, got %q", capturedRole)
	}
	if capturedHash == nil || *capturedHash == "" {
		t.Fatal("expected password hash to be persisted")
	}
	if *capturedHash == *req.Password {
		t.Fatal("expected hashed password to differ from plaintext")
	}
	match, err := auth.ComparePassword(*capturedHash, *req.Password)
	if err != nil {
		t.Fatalf("ComparePassword returned error: %v", err)
	}
	if !match {
		t.Fatal("expected stored password hash to match original password")
	}
}

func TestService_CreateUser_OIDCOnlyHasNoHash(t *testing.T) {
	var capturedHash *string
	var capturedEmail *string
	svc := &Service{
		repo: &mockRepository{
			createUserFn: func(ctx context.Context, id uuid.UUID, username string, email, passwordHash *string, platformRole string, memberships []models.MembershipInput, now time.Time) (*models.AdminUser, error) {
				capturedHash = passwordHash
				capturedEmail = email
				return &models.AdminUser{ID: id, Username: username}, nil
			},
		},
		bcryptCost: 4,
	}

	_, err := svc.CreateUser(context.Background(), &models.CreateAdminUserRequest{
		Username: "sso.user",
		Email:    strPtr("  SSO.User@Example.com "),
	})
	if err != nil {
		t.Fatalf("CreateUser returned error: %v", err)
	}
	if capturedHash != nil {
		t.Fatal("expected nil password hash for OIDC-only user")
	}
	if capturedEmail == nil || *capturedEmail != "sso.user@example.com" {
		t.Fatalf("expected normalized email, got %v", capturedEmail)
	}
}

func TestService_CreateUser_InvalidRoles(t *testing.T) {
	svc := &Service{repo: &mockRepository{}, bcryptCost: 4}

	if _, err := svc.CreateUser(context.Background(), &models.CreateAdminUserRequest{
		Username:     "u1",
		PlatformRole: "root",
	}); err == nil {
		t.Fatal("expected error for invalid platform role")
	}

	if _, err := svc.CreateUser(context.Background(), &models.CreateAdminUserRequest{
		Username:    "u1",
		Memberships: []models.MembershipInput{{TenantID: uuid.New(), Role: "owner"}},
	}); err == nil {
		t.Fatal("expected error for invalid tenant role")
	}
}

func TestService_GetUser(t *testing.T) {
	targetID := uuid.New()
	now := time.Now()

	t.Run("success", func(t *testing.T) {
		svc := &Service{
			repo: &mockRepository{
				getUserFn: func(ctx context.Context, userID uuid.UUID) (*models.AdminUser, error) {
					if userID != targetID {
						t.Fatalf("unexpected userID: %s", userID)
					}
					return &models.AdminUser{
						ID:        userID,
						Username:  "admin",
						CreatedAt: now,
						UpdatedAt: now,
					}, nil
				},
			},
		}

		user, err := svc.GetUser(context.Background(), targetID)
		if err != nil {
			t.Fatalf("GetUser returned error: %v", err)
		}
		if user.ID != targetID {
			t.Fatalf("expected user ID %s, got %s", targetID, user.ID)
		}
	})

	t.Run("not found", func(t *testing.T) {
		svc := &Service{
			repo: &mockRepository{
				getUserFn: func(ctx context.Context, userID uuid.UUID) (*models.AdminUser, error) {
					return nil, sql.ErrNoRows
				},
			},
		}

		_, err := svc.GetUser(context.Background(), targetID)
		if !errors.Is(err, ErrUserNotFound) {
			t.Fatalf("expected ErrUserNotFound, got %v", err)
		}
	})
}

func TestService_CreateUser_DuplicateUsername(t *testing.T) {
	svc := &Service{
		repo: &mockRepository{
			createUserFn: func(ctx context.Context, id uuid.UUID, username string, email, passwordHash *string, platformRole string, memberships []models.MembershipInput, now time.Time) (*models.AdminUser, error) {
				return nil, &pq.Error{Code: "23505"}
			},
		},
		bcryptCost: 4,
	}

	_, err := svc.CreateUser(context.Background(), &models.CreateAdminUserRequest{
		Username: "admin",
		Password: strPtr("super-secure-password"),
	})
	if !errors.Is(err, ErrUsernameTaken) {
		t.Fatalf("expected ErrUsernameTaken, got %v", err)
	}
}

func TestService_CreateUser_DuplicateEmail(t *testing.T) {
	svc := &Service{
		repo: &mockRepository{
			createUserFn: func(ctx context.Context, id uuid.UUID, username string, email, passwordHash *string, platformRole string, memberships []models.MembershipInput, now time.Time) (*models.AdminUser, error) {
				return nil, &pq.Error{Code: "23505", Constraint: "idx_admin_users_email"}
			},
		},
		bcryptCost: 4,
	}

	_, err := svc.CreateUser(context.Background(), &models.CreateAdminUserRequest{
		Username: "admin2",
		Email:    strPtr("dup@example.com"),
		Password: strPtr("super-secure-password"),
	})
	if !errors.Is(err, ErrEmailTaken) {
		t.Fatalf("expected ErrEmailTaken, got %v", err)
	}
}

func TestService_HasActiveUsers(t *testing.T) {
	svc := &Service{
		repo: &mockRepository{
			bootstrapClosedFn: func(ctx context.Context) (bool, error) {
				return true, nil
			},
		},
	}

	hasUsers, err := svc.HasActiveUsers(context.Background())
	if err != nil {
		t.Fatalf("HasActiveUsers returned error: %v", err)
	}
	if !hasUsers {
		t.Fatal("expected active admin users to be present")
	}
}

func TestService_CreateFirstUser(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		var capturedHash string
		svc := &Service{
			repo: &mockRepository{
				createFirstUserFn: func(ctx context.Context, id uuid.UUID, username, passwordHash string, now time.Time) (*models.AdminUser, error) {
					capturedHash = passwordHash
					return &models.AdminUser{
						ID:        id,
						Username:  username,
						CreatedAt: now,
						UpdatedAt: now,
					}, nil
				},
			},
			bcryptCost: 4,
		}

		user, err := svc.CreateFirstUser(context.Background(), &models.CreateAdminUserRequest{
			Username: "  first-admin ",
			Password: strPtr("very-secure-password"),
		})
		if err != nil {
			t.Fatalf("CreateFirstUser returned error: %v", err)
		}
		if user.Username != "first-admin" {
			t.Fatalf("expected trimmed username, got %q", user.Username)
		}
		match, err := auth.ComparePassword(capturedHash, "very-secure-password")
		if err != nil {
			t.Fatalf("ComparePassword returned error: %v", err)
		}
		if !match {
			t.Fatal("expected stored password hash to match plaintext")
		}
	})

	t.Run("bootstrap closed", func(t *testing.T) {
		svc := &Service{
			repo: &mockRepository{
				createFirstUserFn: func(ctx context.Context, id uuid.UUID, username, passwordHash string, now time.Time) (*models.AdminUser, error) {
					return nil, sql.ErrNoRows
				},
			},
			bcryptCost: 4,
		}

		_, err := svc.CreateFirstUser(context.Background(), &models.CreateAdminUserRequest{
			Username: "admin",
			Password: strPtr("very-secure-password"),
		})
		if !errors.Is(err, ErrBootstrapClosed) {
			t.Fatalf("expected ErrBootstrapClosed, got %v", err)
		}
	})

	t.Run("password required", func(t *testing.T) {
		svc := &Service{repo: &mockRepository{}, bcryptCost: 4}
		_, err := svc.CreateFirstUser(context.Background(), &models.CreateAdminUserRequest{
			Username: "admin",
		})
		if err == nil {
			t.Fatal("expected error when bootstrapping without a password")
		}
	})
}

// With a setup code configured, bootstrap stays open until an active
// superadmin exists (an SSO member does not close it); without one, until any
// active user exists, as before. The SQL guards themselves are covered by the
// integration tests that run against Postgres (here and in oidcauth).
func TestService_BootstrapScopeFollowsSetupCode(t *testing.T) {
	for _, required := range []bool{false, true} {
		repo := &mockRepository{
			createFirstUserFn: func(ctx context.Context, id uuid.UUID, username, passwordHash string, now time.Time) (*models.AdminUser, error) {
				return &models.AdminUser{ID: id, Username: username}, nil
			},
		}
		svc := &Service{repo: repo, bcryptCost: 4}
		svc.ConfigureSetupCode(required)
		if _, err := svc.HasActiveUsers(context.Background()); err != nil {
			t.Fatal(err)
		}
		if repo.untilSuperadmin != required {
			t.Fatalf("setup code %v: status counted superadmins only = %v", required, repo.untilSuperadmin)
		}
		repo.untilSuperadmin = !required
		if _, err := svc.CreateFirstUser(context.Background(), &models.CreateAdminUserRequest{Username: "admin", Password: strPtr("very-secure-password")}); err != nil {
			t.Fatal(err)
		}
		if repo.untilSuperadmin != required {
			t.Fatalf("setup code %v: create guarded on superadmins only = %v", required, repo.untilSuperadmin)
		}
	}
}

func TestService_UpdateUser_PasswordResetRevokesSessions(t *testing.T) {
	targetID := uuid.New()
	revokeCount := 0

	svc := &Service{
		repo: &mockRepository{
			updateUserFn: func(ctx context.Context, userID uuid.UUID, username, email, passwordHash, platformRole *string, memberships *[]models.MembershipInput, now time.Time) (*models.AdminUser, error) {
				if userID != targetID {
					t.Fatalf("unexpected target user id: %s", userID)
				}
				if passwordHash == nil {
					t.Fatal("expected password hash to be provided")
				}
				if *passwordHash == "new-password-1234" {
					t.Fatal("expected password hash, got plaintext")
				}
				return &models.AdminUser{
					ID:        userID,
					Username:  "admin",
					CreatedAt: now.Add(-time.Hour),
					UpdatedAt: now,
				}, nil
			},
			revokeSessionsByAdminIDFn: func(ctx context.Context, adminID uuid.UUID) error {
				if adminID != targetID {
					t.Fatalf("unexpected revoke admin id: %s", adminID)
				}
				revokeCount++
				return nil
			},
		},
		bcryptCost: 4,
	}

	newPassword := "new-password-1234"
	_, err := svc.UpdateUser(context.Background(), targetID, &models.UpdateAdminUserRequest{
		Password: &newPassword,
	})
	if err != nil {
		t.Fatalf("UpdateUser returned error: %v", err)
	}
	if revokeCount != 1 {
		t.Fatalf("expected sessions to be revoked once, got %d", revokeCount)
	}
}

// The repository refuses the demotion inside its transaction; the service
// reports that refusal as is, not as a generic write failure.
func TestService_UpdateUser_DemoteLastSuperadminBlocked(t *testing.T) {
	targetID := uuid.New()
	member := "member"

	svc := &Service{
		repo: &mockRepository{
			updateUserFn: func(ctx context.Context, userID uuid.UUID, username, email, passwordHash, platformRole *string, memberships *[]models.MembershipInput, now time.Time) (*models.AdminUser, error) {
				return nil, ErrCannotDemoteLastAdmin
			},
		},
		bcryptCost: 4,
	}

	_, err := svc.UpdateUser(context.Background(), targetID, &models.UpdateAdminUserRequest{
		PlatformRole: &member,
	})
	if !errors.Is(err, ErrCannotDemoteLastAdmin) {
		t.Fatalf("expected ErrCannotDemoteLastAdmin, got %v", err)
	}
}

func TestService_UpdateUser_DuplicateUsername(t *testing.T) {
	targetID := uuid.New()
	newUsername := "duplicate-name"

	svc := &Service{
		repo: &mockRepository{
			updateUserFn: func(ctx context.Context, userID uuid.UUID, username, email, passwordHash, platformRole *string, memberships *[]models.MembershipInput, now time.Time) (*models.AdminUser, error) {
				return nil, &pq.Error{Code: "23505"}
			},
		},
		bcryptCost: 4,
	}

	_, err := svc.UpdateUser(context.Background(), targetID, &models.UpdateAdminUserRequest{
		Username: &newUsername,
	})
	if !errors.Is(err, ErrUsernameTaken) {
		t.Fatalf("expected ErrUsernameTaken, got %v", err)
	}
}

func TestService_DeleteUser_GuardrailsAndDelete(t *testing.T) {
	actorID := uuid.New()
	targetID := uuid.New()

	t.Run("reject self delete", func(t *testing.T) {
		svc := &Service{
			repo: &mockRepository{},
		}

		err := svc.DeleteUser(context.Background(), actorID, actorID)
		if !errors.Is(err, ErrCannotDeleteSelf) {
			t.Fatalf("expected ErrCannotDeleteSelf, got %v", err)
		}
	})

	t.Run("reject deleting last active superadmin", func(t *testing.T) {
		svc := &Service{
			repo: &mockRepository{
				deleteUserFn: func(ctx context.Context, userID uuid.UUID) error {
					return ErrCannotDeleteLastUser
				},
			},
		}

		err := svc.DeleteUser(context.Background(), actorID, targetID)
		if !errors.Is(err, ErrCannotDeleteLastUser) {
			t.Fatalf("expected ErrCannotDeleteLastUser, got %v", err)
		}
	})

	t.Run("delete existing user", func(t *testing.T) {
		users := map[uuid.UUID]bool{targetID: true}
		sessions := map[uuid.UUID]int{targetID: 3}
		svc := &Service{
			repo: &mockRepository{
				deleteUserFn: func(ctx context.Context, userID uuid.UUID) error {
					delete(users, userID)
					delete(sessions, userID)
					return nil
				},
			},
		}

		err := svc.DeleteUser(context.Background(), actorID, targetID)
		if err != nil {
			t.Fatalf("DeleteUser returned error: %v", err)
		}
		if users[targetID] {
			t.Fatal("expected hard delete to be executed")
		}
		if _, ok := sessions[targetID]; ok {
			t.Fatal("expected dependent sessions to be removed with hard delete")
		}
	})

	t.Run("delete missing user", func(t *testing.T) {
		svc := &Service{
			repo: &mockRepository{
				deleteUserFn: func(ctx context.Context, userID uuid.UUID) error {
					return sql.ErrNoRows
				},
			},
		}

		err := svc.DeleteUser(context.Background(), actorID, targetID)
		if !errors.Is(err, ErrUserNotFound) {
			t.Fatalf("expected ErrUserNotFound, got %v", err)
		}
	})
}
