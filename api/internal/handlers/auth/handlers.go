package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	apierrors "github.com/yassinebenameur/probara/api/internal/errors"
	"github.com/yassinebenameur/probara/api/internal/httpx"
	"github.com/yassinebenameur/probara/api/internal/models"
	"github.com/yassinebenameur/probara/api/internal/services/adminauth"
	"github.com/yassinebenameur/probara/api/internal/services/audit"
	"github.com/yassinebenameur/probara/api/internal/services/oidcauth"
	"github.com/yassinebenameur/probara/shared/auth"
	"github.com/yassinebenameur/probara/shared/clientip"
	"github.com/yassinebenameur/probara/shared/config"
	ctxpkg "github.com/yassinebenameur/probara/shared/context"
	"github.com/yassinebenameur/probara/shared/logger"
)

const (
	accessCookieName  = "admin_access"
	refreshCookieName = "admin_refresh"
)

// Handlers handles admin auth requests.
type Handlers struct {
	service *adminauth.Service
	cfg     *config.APIConfig
	logger  *logger.Logger
	audit   *audit.Recorder
	oidc    *oidcauth.Service
}

// NewHandlers creates a new auth handler.
func NewHandlers(service *adminauth.Service, cfg *config.APIConfig, log *logger.Logger, auditRecorder *audit.Recorder) *Handlers {
	return &Handlers{service: service, cfg: cfg, logger: log, audit: auditRecorder}
}

// recordAuthEvent emits an explicit auth-lifecycle audit event. The auth
// routes sit outside the audited subrouter, so these are the only records.
func (h *Handlers) recordAuthEvent(r *http.Request, action, outcome, label string, actorID *uuid.UUID) {
	if h.audit == nil {
		return
	}
	event := audit.FromRequest(r)
	event.Action = action
	event.Outcome = outcome
	event.ActorLabel = label
	if actorID != nil {
		event.ActorType = audit.ActorAdminUser
		event.ActorID = actorID
	}
	h.audit.Record(event)
}

// Login handles POST /api/v1/auth/login. Every refusal -- unknown user,
// wrong password, locked account -- gets the same 401 and body, so neither
// the answer nor the lock reveals whether an account exists (see
// adminauth.LockoutThreshold); the audit log tells them apart.
func (h *Handlers) Login(w http.ResponseWriter, r *http.Request) {
	var req models.AdminLoginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		apierrors.WriteValidationError(w, "invalid request body: "+err.Error())
		return
	}

	req.Username = strings.TrimSpace(req.Username)
	if req.Username == "" || req.Password == "" {
		apierrors.WriteValidationError(w, "username and password are required")
		return
	}

	user, err := h.service.Authenticate(r.Context(), req.Username, req.Password)
	if err != nil {
		var failure *adminauth.LoginFailure
		if !errors.As(err, &failure) {
			// Still the generic 401, as before: an error tied to one
			// account (an unreadable stored hash) must not set it apart.
			h.logger.WithError(err).Error("Failed to check admin credentials")
			failure = &adminauth.LoginFailure{}
		}
		h.recordLoginFailure(r, req.Username, failure)
		apierrors.WriteUnauthorizedError(w, "invalid username or password")
		return
	}
	h.recordAuthEvent(r, "auth.login", audit.OutcomeSuccess, user.Username, &user.ID)

	if err := h.service.UpdateLastLogin(r.Context(), user.ID); err != nil {
		h.logger.WithError(err).Warn("Failed to update admin last login")
	}

	if err := h.issueSession(w, r, user.ID, adminauth.AuthMethodPassword); err != nil {
		h.logger.WithError(err).Error("Failed to issue admin session")
		apierrors.WriteInternalError(w, "failed to create session")
		return
	}

	httpx.WriteJSON(w, http.StatusOK, models.AdminAuthResponse{User: *user})
}

// recordLoginFailure audits a refused sign-in, and the lock when this
// failure caused one (auth.lockout). The actor label is the submitted
// username, as before; the user id is set only when it names an account.
func (h *Handlers) recordLoginFailure(r *http.Request, username string, failure *adminauth.LoginFailure) {
	if h.audit == nil {
		return
	}
	event := audit.FromRequest(r)
	event.Action = "auth.login"
	event.Outcome = audit.OutcomeFailure
	event.ActorLabel = username
	if failure.UserID != nil {
		event.ActorType = audit.ActorAdminUser
		event.ActorID = failure.UserID
	}
	if failure.Locked {
		event.MergeDetails(map[string]any{"reason": "locked", "locked_until": failure.LockedUntil.UTC()})
	}
	h.audit.Record(event)

	if failure.LockedNow && failure.UserID != nil {
		lock := audit.FromRequest(r)
		lock.Action = "auth.lockout"
		lock.Outcome = audit.OutcomeSuccess
		lock.ActorLabel = username
		lock.ActorType = audit.ActorAdminUser
		lock.ActorID = failure.UserID
		lock.ResourceType = "user"
		lock.ResourceID = failure.UserID.String()
		lock.MergeDetails(map[string]any{
			"failed_attempts": adminauth.LockoutThreshold,
			"locked_until":    failure.LockedUntil.UTC(),
		})
		h.audit.Record(lock)
	}
}

// Refresh handles POST /api/v1/auth/refresh: rotates the refresh token
// inside its session and issues an access token carrying the session id.
func (h *Handlers) Refresh(w http.ResponseWriter, r *http.Request) {
	refreshToken, err := h.readRefreshToken(r)
	if err != nil {
		apierrors.WriteUnauthorizedError(w, "missing refresh token")
		return
	}

	newToken, newHash, err := generateRefreshToken()
	if err != nil {
		h.logger.WithError(err).Error("Failed to generate refresh token")
		apierrors.WriteInternalError(w, "failed to create session")
		return
	}

	refreshTTL := time.Duration(h.cfg.AdminRefreshTTLDays) * 24 * time.Hour
	refreshed, err := h.service.RefreshSession(r.Context(), hashToken(refreshToken), newHash, time.Now().Add(refreshTTL), clientip.FromRequest(r), r.UserAgent())
	if err != nil {
		var reuse *adminauth.RefreshReuse
		switch {
		case errors.As(err, &reuse):
			h.recordSessionEvent(r, "auth.session_reuse", audit.OutcomeDenied, reuse.AdminID, reuse.FamilyID, nil)
			h.clearCookies(w)
			apierrors.WriteUnauthorizedError(w, "invalid refresh token")
		case errors.Is(err, adminauth.ErrInvalidRefreshToken):
			apierrors.WriteUnauthorizedError(w, "invalid refresh token")
		default:
			h.logger.WithError(err).Error("Failed to rotate refresh token")
			apierrors.WriteInternalError(w, "failed to refresh session")
		}
		return
	}
	adminID := refreshed.AdminID

	if err := h.setAccessCookie(w, adminID, refreshed.FamilyID); err != nil {
		h.logger.WithError(err).Error("Failed to issue access token")
		apierrors.WriteInternalError(w, "failed to refresh session")
		return
	}

	h.setRefreshCookie(w, newToken, refreshTTL)

	user, err := h.service.GetAdminByID(r.Context(), adminID)
	if err != nil {
		h.logger.WithError(err).Warn("Failed to load admin user")
	}

	if user != nil {
		httpx.WriteJSON(w, http.StatusOK, models.AdminAuthResponse{User: *user})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusNoContent)
}

// Logout handles POST /api/v1/auth/logout: ends the session (its refresh
// chain and every access token issued for it).
func (h *Handlers) Logout(w http.ResponseWriter, r *http.Request) {
	refreshToken, err := h.readRefreshToken(r)
	if err == nil && refreshToken != "" {
		_ = h.service.RevokeByRefreshToken(r.Context(), hashToken(refreshToken))
	}

	if claims, err := h.readAccessClaims(r); err == nil {
		adminID, _ := uuid.Parse(claims.AdminID)
		if sid, err := uuid.Parse(claims.SessionID); err == nil {
			// Without a refresh cookie (already cleared, or a different
			// path) the access token still names its session.
			_ = h.service.RevokeSession(r.Context(), adminID, sid, adminauth.RevokeReasonLogout)
		}
		h.recordAuthEvent(r, "auth.logout", audit.OutcomeSuccess, "", &adminID)
	}

	h.clearCookies(w)
	w.WriteHeader(http.StatusNoContent)
}

// Me handles GET /api/v1/auth/me. It sits outside the auth middleware, so it
// checks the session itself: a revoked session is signed out here too.
func (h *Handlers) Me(w http.ResponseWriter, r *http.Request) {
	claims, err := h.readAccessClaims(r)
	if err != nil {
		apierrors.WriteUnauthorizedError(w, "invalid session")
		return
	}
	adminID, err := uuid.Parse(claims.AdminID)
	if err != nil {
		apierrors.WriteUnauthorizedError(w, "invalid session")
		return
	}
	if claims.SessionID != "" {
		sid, err := uuid.Parse(claims.SessionID)
		if err != nil {
			apierrors.WriteUnauthorizedError(w, "invalid session")
			return
		}
		active, err := h.service.SessionActive(r.Context(), adminID, sid)
		if err != nil {
			h.logger.WithError(err).Error("Failed to check admin session")
			apierrors.WriteUnauthorizedError(w, "invalid session")
			return
		}
		if !active {
			apierrors.WriteUnauthorizedError(w, "session revoked")
			return
		}
	}

	user, err := h.service.GetAdminByID(r.Context(), adminID)
	if err != nil {
		apierrors.WriteUnauthorizedError(w, "invalid session")
		return
	}

	memberships, err := h.service.GetMembershipsForAdmin(r.Context(), adminID)
	if err != nil {
		h.logger.WithError(err).Warn("Failed to load memberships for /me")
	} else {
		user.Memberships = memberships
	}

	httpx.WriteJSON(w, http.StatusOK, models.AdminAuthResponse{User: *user})
}

// AuthContext handles GET /api/v1/auth-context (authenticated subrouter).
// It reports the effective identity for either credential type so the
// frontend can gate UI affordances in both cookie and API-key modes.
func (h *Handlers) AuthContext(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	resp := models.AuthContextResponse{CanWrite: ctxpkg.CanWrite(ctx)}
	if actorType, ok := ctxpkg.GetActorType(ctx); ok {
		resp.ActorType = actorType
	}
	if adminID, ok := ctxpkg.GetAdminID(ctx); ok {
		resp.AdminID = &adminID
	}
	if keyID, ok := ctxpkg.GetAPIKeyID(ctx); ok {
		resp.APIKeyID = &keyID
	}
	if tenantID, ok := ctxpkg.GetTenantID(ctx); ok {
		resp.TenantID = &tenantID
	}
	if platformRole, ok := ctxpkg.GetPlatformRole(ctx); ok {
		resp.PlatformRole = &platformRole
	}
	if role, ok := ctxpkg.GetRole(ctx); ok {
		resp.Role = &role
	}
	if scope, ok := ctxpkg.GetAuthScope(ctx); ok {
		resp.Scope = &scope
	}

	httpx.WriteJSON(w, http.StatusOK, resp)
}

// issueSession starts a session (a new session family) for a completed
// sign-in and sets its cookies. authMethod is adminauth.AuthMethodPassword
// or AuthMethodOIDC.
func (h *Handlers) issueSession(w http.ResponseWriter, r *http.Request, adminID uuid.UUID, authMethod string) error {
	refreshToken, refreshHash, err := generateRefreshToken()
	if err != nil {
		return err
	}

	refreshTTL := time.Duration(h.cfg.AdminRefreshTTLDays) * 24 * time.Hour
	familyID, err := h.service.CreateSession(r.Context(), adminID, refreshHash, time.Now().Add(refreshTTL), clientip.FromRequest(r), r.UserAgent(), authMethod)
	if err != nil {
		return err
	}

	if err := h.setAccessCookie(w, adminID, familyID); err != nil {
		return err
	}

	h.setRefreshCookie(w, refreshToken, refreshTTL)
	return nil
}

// setAccessCookie issues the access JWT for a session; familyID becomes its
// sid (uuid.Nil, before session families exist, issues none).
func (h *Handlers) setAccessCookie(w http.ResponseWriter, adminID, familyID uuid.UUID) error {
	sid := ""
	if familyID != uuid.Nil {
		sid = familyID.String()
	}
	accessTTL := time.Duration(h.cfg.AdminAccessTTLMinutes) * time.Minute
	token, err := auth.CreateAdminToken(adminID.String(), sid, h.cfg.AdminJWTSecret, accessTTL)
	if err != nil {
		return err
	}

	cookie := &http.Cookie{
		Name:     accessCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   h.cfg.AdminCookieSecure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(accessTTL.Seconds()),
	}
	http.SetCookie(w, cookie)
	return nil
}

func (h *Handlers) setRefreshCookie(w http.ResponseWriter, token string, ttl time.Duration) {
	cookie := &http.Cookie{
		Name:     refreshCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   h.cfg.AdminCookieSecure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(ttl.Seconds()),
	}
	http.SetCookie(w, cookie)
}

func (h *Handlers) clearCookies(w http.ResponseWriter) {
	access := &http.Cookie{
		Name:     accessCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   h.cfg.AdminCookieSecure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	}
	refresh := &http.Cookie{
		Name:     refreshCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   h.cfg.AdminCookieSecure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	}
	http.SetCookie(w, access)
	http.SetCookie(w, refresh)
}

func (h *Handlers) readRefreshToken(r *http.Request) (string, error) {
	cookie, err := r.Cookie(refreshCookieName)
	if err != nil {
		return "", err
	}
	if cookie.Value == "" {
		return "", fmt.Errorf("empty refresh token")
	}
	return cookie.Value, nil
}

// readAccessClaims parses the access cookie (signature and expiry checked).
func (h *Handlers) readAccessClaims(r *http.Request) (*auth.AdminClaims, error) {
	cookie, err := r.Cookie(accessCookieName)
	if err != nil {
		return nil, err
	}
	claims, err := auth.ParseAdminToken(cookie.Value, h.cfg.AdminJWTSecret)
	if err != nil || claims.AdminID == "" {
		return nil, fmt.Errorf("invalid token")
	}
	return claims, nil
}

// recordSessionEvent audits a session lifecycle event of userID's session.
func (h *Handlers) recordSessionEvent(r *http.Request, action, outcome string, userID, sessionID uuid.UUID, details map[string]any) {
	if h.audit == nil {
		return
	}
	event := audit.FromRequest(r)
	event.Action = action
	event.Outcome = outcome
	event.ActorType = audit.ActorAdminUser
	event.ActorID = &userID
	event.ResourceType = "session"
	event.ResourceID = sessionID.String()
	event.MergeDetails(details)
	h.audit.Record(event)
}

func generateRefreshToken() (string, string, error) {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", "", fmt.Errorf("failed to generate refresh token: %w", err)
	}
	plain := hex.EncodeToString(bytes)
	return plain, hashToken(plain), nil
}

func hashToken(token string) string {
	hash := sha256.Sum256([]byte(token))
	return hex.EncodeToString(hash[:])
}
