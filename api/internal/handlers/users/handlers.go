package users

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	apierrors "github.com/yassinebenameur/probara/api/internal/errors"
	"github.com/yassinebenameur/probara/api/internal/httpx"
	"github.com/yassinebenameur/probara/api/internal/middleware"
	"github.com/yassinebenameur/probara/api/internal/models"
	"github.com/yassinebenameur/probara/api/internal/services/accountmail"
	adminusers "github.com/yassinebenameur/probara/api/internal/services/adminusers"
	"github.com/yassinebenameur/probara/api/internal/services/audit"
	"github.com/yassinebenameur/probara/shared/auth"
	"github.com/yassinebenameur/probara/shared/clientip"
	"github.com/yassinebenameur/probara/shared/config"
	"github.com/yassinebenameur/probara/shared/logger"
)

const (
	minUsernameLength = 3
	maxUsernameLength = 64
)

var usernameRegex = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

// Handlers handles admin users HTTP requests.
type Handlers struct {
	service userService
	logger  *logger.Logger
	audit   *audit.Recorder
	// setupCode is BOOTSTRAP_SETUP_CODE, normalized; empty leaves first-admin
	// creation open until an admin exists.
	setupCode string
	// security serves unlock and the user's sessions (security.go).
	security accountSecurity
	// links, mail and sso serve invites, reset links and the two-factor
	// reset (lifecycle.go).
	links accountLinks
	mail  *accountmail.Mailer
	sso   ssoProvider
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

// WithSetupCode makes first-admin creation require the install's one-time
// setup code (BOOTSTRAP_SETUP_CODE, as config.LoadAPIConfig normalized and
// length-checked it). Empty keeps it open, as before.
func (h *Handlers) WithSetupCode(code string) *Handlers {
	h.setupCode = config.NormalizeSetupCode(code)
	return h
}

// setupCodeMatches compares in constant time. Hashing first keeps the
// comparison independent of the submitted length as well.
func (h *Handlers) setupCodeMatches(submitted string) bool {
	want := sha256.Sum256([]byte(h.setupCode))
	got := sha256.Sum256([]byte(config.NormalizeSetupCode(submitted)))
	return subtle.ConstantTimeCompare(want[:], got[:]) == 1
}

// bootstrapFirstUserRequest is the first-admin body: the user fields plus the
// install's setup code when one is configured.
type bootstrapFirstUserRequest struct {
	models.CreateAdminUserRequest
	SetupCode string `json:"setup_code,omitempty"`
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
	event.MergeDetails(details)
	h.audit.Record(event)
}

// ListUsers handles GET /api/v1/users.
func (h *Handlers) ListUsers(w http.ResponseWriter, r *http.Request) {
	page, pageSize := httpx.Page(r)

	resp, err := h.service.ListUsers(r.Context(), page, pageSize)
	if err != nil {
		h.logger.WithError(err).Error("Failed to list admin users")
		apierrors.WriteInternalError(w, "failed to list users")
		return
	}

	httpx.WriteJSON(w, http.StatusOK, resp)
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

	httpx.WriteJSON(w, http.StatusOK, user)
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
		if errors.Is(err, auth.ErrPasswordTooLong) {
			// The validator already refuses it; never let bcrypt's limit be a 500.
			apierrors.WriteValidationError(w, auth.ErrPasswordTooLong.Error())
			return
		}
		if errors.Is(err, adminusers.ErrWorkspaceNotFound) {
			apierrors.WriteError(w, http.StatusUnprocessableEntity, "validation_error", err.Error())
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

	httpx.WriteJSON(w, http.StatusCreated, user)
}

// BootstrapStatus handles GET /api/v1/users/bootstrap/status.
func (h *Handlers) BootstrapStatus(w http.ResponseWriter, r *http.Request) {
	hasAdminUsers, err := h.service.HasActiveUsers(r.Context())
	if err != nil {
		h.logger.WithError(err).Error("Failed to check admin bootstrap status")
		apierrors.WriteInternalError(w, "failed to determine bootstrap status")
		return
	}

	httpx.WriteJSON(w, http.StatusOK, models.AdminBootstrapStatusResponse{
		HasAdminUsers:     hasAdminUsers,
		SetupCodeRequired: h.setupCode != "",
	})
}

// BootstrapFirstUser handles POST /api/v1/users/bootstrap/first.
func (h *Handlers) BootstrapFirstUser(w http.ResponseWriter, r *http.Request) {
	var body bootstrapFirstUserRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		apierrors.WriteValidationError(w, "invalid request body: "+err.Error())
		return
	}
	req := body.CreateAdminUserRequest

	// Checked before anything else, so a caller without the code learns
	// nothing (not even whether an admin exists). Missing and wrong codes get
	// the same answer; the log never carries the submitted value.
	if h.setupCode != "" && !h.setupCodeMatches(body.SetupCode) {
		h.logger.WithFields(map[string]any{
			"client_ip":       clientip.FromRequest(r),
			"setup_code_sent": strings.TrimSpace(body.SetupCode) != "",
		}).Warn("First-admin bootstrap refused: invalid or missing setup code")
		apierrors.WriteForbiddenError(w, "invalid or missing setup code")
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
		case errors.Is(err, adminusers.ErrUsernameTaken), errors.Is(err, auth.ErrPasswordTooLong):
			apierrors.WriteValidationError(w, err.Error())
			return
		default:
			h.logger.WithError(err).Error("Failed to bootstrap first admin user")
			apierrors.WriteInternalError(w, "failed to create first admin user")
			return
		}
	}

	httpx.WriteJSON(w, http.StatusCreated, user)
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
		case errors.Is(err, auth.ErrPasswordTooLong):
			apierrors.WriteValidationError(w, auth.ErrPasswordTooLong.Error())
			return
		case errors.Is(err, adminusers.ErrWorkspaceNotFound):
			apierrors.WriteError(w, http.StatusUnprocessableEntity, "validation_error", err.Error())
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
	if req.Password != nil && h.mail.Enabled() && user.Email != nil && *user.Email != "" {
		// The owner learns their password was set by someone else.
		to, username := *user.Email, user.Username
		h.mail.Background(h.logger, "password changed notice", func(ctx context.Context) error {
			return h.mail.PasswordChanged(ctx, to, username, accountmail.PasswordChangedByAdministrator)
		})
	}

	httpx.WriteJSON(w, http.StatusOK, user)
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

// validatePassword is the platform's one password rule (auth.ValidatePassword),
// shared with invites, resets and the account page.
func validatePassword(password string) error {
	return auth.ValidatePassword(password)
}
