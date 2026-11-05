package auth

import (
	"encoding/json"
	stderrors "errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/api/internal/errors"
	"github.com/yassinebenameur/probara/api/internal/middleware"
	"github.com/yassinebenameur/probara/api/internal/models"
	"github.com/yassinebenameur/probara/api/internal/services/adminauth"
	"github.com/yassinebenameur/probara/api/internal/services/audit"
	ctxpkg "github.com/yassinebenameur/probara/shared/context"
)

// Session management. /account/* is the signed-in user's own sessions (a
// browser session only: no API key, no MCP token, outside the write gate so
// a viewer can end their own sessions). /users/{id}/unlock and
// /users/{id}/sessions are superadmin actions under the /users guard. All
// of them sit outside generic audit and record their own events.

// sessionUser returns the signed-in user of a cookie-session request, and
// the session family it used (uuid.Nil for a token minted before families).
func sessionUser(w http.ResponseWriter, r *http.Request) (adminID, current uuid.UUID, ok bool) {
	ctx := r.Context()
	actorType, _ := ctxpkg.GetActorType(ctx)
	_, scoped := ctxpkg.GetAuthScope(ctx)
	via, _ := ctxpkg.GetVia(ctx)
	if actorType != ctxpkg.ActorTypeAdminUser || scoped || via == ctxpkg.ViaMCP {
		errors.WriteForbiddenError(w, "signed-in user session required")
		return uuid.Nil, uuid.Nil, false
	}
	raw, err := middleware.GetAdminID(ctx)
	if err != nil {
		errors.WriteUnauthorizedError(w, "invalid session")
		return uuid.Nil, uuid.Nil, false
	}
	adminID, err = uuid.Parse(raw)
	if err != nil {
		errors.WriteUnauthorizedError(w, "invalid session")
		return uuid.Nil, uuid.Nil, false
	}
	current, _ = middleware.SessionFamilyID(ctx)
	return adminID, current, true
}

// recordSessionEvent audits a session or lockout action by the signed-in
// user. targetUser is the account acted on.
func (h *Handlers) recordSessionEvent(r *http.Request, action string, targetUser uuid.UUID, details map[string]any) {
	if h.audit == nil {
		return
	}
	event := audit.FromRequest(r)
	event.Action = action
	event.Outcome = audit.OutcomeSuccess
	event.ResourceType = "user"
	event.ResourceID = targetUser.String()
	event.MergeDetails(details)
	h.audit.Record(event)
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// ListMySessions handles GET /api/v1/account/sessions.
func (h *Handlers) ListMySessions(w http.ResponseWriter, r *http.Request) {
	adminID, current, ok := sessionUser(w, r)
	if !ok {
		return
	}
	items, err := h.service.ListSessions(r.Context(), adminID, current)
	if err != nil {
		h.logger.WithError(err).Error("Failed to list admin sessions")
		errors.WriteInternalError(w, "failed to list sessions")
		return
	}
	writeJSON(w, http.StatusOK, models.AdminSessionListResponse{Items: items})
}

// RevokeMySession handles DELETE /api/v1/account/sessions/{id}. Revoking the
// current session also clears its cookies (it is a logout).
func (h *Handlers) RevokeMySession(w http.ResponseWriter, r *http.Request) {
	adminID, current, ok := sessionUser(w, r)
	if !ok {
		return
	}
	familyID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		errors.WriteValidationError(w, "invalid session ID")
		return
	}
	revoked, err := h.service.RevokeFamily(r.Context(), adminID, familyID, adminauth.RevokeReasonUser)
	if err != nil {
		h.logger.WithError(err).Error("Failed to revoke admin session")
		errors.WriteInternalError(w, "failed to revoke session")
		return
	}
	if !revoked {
		errors.WriteNotFoundError(w, "session not found")
		return
	}
	isCurrent := familyID == current
	h.recordSessionEvent(r, "auth.session_revoke", adminID, map[string]any{
		"session_id": familyID.String(),
		"current":    isCurrent,
	})
	if isCurrent {
		h.clearCookies(w)
	}
	w.WriteHeader(http.StatusNoContent)
}

// RevokeMyOtherSessions handles POST /api/v1/account/sessions/revoke-others.
func (h *Handlers) RevokeMyOtherSessions(w http.ResponseWriter, r *http.Request) {
	adminID, current, ok := sessionUser(w, r)
	if !ok {
		return
	}
	if current == uuid.Nil {
		// A token from before session families cannot name the session to
		// keep; ending "the others" would end this one too.
		errors.WriteError(w, http.StatusConflict, "conflict", "this session predates session tracking; sign in again first")
		return
	}
	h.revokeMine(w, r, adminID, current, "others")
}

// RevokeAllMySessions handles POST /api/v1/account/sessions/revoke-all: sign
// out everywhere, this browser included.
func (h *Handlers) RevokeAllMySessions(w http.ResponseWriter, r *http.Request) {
	adminID, _, ok := sessionUser(w, r)
	if !ok {
		return
	}
	h.revokeMine(w, r, adminID, uuid.Nil, "all")
}

func (h *Handlers) revokeMine(w http.ResponseWriter, r *http.Request, adminID, keep uuid.UUID, scope string) {
	count, err := h.service.RevokeAllSessions(r.Context(), adminID, keep, adminauth.RevokeReasonUser)
	if err != nil {
		h.logger.WithError(err).Error("Failed to revoke admin sessions")
		errors.WriteInternalError(w, "failed to revoke sessions")
		return
	}
	h.recordSessionEvent(r, "auth.sessions_revoke", adminID, map[string]any{
		"scope":   scope,
		"revoked": count,
	})
	if keep == uuid.Nil {
		h.clearCookies(w)
	}
	writeJSON(w, http.StatusOK, models.RevokeSessionsResponse{Revoked: count})
}

// RevokeUserSessions handles DELETE /api/v1/users/{id}/sessions (superadmin):
// signs the user out everywhere, ending their access tokens immediately, and
// cancels a pending email change.
func (h *Handlers) RevokeUserSessions(w http.ResponseWriter, r *http.Request) {
	userID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		errors.WriteValidationError(w, "invalid user ID")
		return
	}
	if _, err := h.service.GetAdminByID(r.Context(), userID); err != nil {
		errors.WriteNotFoundError(w, "user not found")
		return
	}
	count, err := h.service.RevokeUserAccess(r.Context(), userID)
	if err != nil {
		h.logger.WithError(err).Error("Failed to revoke user sessions")
		errors.WriteInternalError(w, "failed to revoke sessions")
		return
	}
	h.recordSessionEvent(r, "auth.sessions_revoke", userID, map[string]any{
		"scope":   "all",
		"revoked": count,
	})
	writeJSON(w, http.StatusOK, models.RevokeSessionsResponse{Revoked: count})
}

// UnlockUser handles POST /api/v1/users/{id}/unlock (superadmin): clears a
// login lockout and the failure streak behind it.
func (h *Handlers) UnlockUser(w http.ResponseWriter, r *http.Request) {
	userID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		errors.WriteValidationError(w, "invalid user ID")
		return
	}
	wasLocked, err := h.service.UnlockUser(r.Context(), userID)
	if err != nil {
		if stderrors.Is(err, adminauth.ErrUserNotFound) {
			errors.WriteNotFoundError(w, "user not found")
			return
		}
		h.logger.WithError(err).Error("Failed to unlock user")
		errors.WriteInternalError(w, "failed to unlock user")
		return
	}
	h.recordSessionEvent(r, "auth.unlock", userID, map[string]any{"was_locked": wasLocked})
	writeJSON(w, http.StatusOK, models.UnlockUserResponse{WasLocked: wasLocked})
}
