//go:build !unit

package statuspage

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	sharedanalytics "github.com/yassinebenameur/probara/shared/analytics"
	"github.com/yassinebenameur/probara/shared/config"
	"github.com/yassinebenameur/probara/shared/logger"
	"github.com/yassinebenameur/probara/shared/pageaccess"
	"github.com/yassinebenameur/probara/shared/testutil"
)

// A deleted workspace's status pages are not served (by slug, preview or
// ID), take no push subscriptions, and its queued visitor pushes are never
// claimed for sending.
func TestDeletedWorkspaceStatusPagesAreGone(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	live := testutil.InsertTenant(ctx, t, dbClient, "live")
	deleted := testutil.InsertTenant(ctx, t, dbClient, "deleted")
	testutil.InsertStatusPage(ctx, t, dbClient, live, "live-page", "Live")
	deletedPage := testutil.InsertStatusPage(ctx, t, dbClient, deleted, "gone-page", "Gone")
	monitor := testutil.InsertHTTPMonitor(ctx, t, dbClient, deleted, "api")
	if _, err := dbClient.ExecContext(ctx, `
		UPDATE status_pages SET settings = '{"enable_push_notifications": true}'::jsonb WHERE id = $1;`, deletedPage); err != nil {
		t.Fatal(err)
	}
	if _, err := dbClient.ExecContext(ctx, `
		INSERT INTO status_page_push_deliveries (status_page_id, monitor_id, interval_id, kind, monitor_name)
		VALUES ($1, $2, $3, 'down', 'api')`, deletedPage, monitor, uuid.New()); err != nil {
		t.Fatal(err)
	}
	if _, err := dbClient.ExecContext(ctx, `UPDATE tenants SET deleted_at = NOW() WHERE id = $1`, deleted); err != nil {
		t.Fatal(err)
	}

	svc := NewService(dbClient, sharedanalytics.NewRepository(dbClient))
	if _, err := svc.GetStatusPageBySlug(ctx, "live-page"); err != nil {
		t.Fatalf("live page: %v", err)
	}
	if _, err := svc.GetStatusPageBySlug(ctx, "gone-page"); !errors.Is(err, ErrStatusPageNotFound) {
		t.Fatalf("deleted workspace page: err = %v, want not found", err)
	}
	if _, _, _, err := svc.GetDraftTemplateBySlug(ctx, "gone-page"); !errors.Is(err, ErrStatusPageNotFound) {
		t.Fatalf("deleted workspace preview: err = %v, want not found", err)
	}

	store := newPushStore(dbClient.DB)
	if id, _, err := store.PageIDForSlug(ctx, "gone-page"); err != nil || id != uuid.Nil {
		t.Fatalf("push subscribe lookup = %v, %v; want no page", id, err)
	}
	claimed, err := store.ClaimDeliveries(ctx, 10, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if len(claimed) != 0 {
		t.Fatalf("claimed %d pushes of a deleted workspace", len(claimed))
	}
}

// Through the real gate and handlers: once its workspace is deleted, a page
// cached while live answers exactly like a missing slug on every sub-route
// (the add-ons and the unlock form included), and its kiosk tokens and
// visitor sessions stop working at once.
func TestDeletedWorkspacePagesAreMissingOnEveryRoute(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "doomed")
	monitorID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "Checkout API")
	publicID := testutil.InsertStatusPage(ctx, t, dbClient, tenantID, "doomed-public", "Doomed")
	kioskID := testutil.InsertStatusPage(ctx, t, dbClient, tenantID, "doomed-kiosk", "Doomed Kiosk")
	lockedID := testutil.InsertStatusPage(ctx, t, dbClient, tenantID, "doomed-locked", "Doomed Locked")
	for _, page := range []uuid.UUID{publicID, kioskID, lockedID} {
		testutil.AddMonitorToStatusPage(ctx, t, dbClient, page, monitorID, 0)
	}
	hash, err := pageaccess.HashPassword("visitor-password")
	if err != nil {
		t.Fatal(err)
	}
	token, tokenHash, _ := pageaccess.NewToken()
	for _, q := range []struct {
		sql  string
		args []interface{}
	}{
		{`UPDATE status_pages SET visibility = 'private' WHERE id = $1`, []interface{}{kioskID}},
		{`UPDATE status_pages SET visibility = 'password', password_hash = $1, password_version = 1 WHERE id = $2`, []interface{}{hash, lockedID}},
		{`INSERT INTO status_page_access_tokens (status_page_id, label, token_hash) VALUES ($1, 'Lobby', $2)`, []interface{}{kioskID, tokenHash}},
	} {
		if _, err := dbClient.ExecContext(ctx, q.sql, q.args...); err != nil {
			t.Fatal(err)
		}
	}

	svc := NewService(dbClient, sharedanalytics.NewRepository(dbClient))
	h := NewHandlers(svc, &config.StatusPageConfig{SessionSecret: testSessionSecret}, logger.New("status-page", "error"), NewHub(), newRenderCache(time.Hour), nil)
	do := func(req *http.Request) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		h.HandleStatusPage(rec, req)
		return rec
	}
	get := func(path string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		for _, c := range cookies {
			req.AddCookie(c)
		}
		return do(req)
	}

	// While the workspace is live: the public page and its add-ons are
	// rendered and cached, and the kiosk token opens a session.
	for _, p := range []string{"", "/summary.json", "/feed.atom", "/badge.svg"} {
		if rec := get("/public/status/doomed-public" + p); rec.Code != http.StatusOK {
			t.Fatalf("live %s = %d", p, rec.Code)
		}
	}
	rec := get("/public/status/doomed-kiosk?token=" + url.QueryEscape(token))
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("live kiosk token = %d", rec.Code)
	}
	session := findCookie(t, rec, sessionCookieName)
	if page := get("/public/status/doomed-kiosk", session); page.Code != http.StatusOK {
		t.Fatalf("live kiosk session = %d", page.Code)
	}

	if _, err := dbClient.ExecContext(ctx, `UPDATE tenants SET deleted_at = NOW() WHERE id = $1`, tenantID); err != nil {
		t.Fatal(err)
	}

	for _, slug := range []string{"doomed-public", "doomed-kiosk", "doomed-locked"} {
		for _, row := range routeTable {
			if row.method == "" {
				continue
			}
			for _, method := range []string{row.method, http.MethodGet, http.MethodPost} {
				a := do(notFoundRequest(method, "/public/status/"+slug+row.route))
				b := do(notFoundRequest(method, "/public/status/no-such-page"+row.route))
				// The subscription link landing carries a fresh CSP nonce per
				// answer; only that may differ.
				if a.Code != b.Code || withoutNonce(a.Body.String()) != withoutNonce(b.Body.String()) ||
					!equalHeaders(nonceFree(a.Header()), nonceFree(b.Header())) {
					t.Errorf("%s %s%s: deleted %d %v %q / missing %d %v %q", method, slug, row.route,
						a.Code, a.Header(), a.Body.String(), b.Code, b.Header(), b.Body.String())
				}
				if strings.Contains(a.Body.String(), "Checkout API") {
					t.Errorf("%s %s%s: a deleted workspace's page served content", method, slug, row.route)
				}
			}
		}
	}
	if rec := get("/public/status/doomed-kiosk?token=" + url.QueryEscape(token)); rec.Code != http.StatusNotFound {
		t.Fatalf("kiosk token of a deleted workspace = %d, want 404", rec.Code)
	}
	if rec := get("/public/status/doomed-kiosk", session); rec.Code != http.StatusNotFound {
		t.Fatalf("visitor session of a deleted workspace = %d, want 404", rec.Code)
	}
}
