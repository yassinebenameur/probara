//go:build !unit

package statuspage

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	testcontainers "github.com/testcontainers/testcontainers-go"

	sharedanalytics "github.com/yassinebenameur/probara/shared/analytics"
	"github.com/yassinebenameur/probara/shared/config"
	"github.com/yassinebenameur/probara/shared/logger"
	"github.com/yassinebenameur/probara/shared/testutil"
)

// An automatically created incident's own title and summary name the
// internal monitor; its publication carries a public title (the API sets
// it). The page, /data, the feed, summary.json and visitor push show only
// the public one, never the internal name or narrative.
func TestAutomaticIncidentShowsOnlyItsPublicTitle(t *testing.T) {
	testcontainers.SkipIfProviderIsNotHealthy(t)
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "public-title")
	pageID := testutil.InsertStatusPage(ctx, t, dbClient, tenantID, "acme", "Acme Status")
	monitorID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "db-primary-eu-west-internal")
	testutil.AddMonitorToStatusPage(ctx, t, dbClient, pageID, monitorID, 0)
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := dbClient.ExecContext(ctx, q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	exec(`UPDATE status_page_monitors SET display_name = 'Database' WHERE status_page_id = $1`, pageID)
	exec(`UPDATE status_pages SET settings = '{"enable_push_notifications": true}'::jsonb WHERE id = $1`, pageID)
	incidentID := uuid.New()
	exec(`INSERT INTO incidents (id, tenant_id, title, summary, state, is_auto_created, auto_monitor_id)
		VALUES ($1, $2, 'db-primary-eu-west-internal incident', 'Automatically created from a firing alert.', 'investigating', TRUE, $3)`,
		incidentID, tenantID, monitorID)
	exec(`INSERT INTO incident_status_page_publications (incident_id, status_page_id, tenant_id, auto_published, public_title, public_summary)
		VALUES ($1, $2, $3, TRUE, 'Service disruption: Database', 'We are seeing a disruption on the components listed below and are looking into it.')`,
		incidentID, pageID, tenantID)
	exec(`INSERT INTO incident_status_page_monitors (incident_id, status_page_id, monitor_id) VALUES ($1, $2, $3)`, incidentID, pageID, monitorID)

	svc := NewService(dbClient, sharedanalytics.NewRepository(dbClient))
	h := NewHandlers(svc, &config.StatusPageConfig{StatusPageBaseURL: "https://status.example.com"}, logger.New("status-page", "error"), NewHub(), newRenderCache(time.Minute), nil)
	for _, route := range []string{"", "/data", "/feed.atom", "/summary.json"} {
		rec := httptest.NewRecorder()
		h.HandleStatusPage(rec, httptest.NewRequest(http.MethodGet, "/public/status/acme"+route, nil))
		body := rec.Body.String()
		if rec.Code != http.StatusOK {
			t.Fatalf("%q = %d", route, rec.Code)
		}
		for _, leak := range []string{"db-primary-eu-west-internal", "Automatically created from a firing alert"} {
			if strings.Contains(body, leak) {
				t.Errorf("%q shows %q", route, leak)
			}
		}
		if route != "/summary.json" && !strings.Contains(body, "Service disruption: Database") {
			t.Errorf("%q does not show the public title", route)
		}
	}

	// Push: the delivery ledger's label is the public title.
	store := newPushStore(dbClient.DB)
	if _, err := store.Subscribe(ctx, pageID, pushSubscriptionRow{
		Endpoint: "https://fcm.googleapis.com/fcm/send/public-title",
		P256dh:   "BCVxsr7N_eNgVRqvHtD0zTZsEc6-VV-JvLexhqUzORcxaOzi6-AYWXvTBHm4bjyPjs7Vd8pZGH6SRpkNtoIAiw4",
		Auth:     "BTBZMqHH6r4Tts7J_aSIgg",
	}, "test", 10); err != nil {
		t.Fatal(err)
	}
	exec(`INSERT INTO status_page_notification_outbox (tenant_id, status_page_id, event_kind, incident_id, dedupe_key)
		VALUES ($1, $2, 'incident_published', $3, 'k')`, tenantID, pageID, incidentID)
	if _, err := store.reconcileOutboxNotifications(ctx); err != nil {
		t.Fatal(err)
	}
	claimed, err := store.ClaimDeliveries(ctx, 10, time.Minute)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("claimed %v %v", claimed, err)
	}
	payload := (&pushSender{cfg: &config.StatusPageConfig{}}).payloadFor(claimed[0])
	if payload.Body != "Incident: Service disruption: Database" || strings.Contains(payload.Body+payload.Title, "internal") {
		t.Fatalf("push payload = %+v", payload)
	}
}
