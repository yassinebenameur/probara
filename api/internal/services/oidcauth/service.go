package oidcauth

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/google/uuid"
	"github.com/lib/pq"
	"golang.org/x/oauth2"

	"github.com/yassinebenameur/probara/api/internal/models"
	"github.com/yassinebenameur/probara/shared/auth"
	"github.com/yassinebenameur/probara/shared/config"
	"github.com/yassinebenameur/probara/shared/db"
	"github.com/yassinebenameur/probara/shared/logger"
)

var (
	// ErrNotProvisioned means no matching account exists and JIT is disabled.
	ErrNotProvisioned = errors.New("no account is provisioned for this identity")
	// ErrDisabled means OIDC SSO is not enabled on this install.
	ErrDisabled = errors.New("OIDC SSO is not enabled")
)

// Claims are the ID-token claims we consume.
type Claims struct {
	Issuer            string
	Subject           string
	Email             string `json:"email"`
	EmailVerified     bool   `json:"email_verified"`
	PreferredUsername string `json:"preferred_username"`
	Name              string `json:"name"`
}

// FlowState is the per-login-attempt state carried in the signed flow cookie.
type FlowState struct {
	State    string `json:"state"`
	Nonce    string `json:"nonce"`
	Verifier string `json:"verifier"`
	Next     string `json:"next,omitempty"`
}

// Service implements the OIDC authorization-code flow with PKCE and maps
// external identities onto admin_users.
type Service struct {
	cfg config.OIDCConfig
	db  *db.Client
	log *logger.Logger

	// Provider discovery is lazy so the API boots while the IdP is down.
	mu       sync.Mutex
	provider *oidc.Provider
}

// NewService creates an OIDC auth service.
func NewService(cfg config.OIDCConfig, dbClient *db.Client, log *logger.Logger) *Service {
	return &Service{cfg: cfg, db: dbClient, log: log}
}

// Enabled reports whether OIDC SSO is configured.
func (s *Service) Enabled() bool { return s.cfg.Enabled }

// ProviderLabel is the display label for the login button.
func (s *Service) ProviderLabel() string { return s.cfg.ProviderLabel }

// IssuerURL returns the configured issuer.
func (s *Service) IssuerURL() string { return s.cfg.IssuerURL }

// JITEnabled reports whether just-in-time provisioning is on.
func (s *Service) JITEnabled() bool { return s.cfg.JITProvision }

func (s *Service) getProvider(ctx context.Context) (*oidc.Provider, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.provider != nil {
		return s.provider, nil
	}

	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	provider, err := oidc.NewProvider(ctx, s.cfg.IssuerURL)
	if err != nil {
		return nil, fmt.Errorf("OIDC provider discovery failed: %w", err)
	}
	s.provider = provider
	return provider, nil
}

func (s *Service) oauthConfig(provider *oidc.Provider) *oauth2.Config {
	return &oauth2.Config{
		ClientID:     s.cfg.ClientID,
		ClientSecret: s.cfg.ClientSecret,
		Endpoint:     provider.Endpoint(),
		RedirectURL:  s.cfg.RedirectURL,
		Scopes:       s.cfg.Scopes,
	}
}

// BeginFlow generates state/nonce/PKCE material and the IdP redirect URL.
func (s *Service) BeginFlow(ctx context.Context, next string) (string, *FlowState, error) {
	if !s.cfg.Enabled {
		return "", nil, ErrDisabled
	}

	provider, err := s.getProvider(ctx)
	if err != nil {
		return "", nil, err
	}

	state, err := randomHex(32)
	if err != nil {
		return "", nil, err
	}
	nonce, err := randomHex(32)
	if err != nil {
		return "", nil, err
	}
	verifier := oauth2.GenerateVerifier()

	flow := &FlowState{State: state, Nonce: nonce, Verifier: verifier, Next: next}
	authURL := s.oauthConfig(provider).AuthCodeURL(state,
		oidc.Nonce(nonce),
		oauth2.S256ChallengeOption(verifier),
	)
	return authURL, flow, nil
}

// CompleteFlow exchanges the code (with PKCE verifier), verifies the ID token
// and nonce, and returns the validated claims.
func (s *Service) CompleteFlow(ctx context.Context, code string, flow *FlowState) (*Claims, error) {
	if !s.cfg.Enabled {
		return nil, ErrDisabled
	}

	provider, err := s.getProvider(ctx)
	if err != nil {
		return nil, err
	}

	token, err := s.oauthConfig(provider).Exchange(ctx, code, oauth2.VerifierOption(flow.Verifier))
	if err != nil {
		return nil, fmt.Errorf("code exchange failed: %w", err)
	}

	rawIDToken, ok := token.Extra("id_token").(string)
	if !ok || rawIDToken == "" {
		return nil, fmt.Errorf("token response missing id_token")
	}

	idToken, err := provider.Verifier(&oidc.Config{ClientID: s.cfg.ClientID}).Verify(ctx, rawIDToken)
	if err != nil {
		return nil, fmt.Errorf("id_token verification failed: %w", err)
	}
	if idToken.Nonce != flow.Nonce {
		return nil, fmt.Errorf("nonce mismatch")
	}

	claims := &Claims{}
	if err := idToken.Claims(claims); err != nil {
		return nil, fmt.Errorf("failed to parse claims: %w", err)
	}
	claims.Issuer = idToken.Issuer
	claims.Subject = idToken.Subject
	if claims.Subject == "" {
		return nil, fmt.Errorf("id_token missing subject")
	}

	return claims, nil
}

// ResolveUser maps validated claims to an admin user:
//  1. exact (issuer, subject) match → login;
//  2. verified-email match on a passwordless local user without an external
//     identity → link the identity to that account;
//  3. JIT provisioning (when enabled) — new member with the default
//     membership, or superadmin when the install has no active admins
//     (bootstrap parity so a fresh OIDC-only install is administrable);
//  4. otherwise ErrNotProvisioned.
//
// Returns the user and whether it was JIT-created.
func (s *Service) ResolveUser(ctx context.Context, claims *Claims) (*models.AdminUser, bool, error) {
	user, err := s.findByExternalIdentity(ctx, claims.Issuer, claims.Subject)
	if err == nil {
		return user, false, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, false, err
	}

	if claims.Email != "" && claims.EmailVerified {
		user, err = s.linkByEmail(ctx, claims.Issuer, claims.Subject, claims.Email)
		if err == nil {
			s.log.WithFields(map[string]interface{}{"email": claims.Email}).Info("Linked OIDC identity to existing user by email")
			return user, false, nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return nil, false, err
		}
	}

	if !s.cfg.JITProvision {
		return nil, false, ErrNotProvisioned
	}

	user, err = s.provisionUser(ctx, claims)
	if err != nil {
		return nil, false, err
	}
	return user, true, nil
}

const oidcUserColumns = `
	id, username, email, platform_role,
	CASE WHEN external_subject IS NOT NULL THEN 'oidc' ELSE 'password' END,
	created_at, updated_at, last_login_at, disabled_at
`

func scanUser(row interface{ Scan(...interface{}) error }) (*models.AdminUser, error) {
	var user models.AdminUser
	if err := row.Scan(
		&user.ID, &user.Username, &user.Email, &user.PlatformRole, &user.AuthMethod,
		&user.CreatedAt, &user.UpdatedAt, &user.LastLoginAt, &user.DisabledAt,
	); err != nil {
		return nil, err
	}
	return &user, nil
}

func (s *Service) findByExternalIdentity(ctx context.Context, issuer, subject string) (*models.AdminUser, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT `+oidcUserColumns+`
		FROM admin_users
		WHERE external_issuer = $1 AND external_subject = $2 AND disabled_at IS NULL
		LIMIT 1
	`, issuer, subject)
	return scanUser(row)
}

func (s *Service) linkByEmail(ctx context.Context, issuer, subject, email string) (*models.AdminUser, error) {
	row := s.db.QueryRowContext(ctx, `
		UPDATE admin_users
		SET external_issuer = $1, external_subject = $2, updated_at = NOW()
		WHERE lower(email) = lower($3)
			AND password_hash IS NULL
			AND external_subject IS NULL
			AND disabled_at IS NULL
		RETURNING `+oidcUserColumns+`
	`, issuer, subject, email)
	return scanUser(row)
}

func (s *Service) provisionUser(ctx context.Context, claims *Claims) (*models.AdminUser, error) {
	username := claims.Email
	if username == "" {
		username = claims.PreferredUsername
	}
	if username == "" {
		username = "oidc-" + claims.Subject
	}

	var email *string
	if claims.Email != "" {
		normalized := strings.ToLower(strings.TrimSpace(claims.Email))
		email = &normalized
	}

	tenantID, err := uuid.Parse(s.cfg.JITDefaultTenantID)
	if err != nil {
		return nil, fmt.Errorf("invalid OIDC_JIT_DEFAULT_TENANT_ID: %w", err)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	// Bootstrap parity: an install with zero active admins must not end up
	// with an unadministrable viewer as its only account.
	var activeAdmins int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM admin_users WHERE disabled_at IS NULL`).Scan(&activeAdmins); err != nil {
		return nil, fmt.Errorf("failed to count active admins: %w", err)
	}
	platformRole := auth.PlatformRoleMember
	if activeAdmins == 0 {
		platformRole = auth.PlatformRoleSuperadmin
	}

	now := time.Now()
	var user *models.AdminUser
	// Retry once with a collision suffix if the email-derived username is
	// taken. Each attempt runs under a savepoint: a failed INSERT otherwise
	// aborts the whole transaction.
	for attempt, candidate := 0, username; attempt < 2; attempt++ {
		if _, err := tx.ExecContext(ctx, "SAVEPOINT jit_insert"); err != nil {
			return nil, fmt.Errorf("failed to create savepoint: %w", err)
		}
		row := tx.QueryRowContext(ctx, `
			INSERT INTO admin_users (id, username, email, external_issuer, external_subject, platform_role, created_at, updated_at, last_login_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $7, $7)
			RETURNING `+oidcUserColumns+`
		`, uuid.New(), candidate, email, claims.Issuer, claims.Subject, platformRole, now)
		user, err = scanUser(row)
		if err == nil {
			break
		}
		if _, rbErr := tx.ExecContext(ctx, "ROLLBACK TO SAVEPOINT jit_insert"); rbErr != nil {
			return nil, fmt.Errorf("failed to roll back savepoint: %w", rbErr)
		}
		if isUsernameCollision(err) && attempt == 0 {
			suffix, randErr := randomHex(3)
			if randErr != nil {
				return nil, randErr
			}
			candidate = username + "-" + suffix
			continue
		}
		// A duplicate (issuer, subject) or duplicate email here means the
		// identity belongs to an account that step 1/2 refused to match — a
		// disabled account, an unverified email on an existing account, or an
		// email already bound to another identity. Refuse login rather than
		// minting a doppelgänger.
		if isIdentityOrEmailCollision(err) {
			return nil, ErrNotProvisioned
		}
		return nil, fmt.Errorf("failed to provision OIDC user: %w", err)
	}

	if platformRole == auth.PlatformRoleMember {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO tenant_memberships (id, admin_user_id, tenant_id, role, created_at, updated_at)
			SELECT $1, $2, $3, $4, $5, $5
			WHERE EXISTS (SELECT 1 FROM tenants WHERE id = $3)
		`, uuid.New(), user.ID, tenantID, s.cfg.JITDefaultRole, now); err != nil {
			return nil, fmt.Errorf("failed to create default membership: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("failed to commit JIT provisioning: %w", err)
	}

	s.log.WithFields(map[string]interface{}{
		"username":      user.Username,
		"platform_role": platformRole,
	}).Info("JIT-provisioned OIDC user")
	return user, nil
}

func isUsernameCollision(err error) bool {
	var pqErr *pq.Error
	return errors.As(err, &pqErr) && pqErr.Code == "23505" && !strings.Contains(pqErr.Constraint, "email") && !strings.Contains(pqErr.Constraint, "external")
}

func isIdentityOrEmailCollision(err error) bool {
	var pqErr *pq.Error
	return errors.As(err, &pqErr) && pqErr.Code == "23505" &&
		(strings.Contains(pqErr.Constraint, "external") || strings.Contains(pqErr.Constraint, "email"))
}

func randomHex(n int) (string, error) {
	bytes := make([]byte, n)
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("failed to generate random value: %w", err)
	}
	return hex.EncodeToString(bytes), nil
}
