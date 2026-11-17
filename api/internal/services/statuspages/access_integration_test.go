//go:build !unit

package statuspages

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	testcontainers "github.com/testcontainers/testcontainers-go"

	"github.com/yassinebenameur/probara/api/internal/models"
	"github.com/yassinebenameur/probara/shared/pageaccess"
	"github.com/yassinebenameur/probara/shared/statusupdates"
	"github.com/yassinebenameur/probara/shared/testutil"
)

type spyPublisher struct {
	mu     sync.Mutex
	events []statusupdates.Event
}

func (p *spyPublisher) Publish(e statusupdates.Event) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.events = append(p.events, e)
	return nil
}

func (p *spyPublisher) last() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.events) == 0 {
		return ""
	}
	return p.events[len(p.events)-1].Type
}

func TestService_PageAccess(t *testing.T) {
	testcontainers.SkipIfProviderIsNotHealthy(t)

	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "status-page-access-api")
	otherTenant := testutil.InsertTenant(ctx, t, dbClient, "status-page-access-other")
	pub := &spyPublisher{}
	svc := NewService(dbClient, nil)
	svc.publisher = pub

	stored := func(pageID uuid.UUID) (visibility string, hash *string, version int) {
		t.Helper()
		if err := dbClient.QueryRowContext(ctx, `SELECT visibility, password_hash, password_version FROM status_pages WHERE id = $1`, pageID).
			Scan(&visibility, &hash, &version); err != nil {
			t.Fatal(err)
		}
		return
	}

	t.Run("create", func(t *testing.T) {
		if _, err := svc.CreateStatusPage(ctx, tenantID, &models.CreateStatusPageRequest{
			Slug: "needs-password", Title: "x", Visibility: strPtr(pageaccess.Password),
		}); !errors.Is(err, ErrPasswordRequired) {
			t.Fatalf("password page without a password: err = %v", err)
		}
		page, err := svc.CreateStatusPage(ctx, tenantID, &models.CreateStatusPageRequest{
			Slug: "with-password", Title: "x", Visibility: strPtr(pageaccess.Password), Password: strPtr("visitor-pass"),
		})
		if err != nil {
			t.Fatal(err)
		}
		if page.Visibility != pageaccess.Password || !page.HasPassword {
			t.Fatalf("created page = %+v", page)
		}
		_, hash, version := stored(page.ID)
		if hash == nil || *hash == "visitor-pass" || !pageaccess.CheckPassword(*hash, "visitor-pass") || version != 1 {
			t.Fatalf("stored hash %v version %d", hash, version)
		}
		plain, err := svc.CreateStatusPage(ctx, tenantID, &models.CreateStatusPageRequest{Slug: "plain", Title: "x"})
		if err != nil || plain.Visibility != pageaccess.Public || plain.HasPassword {
			t.Fatalf("default page = %+v %v", plain, err)
		}
	})

	page, err := svc.CreateStatusPage(ctx, tenantID, &models.CreateStatusPageRequest{Slug: "access", Title: "Access"})
	if err != nil {
		t.Fatal(err)
	}

	t.Run("update", func(t *testing.T) {
		if _, err := svc.UpdateStatusPage(ctx, tenantID, page.ID, &models.UpdateStatusPageRequest{Visibility: strPtr(pageaccess.Password)}); !errors.Is(err, ErrPasswordRequired) {
			t.Fatalf("password visibility without a password: err = %v", err)
		}
		if v, _, _ := stored(page.ID); v != pageaccess.Public {
			t.Fatalf("refused update still changed visibility to %q", v)
		}

		got, err := svc.UpdateStatusPage(ctx, tenantID, page.ID, &models.UpdateStatusPageRequest{
			Visibility: strPtr(pageaccess.Password), Password: strPtr("first-password"),
		})
		if err != nil || got.Visibility != pageaccess.Password || !got.HasPassword {
			t.Fatalf("set password page = %+v %v", got, err)
		}
		if pub.last() != statusupdates.EventStatusPageAccessChanged {
			t.Fatalf("event = %q, want an access change", pub.last())
		}
		_, _, v1 := stored(page.ID)

		// A title edit is not an access change.
		if _, err := svc.UpdateStatusPage(ctx, tenantID, page.ID, &models.UpdateStatusPageRequest{Title: strPtr("Renamed")}); err != nil {
			t.Fatal(err)
		}
		if pub.last() != "status_page_updated" {
			t.Fatalf("event = %q, want status_page_updated", pub.last())
		}
		// Resending the same visibility is not one either.
		if _, err := svc.UpdateStatusPage(ctx, tenantID, page.ID, &models.UpdateStatusPageRequest{Visibility: strPtr(pageaccess.Password)}); err != nil {
			t.Fatal(err)
		}
		if pub.last() != "status_page_updated" {
			t.Fatalf("event = %q for an unchanged visibility", pub.last())
		}

		if _, err := svc.UpdateStatusPage(ctx, tenantID, page.ID, &models.UpdateStatusPageRequest{Password: strPtr("second-password")}); err != nil {
			t.Fatal(err)
		}
		_, hash, v2 := stored(page.ID)
		if v2 != v1+1 || !pageaccess.CheckPassword(*hash, "second-password") {
			t.Fatalf("password change: version %d → %d", v1, v2)
		}

		if _, err := svc.UpdateStatusPage(ctx, tenantID, page.ID, &models.UpdateStatusPageRequest{Password: strPtr("")}); !errors.Is(err, ErrPasswordRequired) {
			t.Fatalf("clearing the password of a password page: err = %v", err)
		}
		// Leaving password visibility keeps the stored hash, so switching
		// back needs no new password; clearing it is then allowed.
		if got, err := svc.UpdateStatusPage(ctx, tenantID, page.ID, &models.UpdateStatusPageRequest{Visibility: strPtr(pageaccess.Private)}); err != nil || !got.HasPassword {
			t.Fatalf("to private = %+v %v", got, err)
		}
		got, err = svc.UpdateStatusPage(ctx, tenantID, page.ID, &models.UpdateStatusPageRequest{Password: strPtr("")})
		if err != nil || got.HasPassword {
			t.Fatalf("clear on a private page = %+v %v", got, err)
		}
		if _, hash, v3 := stored(page.ID); hash != nil || v3 != v2+1 {
			t.Fatalf("after clear: hash %v version %d", hash, v3)
		}

		if _, err := svc.UpdateStatusPage(ctx, otherTenant, page.ID, &models.UpdateStatusPageRequest{Visibility: strPtr(pageaccess.Public)}); !errors.Is(err, ErrStatusPageNotFound) {
			t.Fatalf("other tenant update: err = %v", err)
		}

		list, err := svc.ListStatusPages(ctx, tenantID, 1, 50)
		if err != nil {
			t.Fatal(err)
		}
		for _, item := range list.Items {
			if item.ID == page.ID && item.Visibility != pageaccess.Private {
				t.Fatalf("list visibility = %q", item.Visibility)
			}
		}
	})

	t.Run("kiosk tokens", func(t *testing.T) {
		var userID, keyID uuid.UUID
		if err := dbClient.QueryRowContext(ctx, `INSERT INTO admin_users (username, password_hash) VALUES ('wall-admin', 'x') RETURNING id`).Scan(&userID); err != nil {
			t.Fatal(err)
		}
		if err := dbClient.QueryRowContext(ctx, `INSERT INTO api_keys (tenant_id, name, key_hash) VALUES ($1, 'ci-key', 'h') RETURNING id`, tenantID).Scan(&keyID); err != nil {
			t.Fatal(err)
		}

		created, err := svc.CreateAccessToken(ctx, tenantID, page.ID, TokenCreator{UserID: userID}, "  Lobby TV ")
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(created.Token, pageaccess.TokenPrefix) || created.Label != "Lobby TV" ||
			created.CreatedBy != "wall-admin" || created.CreatedByUserID == nil || *created.CreatedByUserID != userID {
			t.Fatalf("created = %+v", created)
		}
		var storedHash string
		if err := dbClient.QueryRowContext(ctx, `SELECT token_hash FROM status_page_access_tokens WHERE id = $1`, created.ID).Scan(&storedHash); err != nil {
			t.Fatal(err)
		}
		if storedHash != pageaccess.HashToken(created.Token) {
			t.Fatal("stored value is not the token's hash")
		}
		byKey, err := svc.CreateAccessToken(ctx, tenantID, page.ID, TokenCreator{APIKeyID: keyID}, "Ops board")
		if err != nil || byKey.CreatedBy != "ci-key" {
			t.Fatalf("key-created token = %+v %v", byKey, err)
		}

		list, err := svc.ListAccessTokens(ctx, tenantID, page.ID)
		if err != nil || len(list.Items) != 2 || list.Items[0].ID != byKey.ID {
			t.Fatalf("list = %+v %v", list, err)
		}

		if err := svc.RevokeAccessToken(ctx, otherTenant, page.ID, created.ID); !errors.Is(err, ErrAccessTokenNotFound) {
			t.Fatalf("other tenant revoke: err = %v", err)
		}
		if _, err := svc.ListAccessTokens(ctx, otherTenant, page.ID); !errors.Is(err, ErrStatusPageNotFound) {
			t.Fatalf("other tenant list: err = %v", err)
		}
		if _, err := svc.CreateAccessToken(ctx, otherTenant, page.ID, TokenCreator{}, "x"); !errors.Is(err, ErrStatusPageNotFound) {
			t.Fatalf("other tenant create: err = %v", err)
		}

		pub.events = nil
		if err := svc.RevokeAccessToken(ctx, tenantID, page.ID, created.ID); err != nil {
			t.Fatal(err)
		}
		if pub.last() != statusupdates.EventStatusPageAccessChanged {
			t.Fatalf("revoke event = %q", pub.last())
		}
		pub.events = nil
		if err := svc.RevokeAccessToken(ctx, tenantID, page.ID, created.ID); err != nil {
			t.Fatalf("second revoke: %v", err)
		}
		if pub.last() != "" {
			t.Fatal("a repeated revoke published again")
		}
		list, _ = svc.ListAccessTokens(ctx, tenantID, page.ID)
		for _, item := range list.Items {
			if item.ID == created.ID && item.RevokedAt == nil {
				t.Fatal("revoked token listed as live")
			}
		}
		if err := svc.RevokeAccessToken(ctx, tenantID, page.ID, uuid.New()); !errors.Is(err, ErrAccessTokenNotFound) {
			t.Fatalf("unknown token: err = %v", err)
		}

		// The cap counts active tokens only.
		for i := 1; i < MaxActiveAccessTokens; i++ {
			if _, err := svc.CreateAccessToken(ctx, tenantID, page.ID, TokenCreator{}, "t"); err != nil {
				t.Fatalf("token %d: %v", i, err)
			}
		}
		if _, err := svc.CreateAccessToken(ctx, tenantID, page.ID, TokenCreator{}, "one too many"); !errors.Is(err, ErrTooManyAccessTokens) {
			t.Fatalf("over the cap: err = %v", err)
		}

		// Deleting the page drops its tokens.
		if err := svc.DeleteStatusPage(ctx, tenantID, page.ID); err != nil {
			t.Fatal(err)
		}
		var left int
		if err := dbClient.QueryRowContext(ctx, `SELECT COUNT(*) FROM status_page_access_tokens WHERE status_page_id = $1`, page.ID).Scan(&left); err != nil || left != 0 {
			t.Fatalf("tokens left after page delete: %d %v", left, err)
		}
	})
}
