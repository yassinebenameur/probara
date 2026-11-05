//go:build !unit

package statuspage

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	sharedanalytics "github.com/yassinebenameur/probara/shared/analytics"
	"github.com/yassinebenameur/probara/shared/config"
	"github.com/yassinebenameur/probara/shared/logger"
	"github.com/yassinebenameur/probara/shared/pageaccess"
	"github.com/yassinebenameur/probara/shared/testutil"
)

// The gate against the real schema and the real render path: it runs before
// the slug-keyed render cache, so a page made private after it was cached is
// not served from that cache.
func TestPageAccess_EndToEnd(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "")
	pageID := testutil.InsertStatusPage(ctx, t, dbClient, tenantID, "gated-status", "Gated Status")
	monitorID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "API")
	testutil.AddMonitorToStatusPage(ctx, t, dbClient, pageID, monitorID, 0)
	_, err := dbClient.ExecContext(ctx, `UPDATE status_pages SET settings = '{"enable_push_notifications": true}'::jsonb WHERE id = $1`, pageID)
	require.NoError(t, err)

	svc := NewService(dbClient, sharedanalytics.NewRepository(dbClient))
	h := NewHandlers(svc, &config.StatusPageConfig{SessionSecret: testSessionSecret}, logger.New("status-page", "error"), NewHub(), newRenderCache(time.Minute), nil)
	pushes := newPushStore(dbClient.DB)

	get := func(path string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		for _, c := range cookies {
			req.AddCookie(c)
		}
		rec := httptest.NewRecorder()
		h.HandleStatusPage(rec, req)
		return rec
	}

	// Public: served, cached, and push-capable.
	rec := get("/public/status/gated-status")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Empty(t, rec.Header().Get("X-Robots-Tag"))
	_, pushOK, err := pushes.PageIDForSlug(ctx, "gated-status")
	require.NoError(t, err)
	require.True(t, pushOK)

	acc, err := svc.PageAccess(ctx, "gated-status")
	require.NoError(t, err)
	require.Equal(t, pageaccess.Public, acc.Visibility)
	require.Equal(t, pageID, acc.ID)

	// Password, with no cache invalidation: the cached render is not served.
	hash, err := pageaccess.HashPassword("open sesame")
	require.NoError(t, err)
	_, err = dbClient.ExecContext(ctx, `UPDATE status_pages SET visibility = 'password', password_hash = $2, password_version = 1 WHERE id = $1`, pageID, hash)
	require.NoError(t, err)
	rec = get("/public/status/gated-status")
	require.Equal(t, http.StatusUnauthorized, rec.Code)
	require.NotContains(t, rec.Body.String(), "API")
	require.Equal(t, http.StatusUnauthorized, get("/public/status/gated-status/data").Code)
	_, pushOK, err = pushes.PageIDForSlug(ctx, "gated-status")
	require.NoError(t, err)
	require.False(t, pushOK, "gated pages take no push subscriptions")

	// Unlock, then the page and its data are served, privately.
	form := url.Values{"password": {"open sesame"}}
	req := httptest.NewRequest(http.MethodPost, "/public/status/gated-status/unlock", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec = httptest.NewRecorder()
	h.HandleStatusPage(rec, req)
	require.Equal(t, http.StatusSeeOther, rec.Code)
	session := rec.Result().Cookies()[0]

	rec = get("/public/status/gated-status", session)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "private, no-store", rec.Header().Get("Cache-Control"))
	require.Empty(t, rec.Header().Get("ETag"))
	require.Equal(t, "noindex", rec.Header().Get("X-Robots-Tag"))
	require.Contains(t, rec.Body.String(), `<meta name="robots" content="noindex" />`)
	require.Equal(t, http.StatusOK, get("/public/status/gated-status/data", session).Code)

	// A password change signs the session out.
	_, err = dbClient.ExecContext(ctx, `UPDATE status_pages SET password_version = 2 WHERE id = $1`, pageID)
	require.NoError(t, err)
	require.Equal(t, http.StatusUnauthorized, get("/public/status/gated-status/data", session).Code)

	// Private with a kiosk token.
	token, tokenHash, err := pageaccess.NewToken()
	require.NoError(t, err)
	var tokenID uuid.UUID
	require.NoError(t, dbClient.QueryRowContext(ctx, `
		INSERT INTO status_page_access_tokens (status_page_id, label, token_hash) VALUES ($1, 'Lobby TV', $2) RETURNING id
	`, pageID, tokenHash).Scan(&tokenID))
	_, err = dbClient.ExecContext(ctx, `UPDATE status_pages SET visibility = 'private' WHERE id = $1`, pageID)
	require.NoError(t, err)

	require.Equal(t, http.StatusNotFound, get("/public/status/gated-status").Code)
	rec = get("/public/status/gated-status?token=" + url.QueryEscape(token))
	require.Equal(t, http.StatusOK, rec.Code)
	kiosk := rec.Result().Cookies()[0]
	require.Equal(t, http.StatusOK, get("/public/status/gated-status/data", kiosk).Code)

	// Revoking the token ends the kiosk session at once.
	_, err = dbClient.ExecContext(ctx, `UPDATE status_page_access_tokens SET revoked_at = NOW() WHERE id = $1`, tokenID)
	require.NoError(t, err)
	require.Equal(t, http.StatusNotFound, get("/public/status/gated-status/data", kiosk).Code)
	require.Equal(t, http.StatusNotFound, get("/public/status/gated-status?token="+url.QueryEscape(token)).Code)

	// A deleted workspace's page resolves to nothing.
	_, err = dbClient.ExecContext(ctx, `UPDATE tenants SET deleted_at = NOW() WHERE id = $1`, tenantID)
	require.NoError(t, err)
	_, err = svc.PageAccess(ctx, "gated-status")
	require.ErrorIs(t, err, ErrStatusPageNotFound)
	_, err = svc.PageAccessByID(ctx, pageID)
	require.ErrorIs(t, err, ErrStatusPageNotFound)
}

// The schema refuses a visibility the gate would not know.
func TestPageAccess_VisibilityCheck(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "")
	pageID := testutil.InsertStatusPage(ctx, t, dbClient, tenantID, "checked-status", "Checked")
	_, err := dbClient.ExecContext(ctx, `UPDATE status_pages SET visibility = 'secret' WHERE id = $1`, pageID)
	require.Error(t, err)
}
