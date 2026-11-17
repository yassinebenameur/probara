package users

import (
	"context"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	apierrors "github.com/yassinebenameur/probara/api/internal/errors"
	"github.com/yassinebenameur/probara/api/internal/httpx"
	"github.com/yassinebenameur/probara/api/internal/models"
	"github.com/yassinebenameur/probara/api/internal/services/adminauth"
	adminusers "github.com/yassinebenameur/probara/api/internal/services/adminusers"
	"github.com/yassinebenameur/probara/api/internal/services/audit"
	ctxpkg "github.com/yassinebenameur/probara/shared/context"
)

// accountSecurity is the sign-in state a superadmin manages for other users:
// the lockout and the user's sessions (adminauth.Service).
type accountSecurity interface {
	UnlockUser(ctx context.Context, userID uuid.UUID) (bool, error)
	ListSessions(ctx context.Context, adminID, current uuid.UUID) ([]models.AdminSession, error)
	RevokeSession(ctx context.Context, adminID, familyID uuid.UUID, reason string) error
	RevokeAllSessions(ctx context.Context, adminID, keep uuid.UUID, reason string) (int, error)
}

// WithAccountSecurity attaches the lockout and session operations behind
// /users/{id}/unlock and /users/{id}/sessions.
func (h *Handlers) WithAccountSecurity(svc accountSecurity) *Handlers {
	h.security = svc
	return h
}

// targetUser parses {id} and answers 404 for an unknown user.
func (h *Handlers) targetUser(w http.ResponseWriter, r *http.Request) (*models.AdminUser, bool) {
	userID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		apierrors.WriteValidationError(w, "invalid user ID")
		return nil, false
	}
	user, err := h.service.GetUser(r.Context(), userID)
	if err != nil {
		if errors.Is(err, adminusers.ErrUserNotFound) {
			apierrors.WriteNotFoundError(w, "user not found")
			return nil, false
		}
		h.logger.WithError(err).Error("Failed to get admin user")
		apierrors.WriteInternalError(w, "failed to get user")
		return nil, false
	}
	return user, true
}

// ownSessionToKeep is the caller's current session when they act on their
// own account, so "revoke all" never signs out the device in use.
func ownSessionToKeep(r *http.Request, userID uuid.UUID) uuid.UUID {
	if actor, ok := ctxpkg.GetAdminID(r.Context()); ok && actor == userID.String() {
		if sid, ok := adminauth.SessionIDFromContext(r.Context()); ok {
			return sid
		}
	}
	return uuid.Nil
}

// UnlockUser handles POST /api/v1/users/{id}/unlock: lifts a sign-in lockout
// before it expires and clears the failure streak.
func (h *Handlers) UnlockUser(w http.ResponseWriter, r *http.Request) {
	user, ok := h.targetUser(w, r)
	if !ok {
		return
	}
	wasLocked, err := h.security.UnlockUser(r.Context(), user.ID)
	if err != nil {
		if errors.Is(err, adminauth.ErrUserNotFound) {
			apierrors.WriteNotFoundError(w, "user not found")
			return
		}
		h.logger.WithError(err).Error("Failed to unlock admin user")
		apierrors.WriteInternalError(w, "failed to unlock user")
		return
	}
	h.recordUserEvent(r, "user.unlock", audit.OutcomeSuccess, user.ID.String(), map[string]any{
		"username":   user.Username,
		"was_locked": wasLocked,
	})
	httpx.WriteJSON(w, http.StatusOK, models.UnlockUserResponse{WasLocked: wasLocked})
}

// ListUserSessions handles GET /api/v1/users/{id}/sessions.
func (h *Handlers) ListUserSessions(w http.ResponseWriter, r *http.Request) {
	user, ok := h.targetUser(w, r)
	if !ok {
		return
	}
	current, _ := adminauth.SessionIDFromContext(r.Context())
	sessions, err := h.security.ListSessions(r.Context(), user.ID, current)
	if err != nil {
		h.logger.WithError(err).Error("Failed to list user sessions")
		apierrors.WriteInternalError(w, "failed to list sessions")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, models.AdminSessionListResponse{Items: sessions})
}

// RevokeUserSession handles DELETE /api/v1/users/{id}/sessions/{sessionId}.
func (h *Handlers) RevokeUserSession(w http.ResponseWriter, r *http.Request) {
	user, ok := h.targetUser(w, r)
	if !ok {
		return
	}
	sessionID, err := uuid.Parse(chi.URLParam(r, "sessionId"))
	if err != nil {
		apierrors.WriteValidationError(w, "invalid session ID")
		return
	}
	if err := h.security.RevokeSession(r.Context(), user.ID, sessionID, adminauth.RevokeReasonAdmin); err != nil {
		if errors.Is(err, adminauth.ErrSessionNotFound) {
			apierrors.WriteNotFoundError(w, "session not found")
			return
		}
		h.logger.WithError(err).Error("Failed to revoke user session")
		apierrors.WriteInternalError(w, "failed to revoke session")
		return
	}
	h.recordUserEvent(r, "user.session_revoke", audit.OutcomeSuccess, user.ID.String(), map[string]any{
		"username":   user.Username,
		"session_id": sessionID.String(),
	})
	w.WriteHeader(http.StatusNoContent)
}

// RevokeUserSessions handles DELETE /api/v1/users/{id}/sessions: signs the
// user out everywhere (on your own account, everywhere but here).
func (h *Handlers) RevokeUserSessions(w http.ResponseWriter, r *http.Request) {
	user, ok := h.targetUser(w, r)
	if !ok {
		return
	}
	revoked, err := h.security.RevokeAllSessions(r.Context(), user.ID, ownSessionToKeep(r, user.ID), adminauth.RevokeReasonAdmin)
	if err != nil {
		h.logger.WithError(err).Error("Failed to revoke user sessions")
		apierrors.WriteInternalError(w, "failed to revoke sessions")
		return
	}
	h.recordUserEvent(r, "user.sessions_revoke", audit.OutcomeSuccess, user.ID.String(), map[string]any{
		"username": user.Username,
		"revoked":  revoked,
	})
	httpx.WriteJSON(w, http.StatusOK, models.RevokeSessionsResponse{Revoked: revoked})
}
