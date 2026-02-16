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
	createUserFn              func(ctx context.Context, id uuid.UUID, username, passwordHash string, now time.Time) (*models.AdminUser, error)
	createFirstUserFn         func(ctx context.Context, id uuid.UUID, username, passwordHash string, now time.Time) (*models.AdminUser, error)
	updateUserFn              func(ctx context.Context, userID uuid.UUID, username, passwordHash *string, now time.Time) (*models.AdminUser, error)
	revokeSessionsByAdminIDFn func(ctx context.Context, adminID uuid.UUID) error
	countActiveAdminsFn       func(ctx context.Context) (int, error)
	countOtherAdminsFn        func(ctx context.Context, excludeUserID uuid.UUID) (int, error)
	deleteUserFn              func(ctx context.Context, userID uuid.UUID) error
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

func (m *mockRepository) CreateUser(ctx context.Context, id uuid.UUID, username, passwordHash string, now time.Time) (*models.AdminUser, error) {
	if m.createUserFn != nil {
		return m.createUserFn(ctx, id, username, passwordHash, now)
	}
	return nil, nil
}

func (m *mockRepository) CreateFirstUser(ctx context.Context, id uuid.UUID, username, passwordHash string, now time.Time) (*models.AdminUser, error) {
	if m.createFirstUserFn != nil {
		return m.createFirstUserFn(ctx, id, username, passwordHash, now)
	}
	return nil, nil
}

func (m *mockRepository) UpdateUser(ctx context.Context, userID uuid.UUID, username, passwordHash *string, now time.Time) (*models.AdminUser, error) {
	if m.updateUserFn != nil {
		return m.updateUserFn(ctx, userID, username, passwordHash, now)
	}
	return nil, nil
}

func (m *mockRepository) RevokeSessionsByAdminID(ctx context.Context, adminID uuid.UUID) error {
	if m.revokeSessionsByAdminIDFn != nil {
		return m.revokeSessionsByAdminIDFn(ctx, adminID)
	}
	return nil
}

func (m *mockRepository) CountActiveAdmins(ctx context.Context) (int, error) {
	if m.countActiveAdminsFn != nil {
		return m.countActiveAdminsFn(ctx)
	}
	return 0, nil
}

func (m *mockRepository) CountOtherActiveAdmins(ctx context.Context, excludeUserID uuid.UUID) (int, error) {
	if m.countOtherAdminsFn != nil {
		return m.countOtherAdminsFn(ctx, excludeUserID)
	}
	return 0, nil
}

func (m *mockRepository) DeleteUser(ctx context.Context, userID uuid.UUID) error {
	if m.deleteUserFn != nil {
		return m.deleteUserFn(ctx, userID)
	}
	return nil
}

func TestService_CreateUser_HashesPassword(t *testing.T) {
	var capturedHash string
	svc := &Service{
		repo: &mockRepository{
			createUserFn: func(ctx context.Context, id uuid.UUID, username, passwordHash string, now time.Time) (*models.AdminUser, error) {
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

	req := &models.CreateAdminUserRequest{
		Username: "  admin.user  ",
		Password: "super-secure-password",
	}

	user, err := svc.CreateUser(context.Background(), req)
	if err != nil {
		t.Fatalf("CreateUser returned error: %v", err)
	}
	if user.Username != "admin.user" {
		t.Fatalf("expected trimmed username, got %q", user.Username)
	}
	if capturedHash == "" {
		t.Fatal("expected password hash to be persisted")
	}
	if capturedHash == req.Password {
		t.Fatal("expected hashed password to differ from plaintext")
	}
	match, err := auth.ComparePassword(capturedHash, req.Password)
	if err != nil {
		t.Fatalf("ComparePassword returned error: %v", err)
	}
	if !match {
		t.Fatal("expected stored password hash to match original password")
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
			createUserFn: func(ctx context.Context, id uuid.UUID, username, passwordHash string, now time.Time) (*models.AdminUser, error) {
				return nil, &pq.Error{Code: "23505"}
			},
		},
		bcryptCost: 4,
	}

	_, err := svc.CreateUser(context.Background(), &models.CreateAdminUserRequest{
		Username: "admin",
		Password: "super-secure-password",
	})
	if !errors.Is(err, ErrUsernameTaken) {
		t.Fatalf("expected ErrUsernameTaken, got %v", err)
	}
}

func TestService_HasActiveUsers(t *testing.T) {
	svc := &Service{
		repo: &mockRepository{
			countActiveAdminsFn: func(ctx context.Context) (int, error) {
				return 1, nil
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
			Password: "very-secure-password",
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
			Password: "very-secure-password",
		})
		if !errors.Is(err, ErrBootstrapClosed) {
			t.Fatalf("expected ErrBootstrapClosed, got %v", err)
		}
	})
}

func TestService_UpdateUser_PasswordResetRevokesSessions(t *testing.T) {
	targetID := uuid.New()
	revokeCount := 0

	svc := &Service{
		repo: &mockRepository{
			updateUserFn: func(ctx context.Context, userID uuid.UUID, username, passwordHash *string, now time.Time) (*models.AdminUser, error) {
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

func TestService_UpdateUser_DuplicateUsername(t *testing.T) {
	targetID := uuid.New()
	newUsername := "duplicate-name"

	svc := &Service{
		repo: &mockRepository{
			updateUserFn: func(ctx context.Context, userID uuid.UUID, username, passwordHash *string, now time.Time) (*models.AdminUser, error) {
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

	t.Run("reject deleting last active admin", func(t *testing.T) {
		svc := &Service{
			repo: &mockRepository{
				countOtherAdminsFn: func(ctx context.Context, excludeUserID uuid.UUID) (int, error) {
					return 0, nil
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
				countOtherAdminsFn: func(ctx context.Context, excludeUserID uuid.UUID) (int, error) {
					return 2, nil
				},
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
				countOtherAdminsFn: func(ctx context.Context, excludeUserID uuid.UUID) (int, error) {
					return 1, nil
				},
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
