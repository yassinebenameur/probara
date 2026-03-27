package statuspage

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/google/uuid"

	sharedanalytics "github.com/yassinebenameur/probara/shared/analytics"
	"github.com/yassinebenameur/probara/shared/testutil"
)

type incidentTestExecutor interface {
	ExecContext(ctx context.Context, query string, args ...interface{}) (sql.Result, error)
}

func TestGetStatusPageBySlugIncludesPublishedIncidents(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "status-page-incidents")
	statusPageID := testutil.InsertStatusPage(ctx, t, dbClient, tenantID, "status", "Status")
	monitorID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "API")
	testutil.AddMonitorToStatusPage(ctx, t, dbClient, statusPageID, monitorID, 0)
	insertPublishedIncident(ctx, t, dbClient, tenantID, statusPageID, monitorID)

	svc := NewService(dbClient, sharedanalytics.NewRepository(dbClient))
	page, err := svc.GetStatusPageBySlug(ctx, "status")
	if err != nil {
		t.Fatalf("GetStatusPageBySlug() error = %v", err)
	}
	if len(page.Incidents) != 1 {
		t.Fatalf("incidents len = %d, want 1", len(page.Incidents))
	}
	if page.Incidents[0].Title != "API outage" {
		t.Fatalf("incident title = %q, want %q", page.Incidents[0].Title, "API outage")
	}
	if len(page.Incidents[0].AffectedComponents) != 1 || page.Incidents[0].AffectedComponents[0] != "API" {
		t.Fatalf("affected components = %v, want [API]", page.Incidents[0].AffectedComponents)
	}
	if len(page.Incidents[0].Updates) != 1 || page.Incidents[0].Updates[0].Message != "We are investigating elevated API errors." {
		t.Fatalf("updates = %#v, want one public update", page.Incidents[0].Updates)
	}
}

func insertPublishedIncident(ctx context.Context, t *testing.T, db incidentTestExecutor, tenantID, statusPageID, monitorID uuid.UUID) {
	t.Helper()

	incidentID := uuid.New()
	publishedAt := time.Date(2026, time.March, 27, 17, 0, 0, 0, time.UTC)

	if _, err := db.ExecContext(ctx, `
		INSERT INTO incidents (
			id, tenant_id, title, summary, state, created_at, updated_at
		) VALUES ($1, $2, 'API outage', 'Requests are failing across regions.', 'investigating', $3, $3)
	`, incidentID, tenantID, publishedAt); err != nil {
		t.Fatalf("insert incident: %v", err)
	}

	if _, err := db.ExecContext(ctx, `
		INSERT INTO incident_status_page_publications (
			incident_id, status_page_id, tenant_id, published_at, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $4, $4)
	`, incidentID, statusPageID, tenantID, publishedAt); err != nil {
		t.Fatalf("insert incident publication: %v", err)
	}

	if _, err := db.ExecContext(ctx, `
		INSERT INTO incident_status_page_monitors (
			incident_id, status_page_id, monitor_id, created_at
		) VALUES ($1, $2, $3, $4)
	`, incidentID, statusPageID, monitorID, publishedAt); err != nil {
		t.Fatalf("insert incident status page monitor: %v", err)
	}

	if _, err := db.ExecContext(ctx, `
		INSERT INTO incident_timeline_entries (
			id, tenant_id, incident_id, entry_type, message, metadata, created_at
		) VALUES ($1, $2, $3, 'public_update', 'We are investigating elevated API errors.', '{}'::jsonb, $4)
	`, uuid.New(), tenantID, incidentID, publishedAt.Add(5*time.Minute)); err != nil {
		t.Fatalf("insert incident public update: %v", err)
	}
}
