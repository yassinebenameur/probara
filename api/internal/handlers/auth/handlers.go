package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/api/internal/errors"
	"github.com/yassinebenameur/probara/api/internal/models"
	"github.com/yassinebenameur/probara/api/internal/services/adminauth"
	"github.com/yassinebenameur/probara/api/internal/services/audit"
	"github.com/yassinebenameur/probara/api/internal/services/oidcauth"
	"github.com/yassinebenameur/probara/shared/auth"
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

// Login handles POST /api/v1/auth/login
func (h *Handlers) Login(w http.ResponseWriter, r *http.Request) {
	var req models.AdminLoginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		errors.WriteValidationError(w, "invalid request body: "+err.Error())
		return
	}

	req.Username = strings.TrimSpace(req.Username)
	if req.Username == "" || req.Password == "" {
		errors.WriteValidationError(w, "username and password are required")
		return
	}

	user, err := h.service.Authenticate(r.Context(), req.Username, req.Password)
	if err != nil {
		h.recordAuthEvent(r, "auth.login", audit.OutcomeFailure, req.Username, nil)
		errors.WriteUnauthorizedError(w, "invalid username or password")
		return
	}
	h.recordAuthEvent(r, "auth.login", audit.OutcomeSuccess, user.Username, &user.ID)

	if err := h.service.UpdateLastLogin(r.Context(), user.ID); err != nil {
		h.logger.WithError(err).Warn("Failed to update admin last login")
	}

	if err := h.issueSession(w, r, user.ID); err != nil {
		h.logger.WithError(err).Error("Failed to issue admin session")
		errors.WriteInternalError(w, "failed to create session")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(models.AdminAuthResponse{User: *user})
}

// Refresh handles POST /api/v1/auth/refresh
func (h *Handlers) Refresh(w http.ResponseWriter, r *http.Request) {
	refreshToken, err := h.readRefreshToken(r)
	if err != nil {
		errors.WriteUnauthorizedError(w, "missing refresh token")
		return
	}

	oldHash := hashToken(refreshToken)
	adminID, err := h.service.GetSessionByTokenHash(r.Context(), oldHash)
	if err != nil {
		errors.WriteUnauthorizedError(w, "invalid refresh token")
		return
	}

	newToken, newHash, err := generateRefreshToken()
	if err != nil {
		h.logger.WithError(err).Error("Failed to generate refresh token")
		errors.WriteInternalError(w, "failed to create session")
		return
	}

	refreshTTL := time.Duration(h.cfg.AdminRefreshTTLDays) * 24 * time.Hour
	if err := h.service.RotateSession(r.Context(), oldHash, newHash, adminID, time.Now().Add(refreshTTL), clientIP(r), r.UserAgent()); err != nil {
		h.logger.WithError(err).Error("Failed to rotate refresh token")
		errors.WriteInternalError(w, "failed to refresh session")
		return
	}

	if err := h.setAccessCookie(w, adminID); err != nil {
		h.logger.WithError(err).Error("Failed to issue access token")
		errors.WriteInternalError(w, "failed to refresh session")
		return
	}

	h.setRefreshCookie(w, newToken, refreshTTL)

	user, err := h.service.GetAdminByID(r.Context(), adminID)
	if err != nil {
		h.logger.WithError(err).Warn("Failed to load admin user")
	}

	w.Header().Set("Content-Type", "application/json")
	if user != nil {
		json.NewEncoder(w).Encode(models.AdminAuthResponse{User: *user})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// Logout handles POST /api/v1/auth/logout
func (h *Handlers) Logout(w http.ResponseWriter, r *http.Request) {
	refreshToken, err := h.readRefreshToken(r)
	if err == nil && refreshToken != "" {
		_ = h.service.RevokeSession(r.Context(), hashToken(refreshToken))
	}

	if adminID, err := h.readAdminIDFromAccessCookie(r); err == nil {
		h.recordAuthEvent(r, "auth.logout", audit.OutcomeSuccess, "", &adminID)
	}

	h.clearCookies(w)
	w.WriteHeader(http.StatusNoContent)
}

// Me handles GET /api/v1/auth/me
func (h *Handlers) Me(w http.ResponseWriter, r *http.Request) {
	adminID, err := h.readAdminIDFromAccessCookie(r)
	if err != nil {
		errors.WriteUnauthorizedError(w, "invalid session")
		return
	}

	user, err := h.service.GetAdminByID(r.Context(), adminID)
	if err != nil {
		errors.WriteUnauthorizedError(w, "invalid session")
		return
	}

	memberships, err := h.service.GetMembershipsForAdmin(r.Context(), adminID)
	if err != nil {
		h.logger.WithError(err).Warn("Failed to load memberships for /me")
	} else {
		user.Memberships = memberships
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(models.AdminAuthResponse{User: *user})
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

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (h *Handlers) issueSession(w http.ResponseWriter, r *http.Request, adminID uuid.UUID) error {
	refreshToken, refreshHash, err := generateRefreshToken()
	if err != nil {
		return err
	}

	refreshTTL := time.Duration(h.cfg.AdminRefreshTTLDays) * 24 * time.Hour
	if err := h.service.CreateSession(r.Context(), adminID, refreshHash, time.Now().Add(refreshTTL), clientIP(r), r.UserAgent()); err != nil {
		return err
	}

	if err := h.setAccessCookie(w, adminID); err != nil {
		return err
	}

	h.setRefreshCookie(w, refreshToken, refreshTTL)
	return nil
}

func (h *Handlers) setAccessCookie(w http.ResponseWriter, adminID uuid.UUID) error {
	accessTTL := time.Duration(h.cfg.AdminAccessTTLMinutes) * time.Minute
	token, err := auth.CreateAdminToken(adminID.String(), h.cfg.AdminJWTSecret, accessTTL)
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

func (h *Handlers) readAdminIDFromAccessCookie(r *http.Request) (uuid.UUID, error) {
	cookie, err := r.Cookie(accessCookieName)
	if err != nil {
		return uuid.UUID{}, err
	}
	claims, err := auth.ParseAdminToken(cookie.Value, h.cfg.AdminJWTSecret)
	if err != nil || claims.AdminID == "" {
		return uuid.UUID{}, fmt.Errorf("invalid token")
	}
	return uuid.Parse(claims.AdminID)
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

func clientIP(r *http.Request) string {
	if forwarded := r.Header.Get("X-Forwarded-For"); forwarded != "" {
		parts := strings.Split(forwarded, ",")
		return strings.TrimSpace(parts[0])
	}
	return r.RemoteAddr
}
