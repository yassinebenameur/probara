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
	"github.com/yassinebenameur/probara/shared/statusupdates"
)

// Page access: visibility, the visitor password and kiosk tokens. The API is
// their only writer; the status-page service reads and enforces them
// (status-page/internal/statuspage/access.go), and shared/pageaccess is the
// contract between the two.

var (
	// ErrPasswordRequired: password visibility on a page with no password
	// stored and none in the request.
	ErrPasswordRequired = errors.New("a password is required for password visibility")
	// ErrAccessTokenNotFound: the page has no kiosk token with that ID.
	ErrAccessTokenNotFound = errors.New("access token not found")
	// ErrTooManyAccessTokens: the page is at its active-token cap.
	ErrTooManyAccessTokens = fmt.Errorf("a page can have at most %d active kiosk tokens; revoke one first", MaxActiveAccessTokens)
)

// MaxActiveAccessTokens bounds a page's unrevoked kiosk tokens.
const MaxActiveAccessTokens = 50

// TokenCreator is who creates a kiosk token: an operator, an API key, or
// both unset when neither is known.
type TokenCreator struct {
	UserID   uuid.UUID
	APIKeyID uuid.UUID
}

type pageAccessColumns struct {
	visibility      string
	passwordHash    *string
	passwordVersion int
}

// newPageAccess resolves the access columns of a page being created.
func newPageAccess(visibility, password *string) (pageAccessColumns, error) {
	cols := pageAccessColumns{visibility: pageaccess.Public}
	if visibility != nil {
		cols.visibility = *visibility
	}
	if password != nil && *password != "" {
		hash, err := pageaccess.HashPassword(*password)
		if err != nil {
			return cols, fmt.Errorf("hash status page password: %w", err)
		}
		cols.passwordHash = &hash
		cols.passwordVersion = 1
	}
	if cols.visibility == pageaccess.Password && cols.passwordHash == nil {
		return cols, ErrPasswordRequired
	}
	return cols, nil
}

// accessUpdate returns the SET fragments (numbered from argIndex) and args
// for a visibility and/or password change, checked against the stored row,
// which it locks for the rest of the transaction so a concurrent clear cannot
// leave a password page without a password. Setting or clearing the password
// bumps password_version, which ends every password session. changed reports
// whether anything visitors' access depends on moved.
func (s *Service) accessUpdate(ctx context.Context, tx *sql.Tx, tenantID, pageID uuid.UUID, visibility, password *string, argIndex int) (parts []string, args []interface{}, changed bool, err error) {
	var current string
	var hasPassword bool
	err = tx.QueryRowContext(ctx, `
		SELECT visibility, password_hash IS NOT NULL
		FROM status_pages WHERE id = $1 AND tenant_id = $2
		FOR UPDATE
	`, pageID, tenantID).Scan(&current, &hasPassword)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil, false, ErrStatusPageNotFound
	}
	if err != nil {
		return nil, nil, false, fmt.Errorf("failed to load status page access: %w", err)
	}

	next := current
	if visibility != nil && *visibility != current {
		next = *visibility
		parts = append(parts, fmt.Sprintf("visibility = $%d", argIndex+len(args)))
		args = append(args, next)
		changed = true
	}
	if password != nil {
		if *password == "" {
			hasPassword = false
			parts = append(parts, "password_hash = NULL")
		} else {
			hash, err := pageaccess.HashPassword(*password)
			if err != nil {
				return nil, nil, false, fmt.Errorf("hash status page password: %w", err)
			}
			hasPassword = true
			parts = append(parts, fmt.Sprintf("password_hash = $%d", argIndex+len(args)))
			args = append(args, hash)
		}
		parts = append(parts, "password_version = password_version + 1")
		changed = true
	}
	if next == pageaccess.Password && !hasPassword {
		return nil, nil, false, ErrPasswordRequired
	}
	return parts, args, changed, nil
}

const accessTokenColumns = `
	t.id, t.label, t.created_by_user_id, t.created_by_api_key_id,
	COALESCE(au.username, ak.name, ''), t.created_at, t.revoked_at`

const accessTokenJoins = `
	LEFT JOIN admin_users au ON au.id = t.created_by_user_id
	LEFT JOIN api_keys ak ON ak.id = t.created_by_api_key_id`

func scanAccessToken(row interface{ Scan(...interface{}) error }) (models.StatusPageAccessToken, error) {
	var t models.StatusPageAccessToken
	var userID, keyID uuid.NullUUID
	var revokedAt sql.NullTime
	if err := row.Scan(&t.ID, &t.Label, &userID, &keyID, &t.CreatedBy, &t.CreatedAt, &revokedAt); err != nil {
		return t, err
	}
	if userID.Valid {
		t.CreatedByUserID = &userID.UUID
	}
	if keyID.Valid {
		t.CreatedByAPIKeyID = &keyID.UUID
	}
	if revokedAt.Valid {
		t.RevokedAt = &revokedAt.Time
	}
	return t, nil
}

// ListAccessTokens lists a page's kiosk tokens, revoked ones included,
// newest first. Token values are never returned.
func (s *Service) ListAccessTokens(ctx context.Context, tenantID, pageID uuid.UUID) (*models.StatusPageAccessTokenListResponse, error) {
	if err := s.ensureStatusPage(ctx, s.db, tenantID, pageID); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+accessTokenColumns+`
		FROM status_page_access_tokens t
		JOIN status_pages sp ON sp.id = t.status_page_id`+accessTokenJoins+`
		WHERE t.status_page_id = $1 AND sp.tenant_id = $2
		ORDER BY t.created_at DESC, t.id
	`, pageID, tenantID)
	if err != nil {
		return nil, fmt.Errorf("failed to list access tokens: %w", err)
	}
	defer rows.Close()
	out := &models.StatusPageAccessTokenListResponse{Items: []models.StatusPageAccessToken{}}
	for rows.Next() {
		t, err := scanAccessToken(rows)
		if err != nil {
			return nil, fmt.Errorf("failed to scan access token: %w", err)
		}
		out.Items = append(out.Items, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate access tokens: %w", err)
	}
	return out, nil
}

// CreateAccessToken mints a kiosk token. The returned value is the only time
// the token exists outside the visitor's browser; the row keeps its hash.
func (s *Service) CreateAccessToken(ctx context.Context, tenantID, pageID uuid.UUID, by TokenCreator, label string) (*models.CreatedStatusPageAccessToken, error) {
	token, hash, err := pageaccess.NewToken()
	if err != nil {
		return nil, err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	// The page row lock serializes concurrent creates, so two cannot both
	// pass the cap.
	var locked uuid.UUID
	if err := tx.QueryRowContext(ctx, `
		SELECT id FROM status_pages WHERE id = $1 AND tenant_id = $2 FOR UPDATE
	`, pageID, tenantID).Scan(&locked); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrStatusPageNotFound
		}
		return nil, fmt.Errorf("failed to lock status page: %w", err)
	}
	var active int
	if err := tx.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM status_page_access_tokens WHERE status_page_id = $1 AND revoked_at IS NULL
	`, pageID).Scan(&active); err != nil {
		return nil, fmt.Errorf("failed to count access tokens: %w", err)
	}
	if active >= MaxActiveAccessTokens {
		return nil, ErrTooManyAccessTokens
	}

	var id uuid.UUID
	if err := tx.QueryRowContext(ctx, `
		INSERT INTO status_page_access_tokens (status_page_id, label, token_hash, created_by_user_id, created_by_api_key_id)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id
	`, pageID, strings.TrimSpace(label), hash, nullableUUID(by.UserID), nullableUUID(by.APIKeyID)).Scan(&id); err != nil {
		return nil, fmt.Errorf("failed to create access token: %w", err)
	}
	created, err := scanAccessToken(tx.QueryRowContext(ctx, `
		SELECT `+accessTokenColumns+`
		FROM status_page_access_tokens t`+accessTokenJoins+`
		WHERE t.id = $1
	`, id))
	if err != nil {
		return nil, fmt.Errorf("failed to read created access token: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("failed to commit transaction: %w", err)
	}
	return &models.CreatedStatusPageAccessToken{StatusPageAccessToken: created, Token: token}, nil
}

// RevokeAccessToken revokes a kiosk token. Sessions opened with it end on
// their next request, and the page's open live streams are closed so they
// reconnect through the gate. Revoking twice is a no-op.
func (s *Service) RevokeAccessToken(ctx context.Context, tenantID, pageID, tokenID uuid.UUID) error {
	var newlyRevoked bool
	err := s.db.QueryRowContext(ctx, `
		UPDATE status_page_access_tokens t
		SET revoked_at = COALESCE(t.revoked_at, NOW())
		FROM status_pages sp
		WHERE t.id = $1 AND t.status_page_id = $2
		  AND sp.id = t.status_page_id AND sp.tenant_id = $3
		RETURNING t.revoked_at = NOW()
	`, tokenID, pageID, tenantID).Scan(&newlyRevoked)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrAccessTokenNotFound
	}
	if err != nil {
		return fmt.Errorf("failed to revoke access token: %w", err)
	}
	if newlyRevoked {
		s.notifyStatusPage(tenantID, pageID, statusupdates.EventStatusPageAccessChanged)
	}
	return nil
}

func nullableUUID(id uuid.UUID) interface{} {
	if id == uuid.Nil {
		return nil
	}
	return id
}
