//go:build !unit

package statuspage

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	testcontainers "github.com/testcontainers/testcontainers-go"

	sharedanalytics "github.com/yassinebenameur/probara/shared/analytics"
	"github.com/yassinebenameur/probara/shared/config"
	"github.com/yassinebenameur/probara/shared/logger"
	"github.com/yassinebenameur/probara/shared/testutil"
)

// TestHandleStatusPageCSP drives the real handler against stored settings:
// the nonce in the body matches the header on every 200, each response gets
// a fresh one, a 304 carries no CSP (the browser keeps the header that
// matches its cached body), and a settings change reaches the header and the
// ETag.
func TestHandleStatusPageCSP(t *testing.T) {
	testcontainers.SkipIfProviderIsNotHealthy(t)

	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "status-page-csp")
	statusPageID := testutil.InsertStatusPage(ctx, t, dbClient, tenantID, "csp-status", "CSP Status")
	monitorID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "API")
	testutil.AddMonitorToStatusPage(ctx, t, dbClient, statusPageID, monitorID, 0)
	if _, err := dbClient.ExecContext(ctx,
		`UPDATE status_pages SET settings = $2 WHERE id = $1`, statusPageID,
		`{"allowed_embed_origins":["https://intranet.example.com"]}`); err != nil {
		t.Fatal(err)
	}

	svc := NewService(dbClient, sharedanalytics.NewRepository(dbClient))
	cache := newRenderCache(time.Minute)
	cfg := &config.StatusPageConfig{AppBaseURL: "https://ops.example.com"}
	h := NewHandlers(svc, cfg, logger.New("status-page", "error"), NewHub(), cache, nil)

	get := func(ifNoneMatch string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/public/status/csp-status", nil)
		if ifNoneMatch != "" {
			req.Header.Set("If-None-Match", ifNoneMatch)
		}
		rec := httptest.NewRecorder()
		h.HandleStatusPage(rec, req)
		return rec
	}
	nonceOf := func(rec *httptest.ResponseRecorder) string {
		t.Helper()
		m := headerNonce.FindStringSubmatch(rec.Header().Get("Content-Security-Policy"))
		if m == nil {
			t.Fatalf("no strict nonce policy: %q", rec.Header().Get("Content-Security-Policy"))
		}
		if !strings.Contains(rec.Body.String(), `<script nonce="`+m[1]+`">`) {
			t.Fatalf("body does not carry the header nonce %q", m[1])
		}
		return m[1]
	}

	first := get("")
	if first.Code != http.StatusOK {
		t.Fatalf("status = %d", first.Code)
	}
	n1 := nonceOf(first)
	policy := first.Header().Get("Content-Security-Policy")
	if !strings.Contains(policy, "frame-ancestors 'self' https://ops.example.com https://intranet.example.com") {
		t.Fatalf("frame-ancestors: %q", policy)
	}

	second := get("")
	if n2 := nonceOf(second); n2 == n1 {
		t.Fatal("two responses shared a nonce")
	}
	etag := first.Header().Get("ETag")
	if second.Header().Get("ETag") != etag {
		t.Fatal("the nonce must not change the ETag")
	}

	notModified := get(etag)
	if notModified.Code != http.StatusNotModified {
		t.Fatalf("status = %d, want 304", notModified.Code)
	}
	if got := notModified.Header().Get("Content-Security-Policy"); got != "" {
		t.Fatalf("304 carried a CSP (%q); its nonce would not match the cached body", got)
	}

	// Switching the page to off changes the header and the validator.
	if _, err := dbClient.ExecContext(ctx,
		`UPDATE status_pages SET settings = $2 WHERE id = $1`, statusPageID, `{"csp_mode":"off"}`); err != nil {
		t.Fatal(err)
	}
	cache.Invalidate("csp-status")
	off := get(etag)
	if off.Code != http.StatusOK {
		t.Fatalf("status after the policy change = %d, want 200 (a new ETag)", off.Code)
	}
	if got := off.Header().Get("Content-Security-Policy"); got != "frame-ancestors 'self' https://ops.example.com" {
		t.Fatalf("off policy = %q", got)
	}
}
