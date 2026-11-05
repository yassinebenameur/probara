package statuspages

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/api/internal/models"
	"github.com/yassinebenameur/probara/shared/pageaccess"
)

// Page access: visibility, the visitor password and kiosk tokens. The
// status-page service enforces them (status-page/internal/statuspage/access.go)
// and shared/pageaccess is the contract between the two.

// ValidationError is a request the stored state makes invalid (for example
// password visibility without a password). Handlers answer it with 400.
type ValidationError struct{ msg string }

func (e *ValidationError) Error() string { return e.msg }

var errPasswordRequired = &ValidationError{msg: "a password is required for password visibility"}

// ErrAccessTokenNotFound is a token id that names no token of the page.
var ErrAccessTokenNotFound = errors.New("access token not found")

// maxActiveAccessTokens bounds a page's unrevoked kiosk tokens.
const maxActiveAccessTokens = 50

type pageAccessInput struct {
	visibility      string
	passwordHash    *string
	passwordVersion int
}

// newPageAccess resolves the access columns of a page being created.
func newPageAccess(visibility, password *string) (pageAccessInput, error) {
	in := pageAccessInput{visibility: pageaccess.Public}
	if visibility != nil {
		in.visibility = *visibility
	}
	if password != nil && *password != "" {
		hash, err := pageaccess.HashPassword(*password)
		if err != nil {
			return in, fmt.Errorf("hash status page password: %w", err)
		}
		in.passwordHash = &hash
		in.passwordVersion = 1
	}
	if in.visibility == pageaccess.Password && in.passwordHash == nil {
		return in, errPasswordRequired
	}
	return in, nil
}

// accessUpdate returns the SET fragments (numbered from argIndex) and args for
// a visibility and/or password change, checked against the stored row, which
// it locks for the rest of the transaction. Setting or clearing the password
// bumps password_version, which signs every visitor session out.
func (s *Service) accessUpdate(ctx context.Context, tx *sql.Tx, tenantID, pageID uuid.UUID, visibility, password *string, argIndex int) ([]string, []interface{}, error) {
	var current string
	var hasPassword bool
	err := tx.QueryRowContext(ctx, `
		SELECT visibility, password_hash IS NOT NULL
		FROM status_pages WHERE id = $1 AND tenant_id = $2
		FOR UPDATE
	`, pageID, tenantID).Scan(&current, &hasPassword)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil, fmt.Errorf("status page not found")
	}
	if err != nil {
		return nil, nil, fmt.Errorf("failed to load status page access: %w", err)
	}

	var parts []string
	var args []interface{}
	next := current
	if visibility != nil {
		next = *visibility
		parts = append(parts, fmt.Sprintf("visibility = $%d", argIndex+len(args)))
		args = append(args, next)
	}
	if password != nil {
		if *password == "" {
			hasPassword = false
			parts = append(parts, "password_hash = NULL")
		} else {
			hash, err := pageaccess.HashPassword(*password)
			if err != nil {
				return nil, nil, fmt.Errorf("hash status page password: %w", err)
			}
			hasPassword = true
			parts = append(parts, fmt.Sprintf("password_hash = $%d", argIndex+len(args)))
			args = append(args, hash)
		}
		parts = append(parts, "password_version = password_version + 1")
	}
	if next == pageaccess.Password && !hasPassword {
		return nil, nil, errPasswordRequired
	}
	return parts, args, nil
}

// ListAccessTokens lists a page's kiosk tokens, revoked ones included,
// newest first.
func (s *Service) ListAccessTokens(ctx context.Context, tenantID, pageID uuid.UUID) (*models.StatusPageAccessTokenListResponse, error) {
	if err := s.requirePage(ctx, tenantID, pageID); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT t.id, t.label, t.created_by, t.created_at, t.revoked_at
		FROM status_page_access_tokens t
		JOIN status_pages sp ON sp.id = t.status_page_id
		WHERE t.status_page_id = $1 AND sp.tenant_id = $2
		ORDER BY t.created_at DESC, t.id
	`, pageID, tenantID)
	if err != nil {
		return nil, fmt.Errorf("failed to list access tokens: %w", err)
	}
	defer rows.Close()
	out := &models.StatusPageAccessTokenListResponse{Items: []models.StatusPageAccessToken{}}
	for rows.Next() {
		var t models.StatusPageAccessToken
		if err := rows.Scan(&t.ID, &t.Label, &t.CreatedBy, &t.CreatedAt, &t.RevokedAt); err != nil {
			return nil, fmt.Errorf("failed to scan access token: %w", err)
		}
		out.Items = append(out.Items, t)
	}
	return out, rows.Err()
}

// CreateAccessToken mints a kiosk token. The returned value is the only time
// the token exists outside the visitor's browser; the row keeps its hash.
func (s *Service) CreateAccessToken(ctx context.Context, tenantID, pageID uuid.UUID, createdBy *uuid.UUID, label string) (*models.CreatedStatusPageAccessToken, error) {
	label = strings.TrimSpace(label)
	token, hash, err := pageaccess.NewToken()
	if err != nil {
		return nil, err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	// Lock the page so two concurrent creates cannot both pass the cap.
	var exists bool
	if err := tx.QueryRowContext(ctx, `
		SELECT TRUE FROM status_pages WHERE id = $1 AND tenant_id = $2 FOR UPDATE
	`, pageID, tenantID).Scan(&exists); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("status page not found")
		}
		return nil, fmt.Errorf("failed to load status page: %w", err)
	}
	var active int
	if err := tx.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM status_page_access_tokens WHERE status_page_id = $1 AND revoked_at IS NULL
	`, pageID).Scan(&active); err != nil {
		return nil, fmt.Errorf("failed to count access tokens: %w", err)
	}
	if active >= maxActiveAccessTokens {
		return nil, &ValidationError{msg: fmt.Sprintf("a page can have at most %d active access tokens; revoke one first", maxActiveAccessTokens)}
	}

	out := &models.CreatedStatusPageAccessToken{Token: token}
	if err := tx.QueryRowContext(ctx, `
		INSERT INTO status_page_access_tokens (status_page_id, label, token_hash, created_by)
		VALUES ($1, $2, $3, $4)
		RETURNING id, label, created_by, created_at
	`, pageID, label, hash, createdBy).Scan(&out.ID, &out.Label, &out.CreatedBy, &out.CreatedAt); err != nil {
		return nil, fmt.Errorf("failed to create access token: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("failed to commit transaction: %w", err)
	}
	return out, nil
}

// RevokeAccessToken revokes a kiosk token. Sessions opened with it end on
// their next request. Revoking twice is a no-op.
func (s *Service) RevokeAccessToken(ctx context.Context, tenantID, pageID, tokenID uuid.UUID) error {
	var id uuid.UUID
	err := s.db.QueryRowContext(ctx, `
		UPDATE status_page_access_tokens t
		SET revoked_at = COALESCE(t.revoked_at, NOW())
		FROM status_pages sp
		WHERE t.id = $1 AND t.status_page_id = $2
		  AND sp.id = t.status_page_id AND sp.tenant_id = $3
		RETURNING t.id
	`, tokenID, pageID, tenantID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrAccessTokenNotFound
	}
	if err != nil {
		return fmt.Errorf("failed to revoke access token: %w", err)
	}
	return nil
}

func (s *Service) requirePage(ctx context.Context, tenantID, pageID uuid.UUID) error {
	var exists bool
	err := s.db.QueryRowContext(ctx, `
		SELECT EXISTS (SELECT 1 FROM status_pages WHERE id = $1 AND tenant_id = $2)
	`, pageID, tenantID).Scan(&exists)
	if err != nil {
		return fmt.Errorf("failed to load status page: %w", err)
	}
	if !exists {
		return fmt.Errorf("status page not found")
	}
	return nil
}
