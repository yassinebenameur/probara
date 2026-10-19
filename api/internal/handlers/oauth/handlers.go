// Package oauth serves the MCP OAuth 2.1 authorization server: discovery
// metadata (RFC 9728, RFC 8414), dynamic client registration (RFC 7591), the
// authorization code flow with PKCE, token refresh and revocation (RFC
// 7009), and the consent and connection endpoints the operator UI uses.
package oauth

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	apierrors "github.com/yassinebenameur/probara/api/internal/errors"
	"github.com/yassinebenameur/probara/api/internal/services/audit"
	"github.com/yassinebenameur/probara/api/internal/services/mcpoauth"
	ctxpkg "github.com/yassinebenameur/probara/shared/context"
	"github.com/yassinebenameur/probara/shared/logger"
)

const (
	issuerPath   = "/api/v1/oauth"
	resourcePath = "/api/v1/mcp"
	consentPath  = "/oauth/consent"
)

// Handlers serves the authorization server.
type Handlers struct {
	svc           *mcpoauth.Service
	log           *logger.Logger
	audit         *audit.Recorder
	publicBaseURL string
	appBaseURL    string
}

func NewHandlers(svc *mcpoauth.Service, log *logger.Logger, recorder *audit.Recorder, publicBaseURL, appBaseURL string) *Handlers {
	return &Handlers{svc: svc, log: log, audit: recorder, publicBaseURL: publicBaseURL, appBaseURL: appBaseURL}
}

// BaseURL is the public origin of the API: PUBLIC_BASE_URL when set, else
// derived from the request (honoring the proxy's forwarded headers, so a
// request through the web UI's /api proxy advertises the UI origin).
func (h *Handlers) BaseURL(r *http.Request) string {
	if h.publicBaseURL != "" {
		return h.publicBaseURL
	}
	return requestOrigin(r)
}

func requestOrigin(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if p := firstHeaderValue(r.Header.Get("X-Forwarded-Proto")); p != "" {
		scheme = p
	}
	host := r.Host
	if fh := firstHeaderValue(r.Header.Get("X-Forwarded-Host")); fh != "" {
		host = fh
	}
	return scheme + "://" + host
}

func firstHeaderValue(v string) string {
	if i := strings.IndexByte(v, ','); i >= 0 {
		v = v[:i]
	}
	return strings.TrimSpace(v)
}

// ResourceMetadataURL is what a 401 from the MCP endpoint advertises.
func (h *Handlers) ResourceMetadataURL(r *http.Request) string {
	return h.BaseURL(r) + "/.well-known/oauth-protected-resource" + resourcePath
}

// ProtectedResourceMetadata serves RFC 9728 metadata for the MCP endpoint.
func (h *Handlers) ProtectedResourceMetadata(w http.ResponseWriter, r *http.Request) {
	base := h.BaseURL(r)
	writeJSON(w, http.StatusOK, map[string]any{
		"resource":                 base + resourcePath,
		"authorization_servers":    []string{base + issuerPath},
		"scopes_supported":         []string{"read", "write"},
		"bearer_methods_supported": []string{"header"},
		"resource_name":            "Probara",
		"resource_documentation":   "https://github.com/yassinebenameur/probara",
	})
}

// AuthorizationServerMetadata serves RFC 8414 metadata. It is mounted under
// every well-known path clients probe (path insertion and appending, OAuth
// and OpenID names) since clients differ in which one they try first.
func (h *Handlers) AuthorizationServerMetadata(w http.ResponseWriter, r *http.Request) {
	base := h.BaseURL(r)
	issuer := base + issuerPath
	writeJSON(w, http.StatusOK, map[string]any{
		"issuer":                                     issuer,
		"authorization_endpoint":                     issuer + "/authorize",
		"token_endpoint":                             issuer + "/token",
		"registration_endpoint":                      issuer + "/register",
		"revocation_endpoint":                        issuer + "/revoke",
		"response_types_supported":                   []string{"code"},
		"grant_types_supported":                      []string{"authorization_code", "refresh_token"},
		"code_challenge_methods_supported":           []string{"S256"},
		"token_endpoint_auth_methods_supported":      []string{"none"},
		"revocation_endpoint_auth_methods_supported": []string{"none"},
		"scopes_supported":                           []string{"read", "write"},
	})
}

type registerRequest struct {
	ClientName   string   `json:"client_name"`
	RedirectURIs []string `json:"redirect_uris"`
}

// Register handles RFC 7591 dynamic client registration. Every client is
// public (PKCE, no secret) whatever auth method it asks for.
func (h *Handlers) Register(w http.ResponseWriter, r *http.Request) {
	var req registerRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&req); err != nil {
		oauthError(w, http.StatusBadRequest, "invalid_client_metadata", "body must be a JSON client metadata object")
		return
	}
	client, err := h.svc.RegisterClient(r.Context(), req.ClientName, req.RedirectURIs)
	if errors.Is(err, mcpoauth.ErrInvalidRedirectURI) {
		oauthError(w, http.StatusBadRequest, "invalid_redirect_uri", err.Error())
		return
	}
	if err != nil {
		h.log.WithError(err).Error("MCP OAuth client registration failed")
		oauthError(w, http.StatusInternalServerError, "server_error", "registration failed")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"client_id":                  client.ID.String(),
		"client_id_issued_at":        client.CreatedAt.Unix(),
		"client_name":                client.Name,
		"redirect_uris":              client.RedirectURIs,
		"grant_types":                []string{"authorization_code", "refresh_token"},
		"response_types":             []string{"code"},
		"token_endpoint_auth_method": "none",
	})
}

// Authorize validates the request and hands the user to the operator UI's
// consent screen, which signs them in first if needed. Errors that cannot
// be trusted to the redirect URI (unknown client, unregistered URI) are
// shown here instead of redirected.
func (h *Handlers) Authorize(w http.ResponseWriter, r *http.Request) {
	req, err := h.svc.ValidateAuthorization(r.Context(), r.URL.Query())
	var redirectErr *mcpoauth.RedirectError
	switch {
	case errors.As(err, &redirectErr):
		http.Redirect(w, r, errorRedirect(req, redirectErr.Code, redirectErr.Description), http.StatusFound)
		return
	case err != nil:
		http.Error(w, "Authorization request rejected: "+err.Error(), http.StatusBadRequest)
		return
	}
	consent := h.appBaseURL
	if consent == "" {
		consent = h.BaseURL(r)
	}
	http.Redirect(w, r, consent+consentPath+"?"+r.URL.RawQuery, http.StatusFound)
}

func errorRedirect(req *mcpoauth.AuthorizationRequest, code, description string) string {
	q := url.Values{"error": {code}}
	if description != "" {
		q.Set("error_description", description)
	}
	if req.State != "" {
		q.Set("state", req.State)
	}
	return appendQuery(req.RedirectURI, q)
}

func appendQuery(rawURL string, q url.Values) string {
	sep := "?"
	if strings.Contains(rawURL, "?") {
		sep = "&"
	}
	return rawURL + sep + q.Encode()
}

// AuthorizationRequest describes a pending authorization for the consent
// screen (cookie session).
func (h *Handlers) AuthorizationRequest(w http.ResponseWriter, r *http.Request) {
	if !requireUserSession(w, r) {
		return
	}
	req, err := h.svc.ValidateAuthorization(r.Context(), r.URL.Query())
	if err != nil {
		apierrors.WriteValidationError(w, err.Error())
		return
	}
	redirect, _ := url.Parse(req.RedirectURI)
	writeJSON(w, http.StatusOK, map[string]any{
		"client_id":       req.Client.ID,
		"client_name":     req.Client.Name,
		"redirect_uri":    req.RedirectURI,
		"redirect_origin": redirect.Scheme + "://" + redirect.Host,
		"scope":           req.Scope,
	})
}

type decisionRequest struct {
	// Query is the raw /authorize query string the consent screen got.
	Query    string `json:"query"`
	Approve  bool   `json:"approve"`
	TenantID string `json:"tenant_id"`
	Scope    string `json:"scope"`
}

// Decide records the user's consent decision and returns where to send the
// browser: the client's redirect URI with a code, or with access_denied.
// It runs on the cookie session, outside the write gate (granting a
// read-only connection is not a workspace write) and outside the generic
// audit middleware (it records its own event).
func (h *Handlers) Decide(w http.ResponseWriter, r *http.Request) {
	if !requireUserSession(w, r) {
		return
	}
	if !h.sameOrigin(r) {
		apierrors.WriteForbiddenError(w, "cross-origin consent is not allowed")
		return
	}
	var body decisionRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 16<<10)).Decode(&body); err != nil {
		apierrors.WriteValidationError(w, "invalid request body")
		return
	}
	q, err := url.ParseQuery(body.Query)
	if err != nil {
		apierrors.WriteValidationError(w, "invalid authorization query")
		return
	}
	req, err := h.svc.ValidateAuthorization(r.Context(), q)
	if err != nil {
		apierrors.WriteValidationError(w, err.Error())
		return
	}
	if !body.Approve {
		writeJSON(w, http.StatusOK, map[string]string{"redirect_to": errorRedirect(req, "access_denied", "the user declined")})
		return
	}

	// The tenant must be the one AuthMiddleware verified membership for
	// (the UI sends it as X-Tenant-ID too).
	ctxTenant, _ := ctxpkg.GetTenantID(r.Context())
	if body.TenantID == "" || body.TenantID != ctxTenant {
		apierrors.WriteValidationError(w, "choose a workspace you belong to")
		return
	}
	tenantID, _ := uuid.Parse(ctxTenant)
	adminID, _ := ctxpkg.GetAdminID(r.Context())
	userID, _ := uuid.Parse(adminID)

	// The user can lower the requested scope, never raise it.
	scope := req.Scope
	if body.Scope == "read" {
		scope = "read"
	}
	code, err := h.svc.IssueCode(r.Context(), req, userID, tenantID, scope)
	if err != nil {
		h.log.WithError(err).Error("MCP OAuth code issue failed")
		apierrors.WriteInternalError(w, "could not authorize")
		return
	}
	h.record(r, "mcp_connection.authorize", req.Client.ID.String(), map[string]any{
		"client_name": req.Client.Name, "scope": scope,
	})
	q2 := url.Values{"code": {code}}
	if req.State != "" {
		q2.Set("state", req.State)
	}
	writeJSON(w, http.StatusOK, map[string]string{"redirect_to": appendQuery(req.RedirectURI, q2)})
}

// sameOrigin rejects a consent POST whose Origin is neither the API's nor
// the operator UI's. Cookies are SameSite=Lax already; this is the second
// lock on the door that mints credentials.
func (h *Handlers) sameOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	o, err := url.Parse(origin)
	if err != nil {
		return false
	}
	allowed := []string{r.Host, firstHeaderValue(r.Header.Get("X-Forwarded-Host"))}
	for _, base := range []string{h.appBaseURL, h.publicBaseURL} {
		if u, err := url.Parse(base); err == nil && u.Host != "" {
			allowed = append(allowed, u.Host)
		}
	}
	for _, host := range allowed {
		if host != "" && strings.EqualFold(o.Host, host) {
			return true
		}
	}
	return false
}

// Token is the token endpoint (authorization_code, refresh_token).
func (h *Handlers) Token(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	if err := r.ParseForm(); err != nil {
		oauthError(w, http.StatusBadRequest, "invalid_request", "form body required")
		return
	}
	var (
		pair *mcpoauth.TokenPair
		err  error
	)
	switch r.PostForm.Get("grant_type") {
	case "authorization_code":
		pair, err = h.svc.ExchangeCode(r.Context(), r.PostForm.Get("code"), r.PostForm.Get("client_id"),
			r.PostForm.Get("redirect_uri"), r.PostForm.Get("code_verifier"))
	case "refresh_token":
		pair, err = h.svc.Refresh(r.Context(), r.PostForm.Get("refresh_token"), r.PostForm.Get("client_id"))
	default:
		oauthError(w, http.StatusBadRequest, "unsupported_grant_type", "use authorization_code or refresh_token")
		return
	}
	switch {
	case errors.Is(err, mcpoauth.ErrInvalidGrant):
		oauthError(w, http.StatusBadRequest, "invalid_grant", strings.TrimPrefix(err.Error(), "invalid_grant: "))
		return
	case err != nil:
		h.log.WithError(err).Error("MCP OAuth token request failed")
		oauthError(w, http.StatusInternalServerError, "server_error", "token request failed")
		return
	}
	w.Header().Set("Pragma", "no-cache")
	writeJSON(w, http.StatusOK, pair)
}

// Revoke is RFC 7009 revocation: possession of a token is the credential,
// and the answer is 200 whether or not the token was known.
func (h *Handlers) Revoke(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	if err := r.ParseForm(); err != nil {
		oauthError(w, http.StatusBadRequest, "invalid_request", "form body required")
		return
	}
	if token := r.PostForm.Get("token"); token != "" {
		if err := h.svc.RevokeToken(r.Context(), token); err != nil {
			h.log.WithError(err).Error("MCP OAuth revocation failed")
		}
	}
	w.WriteHeader(http.StatusOK)
}

// ListGrants lists the signed-in user's MCP connections.
func (h *Handlers) ListGrants(w http.ResponseWriter, r *http.Request) {
	userID, ok := sessionUser(w, r)
	if !ok {
		return
	}
	grants, err := h.svc.ListGrants(r.Context(), userID)
	if err != nil {
		h.log.WithError(err).Error("List MCP connections failed")
		apierrors.WriteInternalError(w, "failed to list connections")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": grants})
}

// RevokeGrant disconnects one of the signed-in user's MCP connections.
func (h *Handlers) RevokeGrant(w http.ResponseWriter, r *http.Request) {
	userID, ok := sessionUser(w, r)
	if !ok {
		return
	}
	grantID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		apierrors.WriteValidationError(w, "invalid connection ID")
		return
	}
	switch err := h.svc.RevokeGrant(r.Context(), userID, grantID); {
	case errors.Is(err, mcpoauth.ErrGrantNotFound):
		apierrors.WriteNotFoundError(w, "connection not found")
		return
	case err != nil:
		h.log.WithError(err).Error("Revoke MCP connection failed")
		apierrors.WriteInternalError(w, "failed to revoke connection")
		return
	}
	h.record(r, "mcp_connection.revoke", grantID.String(), nil)
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handlers) record(r *http.Request, action, resourceID string, details map[string]any) {
	if h.audit == nil {
		return
	}
	event := audit.FromRequest(r)
	event.Action = action
	event.Outcome = audit.OutcomeSuccess
	event.ResourceType = "mcp_connection"
	event.ResourceID = resourceID
	event.MergeDetails(details)
	h.audit.Record(event)
}

// requireUserSession admits a signed-in user (cookie session) only: an API
// key or an MCP token must never be able to mint or manage MCP grants.
func requireUserSession(w http.ResponseWriter, r *http.Request) bool {
	_, ok := sessionUser(w, r)
	return ok
}

func sessionUser(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	ctx := r.Context()
	if _, scoped := ctxpkg.GetAuthScope(ctx); scoped || !ctxpkg.IsAdmin(ctx) {
		apierrors.WriteForbiddenError(w, "a signed-in user session is required")
		return uuid.Nil, false
	}
	adminID, _ := ctxpkg.GetAdminID(ctx)
	id, err := uuid.Parse(adminID)
	if err != nil {
		apierrors.WriteUnauthorizedError(w, "invalid session")
		return uuid.Nil, false
	}
	return id, true
}

// PublicCORS opens the protocol endpoints to browser-based MCP clients.
// They carry no cookies and authenticate by PKCE or token possession, so a
// wildcard origin grants nothing.
func PublicCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, Mcp-Protocol-Version, Mcp-Session-Id, Last-Event-ID")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Expose-Headers", "WWW-Authenticate, Mcp-Session-Id")
		w.Header().Set("Access-Control-Max-Age", fmt.Sprint(int((10 * time.Minute).Seconds())))
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func oauthError(w http.ResponseWriter, status int, code, description string) {
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, status, map[string]string{"error": code, "error_description": description})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
