package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/yassinebenameur/probara/api/internal/services/audit"
	"github.com/yassinebenameur/probara/api/internal/services/oidcauth"
)

const (
	oidcFlowCookieName = "oidc_flow"
	oidcFlowMaxAge     = 600 // seconds; one login attempt
)

// WithOIDC attaches the OIDC service to the auth handlers.
func (h *Handlers) WithOIDC(oidcService *oidcauth.Service) *Handlers {
	h.oidc = oidcService
	return h
}

// OIDCStatus handles GET /api/v1/auth/oidc/status (public). The login page
// uses it to decide whether to render the SSO button.
func (h *Handlers) OIDCStatus(w http.ResponseWriter, r *http.Request) {
	enabled := h.oidc != nil && h.oidc.Enabled()
	resp := map[string]any{"enabled": enabled}
	if enabled {
		resp["label"] = h.oidc.ProviderLabel()
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// OIDCStart handles GET /api/v1/auth/oidc/start: sets the signed flow cookie
// and redirects the browser to the IdP.
func (h *Handlers) OIDCStart(w http.ResponseWriter, r *http.Request) {
	if h.oidc == nil || !h.oidc.Enabled() {
		http.NotFound(w, r)
		return
	}

	next := sanitizeNextPath(r.URL.Query().Get("next"))
	authURL, flow, err := h.oidc.BeginFlow(r.Context(), next)
	if err != nil {
		h.logger.WithError(err).Error("Failed to begin OIDC flow")
		h.redirectLoginError(w, r, "provider_unreachable")
		return
	}

	encoded, err := h.encodeFlowCookie(flow)
	if err != nil {
		h.logger.WithError(err).Error("Failed to encode OIDC flow cookie")
		h.redirectLoginError(w, r, "internal")
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     oidcFlowCookieName,
		Value:    encoded,
		Path:     "/",
		HttpOnly: true,
		Secure:   h.cfg.AdminCookieSecure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   oidcFlowMaxAge,
	})
	http.Redirect(w, r, authURL, http.StatusFound)
}

// OIDCCallback handles GET /api/v1/auth/oidc/callback: verifies state/nonce/
// PKCE, resolves the user and issues the regular admin session cookies.
// The browser is navigating, so every outcome is a redirect — never JSON.
func (h *Handlers) OIDCCallback(w http.ResponseWriter, r *http.Request) {
	if h.oidc == nil || !h.oidc.Enabled() {
		http.NotFound(w, r)
		return
	}

	clearFlow := func() {
		http.SetCookie(w, &http.Cookie{
			Name: oidcFlowCookieName, Value: "", Path: "/", HttpOnly: true,
			Secure: h.cfg.AdminCookieSecure, SameSite: http.SameSiteLaxMode, MaxAge: -1,
		})
	}

	if errParam := r.URL.Query().Get("error"); errParam != "" {
		clearFlow()
		h.logger.WithFields(map[string]interface{}{
			"error":             errParam,
			"error_description": r.URL.Query().Get("error_description"),
		}).Warn("OIDC provider returned an error")
		h.redirectLoginError(w, r, "provider_denied")
		return
	}

	flow, err := h.readFlowCookie(r)
	if err != nil {
		clearFlow()
		h.redirectLoginError(w, r, "state_mismatch")
		return
	}
	if state := r.URL.Query().Get("state"); state == "" || !hmac.Equal([]byte(state), []byte(flow.State)) {
		clearFlow()
		h.redirectLoginError(w, r, "state_mismatch")
		return
	}

	code := r.URL.Query().Get("code")
	if code == "" {
		clearFlow()
		h.redirectLoginError(w, r, "exchange_failed")
		return
	}

	claims, err := h.oidc.CompleteFlow(r.Context(), code, flow)
	if err != nil {
		clearFlow()
		h.logger.WithError(err).Warn("OIDC flow completion failed")
		h.redirectLoginError(w, r, "exchange_failed")
		return
	}

	user, jitProvisioned, err := h.oidc.ResolveUser(r.Context(), claims)
	if err != nil {
		clearFlow()
		if errors.Is(err, oidcauth.ErrNotProvisioned) {
			h.recordAuthEvent(r, "auth.oidc_login", audit.OutcomeDenied, claims.Email, nil)
			h.redirectLoginError(w, r, "not_provisioned")
			return
		}
		h.logger.WithError(err).Error("Failed to resolve OIDC user")
		h.redirectLoginError(w, r, "internal")
		return
	}

	if err := h.service.UpdateLastLogin(r.Context(), user.ID); err != nil {
		h.logger.WithError(err).Warn("Failed to update last login")
	}
	if err := h.issueSession(w, r, user.ID); err != nil {
		clearFlow()
		h.logger.WithError(err).Error("Failed to issue session after OIDC login")
		h.redirectLoginError(w, r, "internal")
		return
	}
	clearFlow()

	if h.audit != nil {
		event := audit.FromRequest(r)
		event.Action = "auth.oidc_login"
		event.Outcome = audit.OutcomeSuccess
		event.ActorType = audit.ActorAdminUser
		event.ActorID = &user.ID
		event.ActorLabel = user.Username
		event.Details = map[string]any{"jit_provisioned": jitProvisioned}
		h.audit.Record(event)
	}

	target := flow.Next
	if target == "" {
		target = "/"
	}
	http.Redirect(w, r, target, http.StatusFound)
}

func (h *Handlers) redirectLoginError(w http.ResponseWriter, r *http.Request, code string) {
	http.Redirect(w, r, "/login?sso_error="+url.QueryEscape(code), http.StatusFound)
}

// sanitizeNextPath restricts post-login redirects to same-origin paths.
func sanitizeNextPath(next string) string {
	if next == "" || !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") || strings.ContainsAny(next, "\\\r\n") {
		return ""
	}
	return next
}

// The flow cookie is base64url(payload JSON) + "." + base64url(HMAC-SHA256),
// keyed with ADMIN_JWT_SECRET — stateless CSRF/nonce/PKCE storage that the
// user cannot tamper with.
func (h *Handlers) encodeFlowCookie(flow *oidcauth.FlowState) (string, error) {
	payload, err := json.Marshal(flow)
	if err != nil {
		return "", err
	}
	encoded := base64.RawURLEncoding.EncodeToString(payload)
	return encoded + "." + h.signFlowPayload(encoded), nil
}

func (h *Handlers) readFlowCookie(r *http.Request) (*oidcauth.FlowState, error) {
	cookie, err := r.Cookie(oidcFlowCookieName)
	if err != nil || cookie.Value == "" {
		return nil, fmt.Errorf("missing flow cookie")
	}

	parts := strings.SplitN(cookie.Value, ".", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("malformed flow cookie")
	}
	if !hmac.Equal([]byte(h.signFlowPayload(parts[0])), []byte(parts[1])) {
		return nil, fmt.Errorf("flow cookie signature mismatch")
	}

	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return nil, fmt.Errorf("malformed flow cookie payload")
	}
	flow := &oidcauth.FlowState{}
	if err := json.Unmarshal(payload, flow); err != nil {
		return nil, fmt.Errorf("malformed flow cookie payload")
	}
	if flow.State == "" || flow.Nonce == "" || flow.Verifier == "" {
		return nil, fmt.Errorf("incomplete flow cookie")
	}
	return flow, nil
}

func (h *Handlers) signFlowPayload(encodedPayload string) string {
	mac := hmac.New(sha256.New, []byte(h.cfg.AdminJWTSecret))
	mac.Write([]byte(encodedPayload))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}
