package users

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	apierrors "github.com/yassinebenameur/probara/api/internal/errors"
	apimiddleware "github.com/yassinebenameur/probara/api/internal/middleware"
	"github.com/yassinebenameur/probara/api/internal/models"
	adminusers "github.com/yassinebenameur/probara/api/internal/services/adminusers"
	ctxpkg "github.com/yassinebenameur/probara/shared/context"
	"github.com/yassinebenameur/probara/shared/logger"
)

type mockUserService struct {
	listUsersFn       func(ctx context.Context, page, pageSize int) (*models.AdminUserListResponse, error)
	getUserFn         func(ctx context.Context, userID uuid.UUID) (*models.AdminUser, error)
	createUserFn      func(ctx context.Context, req *models.CreateAdminUserRequest) (*models.AdminUser, error)
	createFirstUserFn func(ctx context.Context, req *models.CreateAdminUserRequest) (*models.AdminUser, error)
	hasActiveUsersFn  func(ctx context.Context) (bool, error)
	updateUserFn      func(ctx context.Context, userID uuid.UUID, req *models.UpdateAdminUserRequest) (*models.AdminUser, error)
	deleteUserFn      func(ctx context.Context, actorUserID, targetUserID uuid.UUID) error
}

func (m *mockUserService) ListUsers(ctx context.Context, page, pageSize int) (*models.AdminUserListResponse, error) {
	return m.listUsersFn(ctx, page, pageSize)
}

func (m *mockUserService) GetUser(ctx context.Context, userID uuid.UUID) (*models.AdminUser, error) {
	return m.getUserFn(ctx, userID)
}

func (m *mockUserService) CreateUser(ctx context.Context, req *models.CreateAdminUserRequest) (*models.AdminUser, error) {
	return m.createUserFn(ctx, req)
}

func (m *mockUserService) CreateFirstUser(ctx context.Context, req *models.CreateAdminUserRequest) (*models.AdminUser, error) {
	return m.createFirstUserFn(ctx, req)
}

func (m *mockUserService) HasActiveUsers(ctx context.Context) (bool, error) {
	return m.hasActiveUsersFn(ctx)
}

func (m *mockUserService) UpdateUser(ctx context.Context, userID uuid.UUID, req *models.UpdateAdminUserRequest) (*models.AdminUser, error) {
	return m.updateUserFn(ctx, userID, req)
}

func (m *mockUserService) DeleteUser(ctx context.Context, actorUserID, targetUserID uuid.UUID) error {
	return m.deleteUserFn(ctx, actorUserID, targetUserID)
}

func TestHandlers_ListUsers_DefaultAndCustomPagination(t *testing.T) {
	log := logger.New("test", "debug")
	expected := []models.AdminUser{{ID: uuid.New(), Username: "admin"}}

	t.Run("default pagination", func(t *testing.T) {
		h := NewHandlers(&mockUserService{
			listUsersFn: func(ctx context.Context, page, pageSize int) (*models.AdminUserListResponse, error) {
				if page != 1 || pageSize != 20 {
					t.Fatalf("expected defaults page=1,page_size=20 got %d,%d", page, pageSize)
				}
				return &models.AdminUserListResponse{Items: expected, Page: page, PageSize: pageSize, Total: 1}, nil
			},
		}, log)

		req := httptest.NewRequest(http.MethodGet, "/api/v1/users", nil)
		w := httptest.NewRecorder()
		h.ListUsers(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", w.Code)
		}
	})

	t.Run("custom pagination", func(t *testing.T) {
		h := NewHandlers(&mockUserService{
			listUsersFn: func(ctx context.Context, page, pageSize int) (*models.AdminUserListResponse, error) {
				if page != 3 || pageSize != 50 {
					t.Fatalf("expected page=3,page_size=50 got %d,%d", page, pageSize)
				}
				return &models.AdminUserListResponse{Items: expected, Page: page, PageSize: pageSize, Total: 1}, nil
			},
		}, log)

		req := httptest.NewRequest(http.MethodGet, "/api/v1/users?page=3&page_size=50", nil)
		w := httptest.NewRecorder()
		h.ListUsers(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", w.Code)
		}
	})
}

func TestHandlers_CreateUser(t *testing.T) {
	log := logger.New("test", "debug")

	t.Run("success", func(t *testing.T) {
		h := NewHandlers(&mockUserService{
			createUserFn: func(ctx context.Context, req *models.CreateAdminUserRequest) (*models.AdminUser, error) {
				return &models.AdminUser{
					ID:        uuid.New(),
					Username:  req.Username,
					CreatedAt: time.Now(),
					UpdatedAt: time.Now(),
				}, nil
			},
		}, log)

		reqBody := []byte(`{"username":"admin.new","password":"super-secure-password"}`)
		req := httptest.NewRequest(http.MethodPost, "/api/v1/users", bytes.NewBuffer(reqBody))
		req.Header.Set("Content-Type", "application/json")

		w := httptest.NewRecorder()
		h.CreateUser(w, req)

		if w.Code != http.StatusCreated {
			t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
		}
	})

	t.Run("invalid short password", func(t *testing.T) {
		h := NewHandlers(&mockUserService{
			createUserFn: func(ctx context.Context, req *models.CreateAdminUserRequest) (*models.AdminUser, error) {
				t.Fatal("service should not be called on validation failure")
				return nil, nil
			},
		}, log)

		reqBody := []byte(`{"username":"admin.new","password":"short"}`)
		req := httptest.NewRequest(http.MethodPost, "/api/v1/users", bytes.NewBuffer(reqBody))
		w := httptest.NewRecorder()
		h.CreateUser(w, req)

		if w.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d", w.Code)
		}
	})

	t.Run("duplicate username", func(t *testing.T) {
		h := NewHandlers(&mockUserService{
			createUserFn: func(ctx context.Context, req *models.CreateAdminUserRequest) (*models.AdminUser, error) {
				return nil, adminusers.ErrUsernameTaken
			},
		}, log)

		reqBody := []byte(`{"username":"admin","password":"super-secure-password"}`)
		req := httptest.NewRequest(http.MethodPost, "/api/v1/users", bytes.NewBuffer(reqBody))
		w := httptest.NewRecorder()
		h.CreateUser(w, req)

		if w.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d", w.Code)
		}
	})
}

func TestHandlers_BootstrapStatus(t *testing.T) {
	log := logger.New("test", "debug")

	t.Run("success", func(t *testing.T) {
		h := NewHandlers(&mockUserService{
			hasActiveUsersFn: func(ctx context.Context) (bool, error) {
				return true, nil
			},
		}, log)

		req := httptest.NewRequest(http.MethodGet, "/api/v1/users/bootstrap/status", nil)
		w := httptest.NewRecorder()
		h.BootstrapStatus(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", w.Code)
		}

		var resp models.AdminBootstrapStatusResponse
		if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
			t.Fatalf("failed to decode response: %v", err)
		}
		if !resp.HasAdminUsers {
			t.Fatal("expected has_admin_users=true")
		}
	})
}

func TestHandlers_BootstrapFirstUser(t *testing.T) {
	log := logger.New("test", "debug")

	t.Run("success", func(t *testing.T) {
		h := NewHandlers(&mockUserService{
			createFirstUserFn: func(ctx context.Context, req *models.CreateAdminUserRequest) (*models.AdminUser, error) {
				return &models.AdminUser{
					ID:        uuid.New(),
					Username:  req.Username,
					CreatedAt: time.Now(),
					UpdatedAt: time.Now(),
				}, nil
			},
		}, log)

		reqBody := []byte(`{"username":"admin.new","password":"super-secure-password"}`)
		req := httptest.NewRequest(http.MethodPost, "/api/v1/users/bootstrap/first", bytes.NewBuffer(reqBody))
		w := httptest.NewRecorder()
		h.BootstrapFirstUser(w, req)

		if w.Code != http.StatusCreated {
			t.Fatalf("expected 201, got %d", w.Code)
		}
	})

	t.Run("bootstrap closed", func(t *testing.T) {
		h := NewHandlers(&mockUserService{
			createFirstUserFn: func(ctx context.Context, req *models.CreateAdminUserRequest) (*models.AdminUser, error) {
				return nil, adminusers.ErrBootstrapClosed
			},
		}, log)

		reqBody := []byte(`{"username":"admin.new","password":"super-secure-password"}`)
		req := httptest.NewRequest(http.MethodPost, "/api/v1/users/bootstrap/first", bytes.NewBuffer(reqBody))
		w := httptest.NewRecorder()
		h.BootstrapFirstUser(w, req)

		if w.Code != http.StatusConflict {
			t.Fatalf("expected 409, got %d", w.Code)
		}
	})
}

func TestHandlers_GetUser(t *testing.T) {
	log := logger.New("test", "debug")
	userID := uuid.New()

	buildRouter := func(h *Handlers) *chi.Mux {
		r := chi.NewRouter()
		r.Get("/{id}", h.GetUser)
		return r
	}

	t.Run("success", func(t *testing.T) {
		h := NewHandlers(&mockUserService{
			getUserFn: func(ctx context.Context, id uuid.UUID) (*models.AdminUser, error) {
				if id != userID {
					t.Fatalf("unexpected id: %s", id)
				}
				return &models.AdminUser{
					ID:        id,
					Username:  "admin",
					CreatedAt: time.Now(),
					UpdatedAt: time.Now(),
				}, nil
			},
		}, log)

		req := httptest.NewRequest(http.MethodGet, "/"+userID.String(), nil)
		w := httptest.NewRecorder()
		buildRouter(h).ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", w.Code)
		}
	})

	t.Run("not found", func(t *testing.T) {
		h := NewHandlers(&mockUserService{
			getUserFn: func(ctx context.Context, id uuid.UUID) (*models.AdminUser, error) {
				return nil, adminusers.ErrUserNotFound
			},
		}, log)

		req := httptest.NewRequest(http.MethodGet, "/"+userID.String(), nil)
		w := httptest.NewRecorder()
		buildRouter(h).ServeHTTP(w, req)

		if w.Code != http.StatusNotFound {
			t.Fatalf("expected 404, got %d", w.Code)
		}
	})
}

func TestHandlers_UpdateUser(t *testing.T) {
	log := logger.New("test", "debug")
	userID := uuid.New()

	buildRouter := func(h *Handlers) *chi.Mux {
		r := chi.NewRouter()
		r.Patch("/{id}", h.UpdateUser)
		return r
	}

	t.Run("username only", func(t *testing.T) {
		h := NewHandlers(&mockUserService{
			updateUserFn: func(ctx context.Context, id uuid.UUID, req *models.UpdateAdminUserRequest) (*models.AdminUser, error) {
				if id != userID {
					t.Fatalf("unexpected id: %s", id)
				}
				if req.Username == nil || *req.Username != "updated.user" {
					t.Fatalf("unexpected username payload: %+v", req.Username)
				}
				return &models.AdminUser{ID: id, Username: *req.Username, CreatedAt: time.Now(), UpdatedAt: time.Now()}, nil
			},
		}, log)

		req := httptest.NewRequest(http.MethodPatch, "/"+userID.String(), bytes.NewBufferString(`{"username":"updated.user"}`))
		w := httptest.NewRecorder()
		buildRouter(h).ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", w.Code)
		}
	})

	t.Run("password only", func(t *testing.T) {
		h := NewHandlers(&mockUserService{
			updateUserFn: func(ctx context.Context, id uuid.UUID, req *models.UpdateAdminUserRequest) (*models.AdminUser, error) {
				if req.Password == nil || *req.Password == "" {
					t.Fatal("expected password payload")
				}
				return &models.AdminUser{ID: id, Username: "admin", CreatedAt: time.Now(), UpdatedAt: time.Now()}, nil
			},
		}, log)

		req := httptest.NewRequest(http.MethodPatch, "/"+userID.String(), bytes.NewBufferString(`{"password":"new-password-1234"}`))
		w := httptest.NewRecorder()
		buildRouter(h).ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", w.Code)
		}
	})

	t.Run("both fields", func(t *testing.T) {
		h := NewHandlers(&mockUserService{
			updateUserFn: func(ctx context.Context, id uuid.UUID, req *models.UpdateAdminUserRequest) (*models.AdminUser, error) {
				if req.Username == nil || req.Password == nil {
					t.Fatal("expected both username and password")
				}
				return &models.AdminUser{ID: id, Username: *req.Username, CreatedAt: time.Now(), UpdatedAt: time.Now()}, nil
			},
		}, log)

		req := httptest.NewRequest(http.MethodPatch, "/"+userID.String(), bytes.NewBufferString(`{"username":"ops-admin","password":"another-password-1234"}`))
		w := httptest.NewRecorder()
		buildRouter(h).ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", w.Code)
		}
	})

	t.Run("empty patch", func(t *testing.T) {
		h := NewHandlers(&mockUserService{
			updateUserFn: func(ctx context.Context, id uuid.UUID, req *models.UpdateAdminUserRequest) (*models.AdminUser, error) {
				t.Fatal("service should not be called on validation failure")
				return nil, nil
			},
		}, log)

		req := httptest.NewRequest(http.MethodPatch, "/"+userID.String(), bytes.NewBufferString(`{}`))
		w := httptest.NewRecorder()
		buildRouter(h).ServeHTTP(w, req)

		if w.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d", w.Code)
		}
	})
}

func TestHandlers_DeleteUser(t *testing.T) {
	log := logger.New("test", "debug")
	actorID := uuid.New()
	targetID := uuid.New()

	buildRouter := func(h *Handlers) *chi.Mux {
		r := chi.NewRouter()
		r.Delete("/{id}", h.DeleteUser)
		return r
	}

	t.Run("success", func(t *testing.T) {
		h := NewHandlers(&mockUserService{
			deleteUserFn: func(ctx context.Context, actorUserID, targetUserID uuid.UUID) error {
				if actorUserID != actorID || targetUserID != targetID {
					t.Fatalf("unexpected IDs actor=%s target=%s", actorUserID, targetUserID)
				}
				return nil
			},
		}, log)

		req := httptest.NewRequest(http.MethodDelete, "/"+targetID.String(), nil)
		req = req.WithContext(ctxpkg.WithAdminID(req.Context(), actorID.String()))
		w := httptest.NewRecorder()
		buildRouter(h).ServeHTTP(w, req)

		if w.Code != http.StatusNoContent {
			t.Fatalf("expected 204, got %d", w.Code)
		}
	})

	t.Run("self delete blocked", func(t *testing.T) {
		h := NewHandlers(&mockUserService{
			deleteUserFn: func(ctx context.Context, actorUserID, targetUserID uuid.UUID) error {
				return adminusers.ErrCannotDeleteSelf
			},
		}, log)

		req := httptest.NewRequest(http.MethodDelete, "/"+targetID.String(), nil)
		req = req.WithContext(ctxpkg.WithAdminID(req.Context(), actorID.String()))
		w := httptest.NewRecorder()
		buildRouter(h).ServeHTTP(w, req)

		if w.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d", w.Code)
		}
	})

	t.Run("last admin blocked", func(t *testing.T) {
		h := NewHandlers(&mockUserService{
			deleteUserFn: func(ctx context.Context, actorUserID, targetUserID uuid.UUID) error {
				return adminusers.ErrCannotDeleteLastUser
			},
		}, log)

		req := httptest.NewRequest(http.MethodDelete, "/"+targetID.String(), nil)
		req = req.WithContext(ctxpkg.WithAdminID(req.Context(), actorID.String()))
		w := httptest.NewRecorder()
		buildRouter(h).ServeHTTP(w, req)

		if w.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d", w.Code)
		}
	})
}

func TestHandlers_UnauthorizedWhenRequireSuperadminApplied(t *testing.T) {
	log := logger.New("test", "debug")
	h := NewHandlers(&mockUserService{
		listUsersFn: func(ctx context.Context, page, pageSize int) (*models.AdminUserListResponse, error) {
			t.Fatal("service should not be called when unauthorized")
			return nil, nil
		},
	}, log)

	r := chi.NewRouter()
	r.Use(apimiddleware.RequireSuperadmin)
	r.Get("/users", h.ListUsers)

	t.Run("api key gets 401", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/users", nil)
		req = req.WithContext(ctxpkg.WithTenantID(req.Context(), uuid.New().String()))

		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if w.Code != http.StatusUnauthorized {
			t.Fatalf("expected 401, got %d", w.Code)
		}

		var errResp apierrors.ErrorResponse
		if err := json.NewDecoder(w.Body).Decode(&errResp); err != nil {
			t.Fatalf("failed to decode error response: %v", err)
		}
	})

	t.Run("member admin gets 403", func(t *testing.T) {
		ctx := ctxpkg.WithAdminID(context.Background(), uuid.New().String())
		ctx = ctxpkg.WithPlatformRole(ctx, "member")
		req := httptest.NewRequest(http.MethodGet, "/users", nil).WithContext(ctx)

		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if w.Code != http.StatusForbidden {
			t.Fatalf("expected 403, got %d", w.Code)
		}
	})

	t.Run("superadmin passes", func(t *testing.T) {
		called := false
		hOK := NewHandlers(&mockUserService{
			listUsersFn: func(ctx context.Context, page, pageSize int) (*models.AdminUserListResponse, error) {
				called = true
				return &models.AdminUserListResponse{Items: []models.AdminUser{}}, nil
			},
		}, log)
		rOK := chi.NewRouter()
		rOK.Use(apimiddleware.RequireSuperadmin)
		rOK.Get("/users", hOK.ListUsers)

		ctx := ctxpkg.WithAdminID(context.Background(), uuid.New().String())
		ctx = ctxpkg.WithPlatformRole(ctx, "superadmin")
		req := httptest.NewRequest(http.MethodGet, "/users", nil).WithContext(ctx)

		w := httptest.NewRecorder()
		rOK.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", w.Code)
		}
		if !called {
			t.Fatal("expected service to be called for superadmin")
		}
	})
}
