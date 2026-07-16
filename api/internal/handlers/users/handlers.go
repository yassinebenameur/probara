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
	"github.com/yassinebenameur/probara/api/internal/services/audit"
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
	audit   *audit.Recorder
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

// WithAudit attaches an audit recorder for explicit user-management events.
func (h *Handlers) WithAudit(recorder *audit.Recorder) *Handlers {
	h.audit = recorder
	return h
}

// recordUserEvent emits a rich user-management audit event (the /users
// subtree is excluded from the generic mutation middleware). The target
// user's identity goes in resource fields/details, not the actor fields.
func (h *Handlers) recordUserEvent(r *http.Request, action, outcome string, targetID string, details map[string]any) {
	if h.audit == nil {
		return
	}
	event := audit.FromRequest(r)
	event.Action = action
	event.Outcome = outcome
	event.ResourceType = "user"
	event.ResourceID = targetID
	event.Details = details
	h.audit.Record(event)
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
		if errors.Is(err, adminusers.ErrUsernameTaken) || errors.Is(err, adminusers.ErrEmailTaken) {
			apierrors.WriteValidationError(w, err.Error())
			return
		}
		h.logger.WithError(err).Error("Failed to create admin user")
		apierrors.WriteInternalError(w, "failed to create user")
		return
	}

	h.recordUserEvent(r, "user.create", audit.OutcomeSuccess, user.ID.String(), map[string]any{
		"username":      user.Username,
		"platform_role": user.PlatformRole,
		"auth_method":   user.AuthMethod,
		"memberships":   len(user.Memberships),
	})

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

	if err := validateBootstrapRequest(&req); err != nil {
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
		case errors.Is(err, adminusers.ErrUsernameTaken),
			errors.Is(err, adminusers.ErrEmailTaken),
			errors.Is(err, adminusers.ErrCannotDemoteLastAdmin):
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

	changed := make([]string, 0, 5)
	if req.Username != nil {
		changed = append(changed, "username")
	}
	if req.Email != nil {
		changed = append(changed, "email")
	}
	if req.Password != nil {
		changed = append(changed, "password")
	}
	if req.PlatformRole != nil {
		changed = append(changed, "platform_role")
	}
	if req.Memberships != nil {
		changed = append(changed, "memberships")
	}
	h.recordUserEvent(r, "user.update", audit.OutcomeSuccess, user.ID.String(), map[string]any{
		"username": user.Username,
		"changed":  changed,
	})

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

	h.recordUserEvent(r, "user.delete", audit.OutcomeSuccess, targetUserID.String(), nil)

	w.WriteHeader(http.StatusNoContent)
}

func validateCreateRequest(req *models.CreateAdminUserRequest) error {
	req.Username = strings.TrimSpace(req.Username)
	if err := validateUsername(req.Username); err != nil {
		return err
	}
	hasPassword := req.Password != nil && *req.Password != ""
	if hasPassword {
		if err := validatePassword(*req.Password); err != nil {
			return err
		}
	} else {
		// OIDC-only user: the IdP identity is linked by email on first login.
		if req.Email == nil || strings.TrimSpace(*req.Email) == "" {
			return errors.New("password is required unless an email is set for SSO-only sign-in")
		}
	}
	if req.Email != nil {
		if err := validateEmail(*req.Email); err != nil {
			return err
		}
	}
	return nil
}

// validateBootstrapRequest is stricter: the first admin always has a password.
func validateBootstrapRequest(req *models.CreateAdminUserRequest) error {
	req.Username = strings.TrimSpace(req.Username)
	if err := validateUsername(req.Username); err != nil {
		return err
	}
	if req.Password == nil {
		return errors.New("password is required")
	}
	return validatePassword(*req.Password)
}

func validateUpdateRequest(req *models.UpdateAdminUserRequest) error {
	if req.Username == nil && req.Password == nil && req.Email == nil && req.PlatformRole == nil && req.Memberships == nil {
		return errors.New("at least one field is required")
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

	if req.Email != nil {
		if err := validateEmail(*req.Email); err != nil {
			return err
		}
	}

	return nil
}

func validateEmail(email string) error {
	trimmed := strings.TrimSpace(email)
	if trimmed == "" {
		return nil
	}
	if len(trimmed) > 254 || !strings.Contains(trimmed, "@") {
		return errors.New("invalid email address")
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
