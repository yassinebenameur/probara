// Package mcpoauth is the OAuth 2.1 authorization server MCP clients sign in
// through (authorization code + PKCE S256, dynamic client registration,
// rotating refresh tokens). A grant records a user's consent for one client
// with a default tenant and a scope; it never stores a role. Access tokens
// authenticate MCP requests as that user, with roles resolved from the
// database on every request and the scope only narrowing them.
package mcpoauth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"

	"github.com/yassinebenameur/probara/shared/db"
)

const (
	AccessTokenPrefix  = "pbm_at_"
	RefreshTokenPrefix = "pbm_rt_"

	AccessTokenTTL  = time.Hour
	RefreshTokenTTL = 30 * 24 * time.Hour
	CodeTTL         = 10 * time.Minute

	// Registration is unauthenticated (RFC 7591 as MCP clients use it), so
	// clients that never complete a sign-in are pruned after a day.
	unusedClientTTL = 24 * time.Hour

	maxRedirectURIs = 10
	maxClientName   = 200
)

var (
	ErrInvalidClient      = errors.New("invalid_client")
	ErrInvalidRedirectURI = errors.New("invalid_redirect_uri")
	ErrInvalidGrant       = errors.New("invalid_grant")
	ErrInvalidScope       = errors.New("invalid_scope")
	ErrGrantNotFound      = errors.New("grant not found")
)

// Client is a registered MCP client.
type Client struct {
	ID           uuid.UUID `json:"client_id"`
	Name         string    `json:"client_name"`
	RedirectURIs []string  `json:"redirect_uris"`
	CreatedAt    time.Time `json:"-"`
}

// Grant is a user's standing consent for a client, as listed in the UI.
type Grant struct {
	ID         uuid.UUID  `json:"id"`
	ClientID   uuid.UUID  `json:"client_id"`
	ClientName string     `json:"client_name"`
	TenantID   uuid.UUID  `json:"tenant_id"`
	TenantName string     `json:"tenant_name"`
	Scope      string     `json:"scope"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
}

// TokenIdentity is what an access token authenticates as.
type TokenIdentity struct {
	GrantID     uuid.UUID
	AdminUserID uuid.UUID
	TenantID    uuid.UUID
	Scope       string
}

// TokenPair is a token endpoint response body (RFC 6749 §5.1).
type TokenPair struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	RefreshToken string `json:"refresh_token"`
	Scope        string `json:"scope"`
}

// AuthorizationRequest is a validated /authorize request.
type AuthorizationRequest struct {
	Client        *Client
	RedirectURI   string
	CodeChallenge string
	State         string
	Scope         string
}

// Service is the authorization server's store.
type Service struct {
	db  *db.Client
	now func() time.Time
}

func NewService(dbClient *db.Client) *Service {
	return &Service{db: dbClient, now: time.Now}
}

// RegisterClient stores a public client (RFC 7591).
func (s *Service) RegisterClient(ctx context.Context, name string, redirectURIs []string) (*Client, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		name = "MCP client"
	}
	if len(name) > maxClientName {
		name = name[:maxClientName]
	}
	if len(redirectURIs) == 0 || len(redirectURIs) > maxRedirectURIs {
		return nil, fmt.Errorf("%w: between 1 and %d redirect_uris are required", ErrInvalidRedirectURI, maxRedirectURIs)
	}
	for _, u := range redirectURIs {
		if err := ValidateRedirectURI(u); err != nil {
			return nil, err
		}
	}

	// Opportunistic cleanup of registrations nobody ever signed in with.
	_, _ = s.db.ExecContext(ctx, `
		DELETE FROM mcp_oauth_clients c
		WHERE c.created_at < $1
		  AND NOT EXISTS (SELECT 1 FROM mcp_oauth_grants g WHERE g.client_id = c.id)
		  AND NOT EXISTS (SELECT 1 FROM mcp_oauth_codes k WHERE k.client_id = c.id)`,
		s.now().Add(-unusedClientTTL))

	c := &Client{Name: name, RedirectURIs: redirectURIs}
	err := s.db.QueryRowContext(ctx, `
		INSERT INTO mcp_oauth_clients (client_name, redirect_uris) VALUES ($1, $2)
		RETURNING id, created_at`, name, pq.Array(redirectURIs)).Scan(&c.ID, &c.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("register client: %w", err)
	}
	return c, nil
}

// ValidateRedirectURI accepts https URIs, http only on a loopback host (RFC
// 8252 native apps), and private-use schemes (cursor://, vscode://). It
// refuses schemes a browser would execute or read locally, and fragments.
func ValidateRedirectURI(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" {
		return fmt.Errorf("%w: %q is not an absolute URI", ErrInvalidRedirectURI, raw)
	}
	if u.Fragment != "" {
		return fmt.Errorf("%w: %q has a fragment", ErrInvalidRedirectURI, raw)
	}
	switch strings.ToLower(u.Scheme) {
	case "https":
		if u.Host == "" {
			return fmt.Errorf("%w: %q has no host", ErrInvalidRedirectURI, raw)
		}
		return nil
	case "http":
		host := u.Hostname()
		if host == "localhost" {
			return nil
		}
		if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
			return nil
		}
		return fmt.Errorf("%w: http redirect URIs must use a loopback host", ErrInvalidRedirectURI)
	case "javascript", "data", "file", "vbscript", "blob", "about":
		return fmt.Errorf("%w: scheme %q is not allowed", ErrInvalidRedirectURI, u.Scheme)
	default:
		return nil
	}
}

// GetClient loads a registered client.
func (s *Service) GetClient(ctx context.Context, clientID string) (*Client, error) {
	id, err := uuid.Parse(clientID)
	if err != nil {
		return nil, ErrInvalidClient
	}
	c := &Client{ID: id}
	err = s.db.QueryRowContext(ctx, `
		SELECT client_name, redirect_uris, created_at FROM mcp_oauth_clients WHERE id = $1`, id).
		Scan(&c.Name, pq.Array(&c.RedirectURIs), &c.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrInvalidClient
	}
	if err != nil {
		return nil, err
	}
	return c, nil
}

// ValidateAuthorization checks an /authorize request. Errors before the
// redirect URI is trusted (unknown client, unregistered redirect URI) must
// not redirect; the caller tells them apart with IsRedirectable.
func (s *Service) ValidateAuthorization(ctx context.Context, q url.Values) (*AuthorizationRequest, error) {
	client, err := s.GetClient(ctx, q.Get("client_id"))
	if err != nil {
		return nil, err
	}
	redirectURI := q.Get("redirect_uri")
	if redirectURI == "" && len(client.RedirectURIs) == 1 {
		redirectURI = client.RedirectURIs[0]
	}
	registered := false
	for _, u := range client.RedirectURIs {
		if u == redirectURI {
			registered = true
		}
	}
	if !registered {
		return nil, fmt.Errorf("%w: redirect_uri is not registered for this client", ErrInvalidRedirectURI)
	}
	req := &AuthorizationRequest{Client: client, RedirectURI: redirectURI, State: q.Get("state")}
	if q.Get("response_type") != "code" {
		return req, &RedirectError{Code: "unsupported_response_type", Description: "only response_type=code is supported"}
	}
	if q.Get("code_challenge_method") != "S256" || len(q.Get("code_challenge")) < 43 {
		return req, &RedirectError{Code: "invalid_request", Description: "PKCE with code_challenge_method=S256 is required"}
	}
	req.CodeChallenge = q.Get("code_challenge")
	scope, err := NormalizeScope(q.Get("scope"))
	if err != nil {
		return req, &RedirectError{Code: "invalid_scope", Description: err.Error()}
	}
	req.Scope = scope
	return req, nil
}

// RedirectError is an authorization error reported to the client through
// its (validated) redirect URI.
type RedirectError struct {
	Code        string
	Description string
}

func (e *RedirectError) Error() string { return e.Code + ": " + e.Description }

// NormalizeScope maps a requested scope string to read or write: write if
// asked for (or nothing was asked), read if only read was. The consent
// screen lets the user lower it.
func NormalizeScope(raw string) (string, error) {
	hasRead, hasWrite := false, false
	for _, s := range strings.Fields(raw) {
		switch s {
		case "read":
			hasRead = true
		case "write":
			hasWrite = true
		case "offline_access", "openid", "profile", "email":
			// Harmless extras some clients always ask for.
		default:
			return "", fmt.Errorf("%w: unknown scope %q (want read or write)", ErrInvalidScope, s)
		}
	}
	if hasRead && !hasWrite {
		return "read", nil
	}
	return "write", nil
}

// IssueCode records the user's consent as a one-time authorization code.
func (s *Service) IssueCode(ctx context.Context, req *AuthorizationRequest, adminUserID, tenantID uuid.UUID, scope string) (string, error) {
	if scope != "read" && scope != "write" {
		return "", ErrInvalidScope
	}
	code, err := randomToken("")
	if err != nil {
		return "", err
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO mcp_oauth_codes
			(code_hash, client_id, admin_user_id, tenant_id, scope, redirect_uri, code_challenge, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		hashToken(code), req.Client.ID, adminUserID, tenantID, scope, req.RedirectURI, req.CodeChallenge,
		s.now().Add(CodeTTL))
	if err != nil {
		return "", fmt.Errorf("issue code: %w", err)
	}
	return code, nil
}

// ExchangeCode redeems an authorization code (PKCE-verified) for tokens. A
// code presented twice revokes the grant it produced (RFC 6749 §4.1.2).
func (s *Service) ExchangeCode(ctx context.Context, code, clientID, redirectURI, verifier string) (*TokenPair, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	var (
		cID, userID, tenantID uuid.UUID
		scope, storedRedirect string
		challenge             string
		expiresAt             time.Time
		usedGrant             uuid.NullUUID
		disabled              bool
	)
	err = tx.QueryRowContext(ctx, `
		SELECT k.client_id, k.admin_user_id, k.tenant_id, k.scope, k.redirect_uri, k.code_challenge,
		       k.expires_at, k.grant_id, u.disabled_at IS NOT NULL
		FROM mcp_oauth_codes k JOIN admin_users u ON u.id = k.admin_user_id
		WHERE k.code_hash = $1
		FOR UPDATE OF k`, hashToken(code)).
		Scan(&cID, &userID, &tenantID, &scope, &storedRedirect, &challenge, &expiresAt, &usedGrant, &disabled)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("%w: unknown authorization code", ErrInvalidGrant)
	}
	if err != nil {
		return nil, err
	}
	if usedGrant.Valid {
		_, _ = tx.ExecContext(ctx, `UPDATE mcp_oauth_grants SET revoked_at = NOW() WHERE id = $1 AND revoked_at IS NULL`, usedGrant.UUID)
		if cerr := tx.Commit(); cerr != nil {
			return nil, cerr
		}
		return nil, fmt.Errorf("%w: authorization code already used; the connection it created was revoked", ErrInvalidGrant)
	}
	switch {
	case s.now().After(expiresAt):
		return nil, fmt.Errorf("%w: authorization code expired", ErrInvalidGrant)
	case cID.String() != clientID:
		return nil, fmt.Errorf("%w: code was issued to another client", ErrInvalidGrant)
	case storedRedirect != redirectURI:
		return nil, fmt.Errorf("%w: redirect_uri does not match the authorization request", ErrInvalidGrant)
	case !pkceMatches(verifier, challenge):
		return nil, fmt.Errorf("%w: code_verifier does not match the code challenge", ErrInvalidGrant)
	case disabled:
		return nil, fmt.Errorf("%w: account disabled", ErrInvalidGrant)
	}

	var grantID uuid.UUID
	if err := tx.QueryRowContext(ctx, `
		INSERT INTO mcp_oauth_grants (client_id, admin_user_id, tenant_id, scope)
		VALUES ($1, $2, $3, $4) RETURNING id`, cID, userID, tenantID, scope).Scan(&grantID); err != nil {
		return nil, fmt.Errorf("create grant: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE mcp_oauth_codes SET grant_id = $1 WHERE code_hash = $2`, grantID, hashToken(code)); err != nil {
		return nil, err
	}
	pair, err := s.issueTokens(ctx, tx, grantID, scope)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	s.prune(ctx)
	return pair, nil
}

// Refresh rotates a refresh token. Presenting an already-rotated refresh
// token means it leaked (or a client bug); the grant is revoked.
func (s *Service) Refresh(ctx context.Context, refreshToken, clientID string) (*TokenPair, error) {
	if !strings.HasPrefix(refreshToken, RefreshTokenPrefix) {
		return nil, fmt.Errorf("%w: not a refresh token", ErrInvalidGrant)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	var (
		grantID, cID uuid.UUID
		scope        string
		expiresAt    time.Time
		rotatedAt    sql.NullTime
		revoked      bool
		disabled     bool
	)
	err = tx.QueryRowContext(ctx, `
		SELECT t.grant_id, g.client_id, g.scope, t.expires_at, t.rotated_at,
		       g.revoked_at IS NOT NULL, u.disabled_at IS NOT NULL
		FROM mcp_oauth_tokens t
		JOIN mcp_oauth_grants g ON g.id = t.grant_id
		JOIN admin_users u ON u.id = g.admin_user_id
		WHERE t.token_hash = $1 AND t.kind = 'refresh'
		FOR UPDATE OF t`, hashToken(refreshToken)).
		Scan(&grantID, &cID, &scope, &expiresAt, &rotatedAt, &revoked, &disabled)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("%w: unknown refresh token", ErrInvalidGrant)
	}
	if err != nil {
		return nil, err
	}
	if rotatedAt.Valid {
		_, _ = tx.ExecContext(ctx, `UPDATE mcp_oauth_grants SET revoked_at = NOW() WHERE id = $1 AND revoked_at IS NULL`, grantID)
		if cerr := tx.Commit(); cerr != nil {
			return nil, cerr
		}
		return nil, fmt.Errorf("%w: refresh token reused; the connection was revoked", ErrInvalidGrant)
	}
	switch {
	case revoked:
		return nil, fmt.Errorf("%w: connection revoked", ErrInvalidGrant)
	case disabled:
		return nil, fmt.Errorf("%w: account disabled", ErrInvalidGrant)
	case s.now().After(expiresAt):
		return nil, fmt.Errorf("%w: refresh token expired", ErrInvalidGrant)
	case cID.String() != clientID:
		return nil, fmt.Errorf("%w: refresh token was issued to another client", ErrInvalidGrant)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE mcp_oauth_tokens SET rotated_at = NOW() WHERE token_hash = $1`, hashToken(refreshToken)); err != nil {
		return nil, err
	}
	pair, err := s.issueTokens(ctx, tx, grantID, scope)
	if err != nil {
		return nil, err
	}
	return pair, tx.Commit()
}

func (s *Service) issueTokens(ctx context.Context, tx *sql.Tx, grantID uuid.UUID, scope string) (*TokenPair, error) {
	access, err := randomToken(AccessTokenPrefix)
	if err != nil {
		return nil, err
	}
	refresh, err := randomToken(RefreshTokenPrefix)
	if err != nil {
		return nil, err
	}
	now := s.now()
	_, err = tx.ExecContext(ctx, `
		INSERT INTO mcp_oauth_tokens (token_hash, grant_id, kind, expires_at) VALUES
			($1, $3, 'access', $4), ($2, $3, 'refresh', $5)`,
		hashToken(access), hashToken(refresh), grantID, now.Add(AccessTokenTTL), now.Add(RefreshTokenTTL))
	if err != nil {
		return nil, fmt.Errorf("issue tokens: %w", err)
	}
	return &TokenPair{
		AccessToken:  access,
		TokenType:    "Bearer",
		ExpiresIn:    int(AccessTokenTTL.Seconds()),
		RefreshToken: refresh,
		Scope:        scope,
	}, nil
}

// prune drops tokens and codes that expired more than a day ago.
func (s *Service) prune(ctx context.Context) {
	cutoff := s.now().Add(-24 * time.Hour)
	_, _ = s.db.ExecContext(ctx, `DELETE FROM mcp_oauth_tokens WHERE expires_at < $1`, cutoff)
	_, _ = s.db.ExecContext(ctx, `DELETE FROM mcp_oauth_codes WHERE expires_at < $1`, cutoff)
}

// RevokeToken revokes the grant behind an access or refresh token (RFC
// 7009). Unknown tokens are not an error.
func (s *Service) RevokeToken(ctx context.Context, token string) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE mcp_oauth_grants SET revoked_at = NOW()
		WHERE revoked_at IS NULL
		  AND id = (SELECT grant_id FROM mcp_oauth_tokens WHERE token_hash = $1)`, hashToken(token))
	return err
}

// LookupAccessToken resolves a live access token. It touches the grant's
// last_used_at at most once a minute.
func (s *Service) LookupAccessToken(ctx context.Context, token string) (*TokenIdentity, error) {
	return LookupAccessToken(ctx, s.db, token)
}

// LookupAccessToken is the middleware entry point (no Service needed).
func LookupAccessToken(ctx context.Context, dbClient *db.Client, token string) (*TokenIdentity, error) {
	if !strings.HasPrefix(token, AccessTokenPrefix) {
		return nil, ErrInvalidGrant
	}
	var id TokenIdentity
	var lastUsed sql.NullTime
	err := dbClient.QueryRowContext(ctx, `
		SELECT g.id, g.admin_user_id, g.tenant_id, g.scope, g.last_used_at
		FROM mcp_oauth_tokens t JOIN mcp_oauth_grants g ON g.id = t.grant_id
		WHERE t.token_hash = $1 AND t.kind = 'access' AND t.expires_at > NOW() AND g.revoked_at IS NULL`,
		hashToken(token)).Scan(&id.GrantID, &id.AdminUserID, &id.TenantID, &id.Scope, &lastUsed)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrInvalidGrant
	}
	if err != nil {
		return nil, err
	}
	if !lastUsed.Valid || time.Since(lastUsed.Time) > time.Minute {
		_, _ = dbClient.ExecContext(ctx, `UPDATE mcp_oauth_grants SET last_used_at = NOW() WHERE id = $1`, id.GrantID)
	}
	return &id, nil
}

// ListGrants lists a user's live connections.
func (s *Service) ListGrants(ctx context.Context, adminUserID uuid.UUID) ([]Grant, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT g.id, g.client_id, c.client_name, g.tenant_id, t.name, g.scope, g.created_at, g.last_used_at
		FROM mcp_oauth_grants g
		JOIN mcp_oauth_clients c ON c.id = g.client_id
		JOIN tenants t ON t.id = g.tenant_id
		WHERE g.admin_user_id = $1 AND g.revoked_at IS NULL
		ORDER BY g.created_at DESC`, adminUserID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	grants := []Grant{}
	for rows.Next() {
		var g Grant
		if err := rows.Scan(&g.ID, &g.ClientID, &g.ClientName, &g.TenantID, &g.TenantName, &g.Scope, &g.CreatedAt, &g.LastUsedAt); err != nil {
			return nil, err
		}
		grants = append(grants, g)
	}
	return grants, rows.Err()
}

// RevokeGrant revokes one of a user's own connections.
func (s *Service) RevokeGrant(ctx context.Context, adminUserID, grantID uuid.UUID) error {
	res, err := s.db.ExecContext(ctx, `
		UPDATE mcp_oauth_grants SET revoked_at = NOW()
		WHERE id = $1 AND admin_user_id = $2 AND revoked_at IS NULL`, grantID, adminUserID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrGrantNotFound
	}
	return nil
}

func randomToken(prefix string) (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return prefix + base64.RawURLEncoding.EncodeToString(b), nil
}

// hashToken stores tokens as SHA-256: they are 256-bit random values, so a
// fast hash is enough (no password stretching) and allows indexed lookup.
func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func pkceMatches(verifier, challenge string) bool {
	if len(verifier) < 43 || len(verifier) > 128 {
		return false
	}
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:]) == challenge
}
