package statuspage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/shared/db/pgerr"
	"github.com/yassinebenameur/probara/shared/tenancy"
)

// pageAccess is the gate's view of a page: everything authorize and the
// unlock form need and no page data, read before anything else.
type pageAccess struct {
	ID              uuid.UUID
	Title           string
	LogoURL         string
	PrimaryColor    string
	Visibility      string
	PasswordHash    string
	PasswordVersion int
	// PresentedTokenID is the live kiosk token of this page whose hash was
	// asked about (uuid.Nil when none matches or none was asked about).
	PresentedTokenID uuid.UUID
	// SessionTokenLive reports whether the token a token session names is
	// still a live token of this page.
	SessionTokenLive bool
}

// accessQuery is what the gate needs. *Service implements it through its
// read-only Querier; tests substitute a fake.
type accessQuery interface {
	// PageAccess loads a page's gate fields by slug, and in the same round
	// trip resolves a presented kiosk token (by hash, "" for none) and the
	// liveness of a session's token (uuid.Nil for none), so a missing slug
	// and a private one cost the same. ErrStatusPageNotFound when no page has
	// the slug.
	PageAccess(ctx context.Context, slug, presentedTokenHash string, sessionTokenID uuid.UUID) (*pageAccess, error)
}

var _ accessQuery = (*Service)(nil)

// A deleted workspace's page is not found by this lookup, so every
// sub-route, the unlock form and open streams (stillAuthorized) answer it
// exactly like a missing slug, and its visitor sessions and kiosk tokens
// stop working the moment the workspace is deleted.
var pageAccessQuery = `
	SELECT sp.id, sp.title, COALESCE(sp.logo_url, ''), COALESCE(sp.primary_color, ''),
	       sp.visibility, COALESCE(sp.password_hash, ''), sp.password_version,
	       (SELECT t.id FROM status_page_access_tokens t
	         WHERE t.status_page_id = sp.id AND t.token_hash = $2 AND t.revoked_at IS NULL),
	       EXISTS (SELECT 1 FROM status_page_access_tokens t
	         WHERE t.id = $3 AND t.status_page_id = sp.id AND t.revoked_at IS NULL)
	FROM status_pages sp
	WHERE sp.slug = $1 AND ` + tenancy.LiveTenantSQL("sp.tenant_id")

// PageAccess implements accessQuery. The presented token is looked up by
// hash, so the token itself never reaches the database or a log.
func (s *Service) PageAccess(ctx context.Context, slug, presentedTokenHash string, sessionTokenID uuid.UUID) (*pageAccess, error) {
	var sessionArg interface{}
	if sessionTokenID != uuid.Nil {
		sessionArg = sessionTokenID
	}
	var a pageAccess
	var presented uuid.NullUUID
	err := s.db.QueryRowContext(ctx, pageAccessQuery, slug, presentedTokenHash, sessionArg).Scan(
		&a.ID, &a.Title, &a.LogoURL, &a.PrimaryColor,
		&a.Visibility, &a.PasswordHash, &a.PasswordVersion,
		&presented, &a.SessionTokenLive,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrStatusPageNotFound
		}
		return nil, fmt.Errorf("load status page access: %w", err)
	}
	if presented.Valid {
		a.PresentedTokenID = presented.UUID
	}
	return &a, nil
}

// HasGatedPages reports whether any page is password or private, for the
// startup warning about unconfigured trusted proxies. False before migration
// 000114, when none can be.
func (s *Service) HasGatedPages(ctx context.Context) (bool, error) {
	var gated bool
	err := s.db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM status_pages WHERE visibility IN ('password', 'private'))`).Scan(&gated)
	if err != nil && pgerr.IsMissingSchema(err) {
		return false, nil
	}
	return gated, err
}
