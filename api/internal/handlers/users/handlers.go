package users

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	apierrors "github.com/yassinebenameur/probara/api/internal/errors"
	"github.com/yassinebenameur/probara/api/internal/middleware"
	"github.com/yassinebenameur/probara/api/internal/models"
	adminusers "github.com/yassinebenameur/probara/api/internal/services/adminusers"
	"github.com/yassinebenameur/probara/shared/logger"
)

const (
	minUsernameLength = 3
	maxUsernameLength = 64
	minPasswordLength = 12
	maxPasswordLength = 128
)

var usernameRegex = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

// Handlers handles admin users HTTP requests.
type Handlers struct {
	service userService
	logger  *logger.Logger
}

type userService interface {
	ListUsers(ctx context.Context, page, pageSize int) (*models.AdminUserListResponse, error)
	GetUser(ctx context.Context, userID uuid.UUID) (*models.AdminUser, error)
	CreateUser(ctx context.Context, req *models.CreateAdminUserRequest) (*models.AdminUser, error)
	CreateFirstUser(ctx context.Context, req *models.CreateAdminUserRequest) (*models.AdminUser, error)
	HasActiveUsers(ctx context.Context) (bool, error)
	UpdateUser(ctx context.Context, userID uuid.UUID, req *models.UpdateAdminUserRequest) (*models.AdminUser, error)
	DeleteUser(ctx context.Context, actorUserID, targetUserID uuid.UUID) error
}

// NewHandlers creates a new users handler.
func NewHandlers(service userService, log *logger.Logger) *Handlers {
	return &Handlers{
		service: service,
		logger:  log,
	}
}

// ListUsers handles GET /api/v1/users.
func (h *Handlers) ListUsers(w http.ResponseWriter, r *http.Request) {
	page := 1
	if pageStr := r.URL.Query().Get("page"); pageStr != "" {
		if p, err := strconv.Atoi(pageStr); err == nil && p > 0 {
			page = p
		}
	}

	pageSize := 20
	if pageSizeStr := r.URL.Query().Get("page_size"); pageSizeStr != "" {
		if ps, err := strconv.Atoi(pageSizeStr); err == nil && ps > 0 && ps <= 100 {
			pageSize = ps
		}
	}

	resp, err := h.service.ListUsers(r.Context(), page, pageSize)
	if err != nil {
		h.logger.WithError(err).Error("Failed to list admin users")
		apierrors.WriteInternalError(w, "failed to list users")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// GetUser handles GET /api/v1/users/{id}.
func (h *Handlers) GetUser(w http.ResponseWriter, r *http.Request) {
	userIDStr := chi.URLParam(r, "id")
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		apierrors.WriteValidationError(w, "invalid user ID")
		return
	}

	user, err := h.service.GetUser(r.Context(), userID)
	if err != nil {
		if errors.Is(err, adminusers.ErrUserNotFound) {
			apierrors.WriteNotFoundError(w, "user not found")
			return
		}
		h.logger.WithFields(map[string]interface{}{
			"error":   err.Error(),
			"user_id": userID.String(),
		}).Error("Failed to get admin user")
		apierrors.WriteInternalError(w, "failed to get user")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(user)
}

// CreateUser handles POST /api/v1/users.
func (h *Handlers) CreateUser(w http.ResponseWriter, r *http.Request) {
	var req models.CreateAdminUserRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		apierrors.WriteValidationError(w, "invalid request body: "+err.Error())
		return
	}

	if err := validateCreateRequest(&req); err != nil {
		apierrors.WriteValidationError(w, err.Error())
		return
	}

	user, err := h.service.CreateUser(r.Context(), &req)
	if err != nil {
		if errors.Is(err, adminusers.ErrUsernameTaken) {
			apierrors.WriteValidationError(w, err.Error())
			return
		}
		h.logger.WithError(err).Error("Failed to create admin user")
		apierrors.WriteInternalError(w, "failed to create user")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(user)
}

// BootstrapStatus handles GET /api/v1/users/bootstrap/status.
func (h *Handlers) BootstrapStatus(w http.ResponseWriter, r *http.Request) {
	hasAdminUsers, err := h.service.HasActiveUsers(r.Context())
	if err != nil {
		h.logger.WithError(err).Error("Failed to check admin bootstrap status")
		apierrors.WriteInternalError(w, "failed to determine bootstrap status")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(models.AdminBootstrapStatusResponse{
		HasAdminUsers: hasAdminUsers,
	})
}

// BootstrapFirstUser handles POST /api/v1/users/bootstrap/first.
func (h *Handlers) BootstrapFirstUser(w http.ResponseWriter, r *http.Request) {
	var req models.CreateAdminUserRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		apierrors.WriteValidationError(w, "invalid request body: "+err.Error())
		return
	}

	if err := validateCreateRequest(&req); err != nil {
		apierrors.WriteValidationError(w, err.Error())
		return
	}

	user, err := h.service.CreateFirstUser(r.Context(), &req)
	if err != nil {
		switch {
		case errors.Is(err, adminusers.ErrBootstrapClosed):
			apierrors.WriteError(w, http.StatusConflict, "conflict", err.Error())
			return
		case errors.Is(err, adminusers.ErrUsernameTaken):
			apierrors.WriteValidationError(w, err.Error())
			return
		default:
			h.logger.WithError(err).Error("Failed to bootstrap first admin user")
			apierrors.WriteInternalError(w, "failed to create first admin user")
			return
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(user)
}

// UpdateUser handles PATCH /api/v1/users/{id}.
func (h *Handlers) UpdateUser(w http.ResponseWriter, r *http.Request) {
	userIDStr := chi.URLParam(r, "id")
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		apierrors.WriteValidationError(w, "invalid user ID")
		return
	}

	var req models.UpdateAdminUserRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		apierrors.WriteValidationError(w, "invalid request body: "+err.Error())
		return
	}

	if err := validateUpdateRequest(&req); err != nil {
		apierrors.WriteValidationError(w, err.Error())
		return
	}

	user, err := h.service.UpdateUser(r.Context(), userID, &req)
	if err != nil {
		switch {
		case errors.Is(err, adminusers.ErrUserNotFound):
			apierrors.WriteNotFoundError(w, "user not found")
			return
		case errors.Is(err, adminusers.ErrUsernameTaken):
			apierrors.WriteValidationError(w, err.Error())
			return
		default:
			h.logger.WithFields(map[string]interface{}{
				"error":   err.Error(),
				"user_id": userID.String(),
			}).Error("Failed to update admin user")
			apierrors.WriteInternalError(w, "failed to update user")
			return
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(user)
}

// DeleteUser handles DELETE /api/v1/users/{id}.
func (h *Handlers) DeleteUser(w http.ResponseWriter, r *http.Request) {
	userIDStr := chi.URLParam(r, "id")
	targetUserID, err := uuid.Parse(userIDStr)
	if err != nil {
		apierrors.WriteValidationError(w, "invalid user ID")
		return
	}

	actorAdminID, err := middleware.GetAdminID(r.Context())
	if err != nil {
		apierrors.WriteUnauthorizedError(w, "admin access required")
		return
	}

	actorUserID, err := uuid.Parse(actorAdminID)
	if err != nil {
		apierrors.WriteUnauthorizedError(w, "admin access required")
		return
	}

	err = h.service.DeleteUser(r.Context(), actorUserID, targetUserID)
	if err != nil {
		switch {
		case errors.Is(err, adminusers.ErrUserNotFound):
			apierrors.WriteNotFoundError(w, "user not found")
			return
		case errors.Is(err, adminusers.ErrCannotDeleteSelf), errors.Is(err, adminusers.ErrCannotDeleteLastUser):
			apierrors.WriteValidationError(w, err.Error())
			return
		default:
			h.logger.WithFields(map[string]interface{}{
				"error":            err.Error(),
				"target_admin_id":  targetUserID.String(),
				"request_admin_id": actorUserID.String(),
			}).Error("Failed to delete admin user")
			apierrors.WriteInternalError(w, "failed to delete user")
			return
		}
	}

	w.WriteHeader(http.StatusNoContent)
}

func validateCreateRequest(req *models.CreateAdminUserRequest) error {
	req.Username = strings.TrimSpace(req.Username)
	if err := validateUsername(req.Username); err != nil {
		return err
	}
	if err := validatePassword(req.Password); err != nil {
		return err
	}
	return nil
}

func validateUpdateRequest(req *models.UpdateAdminUserRequest) error {
	if req.Username == nil && req.Password == nil {
		return errors.New("at least one field (username or password) is required")
	}

	if req.Username != nil {
		trimmed := strings.TrimSpace(*req.Username)
		if err := validateUsername(trimmed); err != nil {
			return err
		}
		req.Username = &trimmed
	}

	if req.Password != nil {
		if err := validatePassword(*req.Password); err != nil {
			return err
		}
	}

	return nil
}

func validateUsername(username string) error {
	if username == "" {
		return errors.New("username is required")
	}
	if len(username) < minUsernameLength || len(username) > maxUsernameLength {
		return errors.New("username must be between 3 and 64 characters")
	}
	if !usernameRegex.MatchString(username) {
		return errors.New("username can only contain letters, numbers, dots, underscores, and hyphens")
	}
	return nil
}

func validatePassword(password string) error {
	if password == "" {
		return errors.New("password is required")
	}
	if len(password) < minPasswordLength || len(password) > maxPasswordLength {
		return errors.New("password must be between 12 and 128 characters")
	}
	return nil
}
