package statuspage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/shared/tenancy"
)

// pageAccess is the gate's view of a page: everything authorize needs and
// nothing it does not, read before any page data.
type pageAccess struct {
	ID              uuid.UUID
	Title           string
	Visibility      string
	PasswordHash    string
	PasswordVersion int
}

// accessStore is what the gate reads. *Service implements it through its
// read-only Querier; tests substitute a fake.
type accessStore interface {
	PageAccess(ctx context.Context, slug string) (*pageAccess, error)
	PageAccessByID(ctx context.Context, id uuid.UUID) (*pageAccess, error)
	// ActiveTokenID returns the id of the page's unrevoked kiosk token with
	// this hash.
	ActiveTokenID(ctx context.Context, pageID uuid.UUID, tokenHash string) (uuid.UUID, bool, error)
	// TokenActive reports whether a kiosk token still opens the page.
	TokenActive(ctx context.Context, pageID, tokenID uuid.UUID) (bool, error)
}

var _ accessStore = (*Service)(nil)

// pageAccessQuery is shared by both lookups: the first %s is the key
// predicate, the second the live-workspace check, so a deleted workspace's
// pages resolve to nothing, like GetStatusPageBySlug.
const pageAccessQuery = `
	SELECT sp.id, sp.title, sp.visibility, COALESCE(sp.password_hash, ''), sp.password_version
	FROM status_pages sp
	WHERE %s AND %s`

// PageAccess loads the gate fields for a slug. ErrStatusPageNotFound when no
// live page has it.
func (s *Service) PageAccess(ctx context.Context, slug string) (*pageAccess, error) {
	return s.loadPageAccess(ctx, "sp.slug = $1", slug)
}

// PageAccessByID loads the gate fields for a page id.
func (s *Service) PageAccessByID(ctx context.Context, id uuid.UUID) (*pageAccess, error) {
	return s.loadPageAccess(ctx, "sp.id = $1", id)
}

func (s *Service) loadPageAccess(ctx context.Context, where string, arg any) (*pageAccess, error) {
	query := fmt.Sprintf(pageAccessQuery, where, tenancy.LiveTenantSQL("sp.tenant_id"))
	var a pageAccess
	err := s.db.QueryRowContext(ctx, query, arg).Scan(&a.ID, &a.Title, &a.Visibility, &a.PasswordHash, &a.PasswordVersion)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrStatusPageNotFound
		}
		return nil, fmt.Errorf("load status page access: %w", err)
	}
	return &a, nil
}

// ActiveTokenID resolves a presented kiosk token. The lookup is by hash, so
// the token itself never reaches the database or a log.
func (s *Service) ActiveTokenID(ctx context.Context, pageID uuid.UUID, tokenHash string) (uuid.UUID, bool, error) {
	var id uuid.UUID
	err := s.db.QueryRowContext(ctx, `
		SELECT id FROM status_page_access_tokens
		WHERE status_page_id = $1 AND token_hash = $2 AND revoked_at IS NULL
	`, pageID, tokenHash).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return uuid.Nil, false, nil
	}
	if err != nil {
		return uuid.Nil, false, fmt.Errorf("resolve kiosk token: %w", err)
	}
	return id, true, nil
}

// TokenActive reports whether a token session's token is still live. Run on
// every request of a token session, so revoking a token takes effect at once.
func (s *Service) TokenActive(ctx context.Context, pageID, tokenID uuid.UUID) (bool, error) {
	var ok bool
	err := s.db.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM status_page_access_tokens
			WHERE id = $1 AND status_page_id = $2 AND revoked_at IS NULL
		)
	`, tokenID, pageID).Scan(&ok)
	if err != nil {
		return false, fmt.Errorf("check kiosk token: %w", err)
	}
	return ok, nil
}
