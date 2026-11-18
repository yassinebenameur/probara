// Package account serves the signed-in user's own account: their sessions,
// password, email address and second factor (security.go). Users are
// platform-scoped, so nothing here is tenant-scoped.
package account

import (
	"context"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	apierrors "github.com/yassinebenameur/probara/api/internal/errors"
	"github.com/yassinebenameur/probara/api/internal/httpx"
	"github.com/yassinebenameur/probara/api/internal/models"
	"github.com/yassinebenameur/probara/api/internal/services/accountmail"
	"github.com/yassinebenameur/probara/api/internal/services/adminauth"
	"github.com/yassinebenameur/probara/api/internal/services/audit"
	ctxpkg "github.com/yassinebenameur/probara/shared/context"
	"github.com/yassinebenameur/probara/shared/logger"
)

type sessionService interface {
	ListSessions(ctx context.Context, adminID, current uuid.UUID) ([]models.AdminSession, error)
	RevokeSession(ctx context.Context, adminID, familyID uuid.UUID, reason string) error
	RevokeAllSessions(ctx context.Context, adminID, keep uuid.UUID, reason string) (int, error)
}

// Handlers serves /api/v1/account.
type Handlers struct {
	sessions sessionService
	logger   *logger.Logger
	audit    *audit.Recorder
	// security and mail serve the password, email and two-factor routes
	// (security.go).
	security securityService
	mail     *accountmail.Mailer
}

// NewHandlers creates the account handlers.
func NewHandlers(sessions sessionService, log *logger.Logger, recorder *audit.Recorder) *Handlers {
	return &Handlers{sessions: sessions, logger: log, audit: recorder}
}

// sessionCaller admits a signed-in user's session cookie only: the routes
// act on "this device", which an API key or an MCP token does not have.
func sessionCaller(w http.ResponseWriter, r *http.Request) (adminID, sessionID uuid.UUID, ok bool) {
	ctx := r.Context()
	sid, hasSession := adminauth.SessionIDFromContext(ctx)
	raw, isAdmin := ctxpkg.GetAdminID(ctx)
	if !hasSession || !isAdmin {
		apierrors.WriteForbiddenError(w, "a signed-in user session is required")
		return uuid.Nil, uuid.Nil, false
	}
	id, err := uuid.Parse(raw)
	if err != nil {
		apierrors.WriteUnauthorizedError(w, "invalid session")
		return uuid.Nil, uuid.Nil, false
	}
	return id, sid, true
}

func (h *Handlers) record(r *http.Request, action string, userID uuid.UUID, resourceID string, details map[string]any) {
	if h.audit == nil {
		return
	}
	event := audit.FromRequest(r)
	event.Action = action
	event.Outcome = audit.OutcomeSuccess
	event.ActorType = audit.ActorAdminUser
	event.ActorID = &userID
	event.ResourceType = "session"
	event.ResourceID = resourceID
	event.MergeDetails(details)
	h.audit.Record(event)
}

// ListSessions handles GET /api/v1/account/sessions: the caller's live
// sessions, this one marked current.
func (h *Handlers) ListSessions(w http.ResponseWriter, r *http.Request) {
	userID, current, ok := sessionCaller(w, r)
	if !ok {
		return
	}
	sessions, err := h.sessions.ListSessions(r.Context(), userID, current)
	if err != nil {
		h.logger.WithError(err).Error("Failed to list account sessions")
		apierrors.WriteInternalError(w, "failed to list sessions")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, models.AdminSessionListResponse{Items: sessions})
}

// RevokeSession handles DELETE /api/v1/account/sessions/{id}: signs one of
// the caller's sessions out. Revoking the current one signs this device out.
func (h *Handlers) RevokeSession(w http.ResponseWriter, r *http.Request) {
	userID, _, ok := sessionCaller(w, r)
	if !ok {
		return
	}
	sessionID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		apierrors.WriteValidationError(w, "invalid session ID")
		return
	}
	if err := h.sessions.RevokeSession(r.Context(), userID, sessionID, adminauth.RevokeReasonUser); err != nil {
		if errors.Is(err, adminauth.ErrSessionNotFound) {
			apierrors.WriteNotFoundError(w, "session not found")
			return
		}
		h.logger.WithError(err).Error("Failed to revoke account session")
		apierrors.WriteInternalError(w, "failed to revoke session")
		return
	}
	h.record(r, "auth.session_revoke", userID, sessionID.String(), nil)
	w.WriteHeader(http.StatusNoContent)
}

// RevokeOtherSessions handles POST /api/v1/account/sessions/revoke-others:
// signs the caller out everywhere except this session.
func (h *Handlers) RevokeOtherSessions(w http.ResponseWriter, r *http.Request) {
	userID, current, ok := sessionCaller(w, r)
	if !ok {
		return
	}
	revoked, err := h.sessions.RevokeAllSessions(r.Context(), userID, current, adminauth.RevokeReasonUser)
	if err != nil {
		h.logger.WithError(err).Error("Failed to revoke other account sessions")
		apierrors.WriteInternalError(w, "failed to revoke sessions")
		return
	}
	h.record(r, "auth.sessions_revoke_others", userID, current.String(), map[string]any{"revoked": revoked})
	httpx.WriteJSON(w, http.StatusOK, models.RevokeSessionsResponse{Revoked: revoked})
}
