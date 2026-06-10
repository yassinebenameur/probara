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

func TestGetStatusPageBySlugBatchesMultipleIncidentsPreservingOrdering(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "status-page-incidents-multi")
	statusPageID := testutil.InsertStatusPage(ctx, t, dbClient, tenantID, "status-multi", "Status Multi")

	// Insert monitors deliberately out of alphabetical order to exercise
	// per-incident component ordering (m.name ASC).
	cdnID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "CDN")
	apiID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "API")
	billingID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "Billing")
	testutil.AddMonitorToStatusPage(ctx, t, dbClient, statusPageID, cdnID, 0)
	testutil.AddMonitorToStatusPage(ctx, t, dbClient, statusPageID, apiID, 1)
	testutil.AddMonitorToStatusPage(ctx, t, dbClient, statusPageID, billingID, 2)

	base := time.Date(2026, time.March, 27, 12, 0, 0, 0, time.UTC)

	// Unresolved, most recently updated -> expected first.
	insertIncidentFixture(ctx, t, dbClient, incidentFixture{
		tenantID:     tenantID,
		statusPageID: statusPageID,
		title:        "Database outage",
		state:        "investigating",
		updatedAt:    base.Add(2 * time.Hour),
		publishedAt:  base,
		monitorIDs:   []uuid.UUID{billingID, apiID},
		updates: []incidentUpdateFixture{
			{message: "First update", createdAt: base.Add(10 * time.Minute)},
			{message: "Second update", createdAt: base.Add(20 * time.Minute)},
		},
	})

	// Unresolved, older update -> expected second.
	insertIncidentFixture(ctx, t, dbClient, incidentFixture{
		tenantID:     tenantID,
		statusPageID: statusPageID,
		title:        "CDN degradation",
		state:        "monitoring",
		updatedAt:    base.Add(1 * time.Hour),
		publishedAt:  base,
		monitorIDs:   []uuid.UUID{cdnID},
		updates:      nil,
	})

	// Resolved (even though updated most recently) -> expected last.
	resolvedAt := base.Add(3 * time.Hour)
	insertIncidentFixture(ctx, t, dbClient, incidentFixture{
		tenantID:     tenantID,
		statusPageID: statusPageID,
		title:        "Resolved blip",
		state:        "resolved",
		updatedAt:    base.Add(3 * time.Hour),
		resolvedAt:   &resolvedAt,
		publishedAt:  base,
		monitorIDs:   []uuid.UUID{cdnID, apiID, billingID},
		updates: []incidentUpdateFixture{
			{message: "Investigating", createdAt: base.Add(1 * time.Minute)},
			{message: "Identified", createdAt: base.Add(2 * time.Minute)},
			{message: "Resolved", createdAt: base.Add(3 * time.Minute)},
		},
	})

	svc := NewService(dbClient, sharedanalytics.NewRepository(dbClient))
	page, err := svc.GetStatusPageBySlug(ctx, "status-multi")
	if err != nil {
		t.Fatalf("GetStatusPageBySlug() error = %v", err)
	}
	if len(page.Incidents) != 3 {
		t.Fatalf("incidents len = %d, want 3", len(page.Incidents))
	}

	wantTitles := []string{"Database outage", "CDN degradation", "Resolved blip"}
	for i, want := range wantTitles {
		if page.Incidents[i].Title != want {
			t.Fatalf("incident[%d].Title = %q, want %q", i, page.Incidents[i].Title, want)
		}
	}

	wantComponents := [][]string{
		{"API", "Billing"},
		{"CDN"},
		{"API", "Billing", "CDN"},
	}
	for i, want := range wantComponents {
		got := page.Incidents[i].AffectedComponents
		if got == nil {
			t.Fatalf("incident[%d].AffectedComponents is nil, want %v", i, want)
		}
		if len(got) != len(want) {
			t.Fatalf("incident[%d].AffectedComponents = %v, want %v", i, got, want)
		}
		for j := range want {
			if got[j] != want[j] {
				t.Fatalf("incident[%d].AffectedComponents = %v, want %v", i, got, want)
			}
		}
	}

	wantUpdates := [][]string{
		{"Second update", "First update"}, // created_at DESC
		{},
		{"Resolved", "Identified", "Investigating"},
	}
	for i, want := range wantUpdates {
		got := page.Incidents[i].Updates
		if got == nil {
			t.Fatalf("incident[%d].Updates is nil, want %v", i, want)
		}
		if len(got) != len(want) {
			t.Fatalf("incident[%d].Updates = %#v, want messages %v", i, got, want)
		}
		for j := range want {
			if got[j].Message != want[j] {
				t.Fatalf("incident[%d].Updates[%d].Message = %q, want %q", i, j, got[j].Message, want[j])
			}
		}
	}

	if page.Incidents[2].ResolvedAt == nil || !page.Incidents[2].ResolvedAt.Equal(resolvedAt) {
		t.Fatalf("incident[2].ResolvedAt = %v, want %v", page.Incidents[2].ResolvedAt, resolvedAt)
	}
}

type incidentUpdateFixture struct {
	message   string
	createdAt time.Time
}

type incidentFixture struct {
	tenantID     uuid.UUID
	statusPageID uuid.UUID
	title        string
	state        string
	updatedAt    time.Time
	resolvedAt   *time.Time
	publishedAt  time.Time
	monitorIDs   []uuid.UUID
	updates      []incidentUpdateFixture
}

func insertIncidentFixture(ctx context.Context, t *testing.T, db incidentTestExecutor, fixture incidentFixture) {
	t.Helper()

	incidentID := uuid.New()
	if _, err := db.ExecContext(ctx, `
		INSERT INTO incidents (
			id, tenant_id, title, summary, state, resolved_at, created_at, updated_at
		) VALUES ($1, $2, $3, '', $4, $5, $6, $7)
	`, incidentID, fixture.tenantID, fixture.title, fixture.state, fixture.resolvedAt, fixture.publishedAt, fixture.updatedAt); err != nil {
		t.Fatalf("insert incident: %v", err)
	}

	if _, err := db.ExecContext(ctx, `
		INSERT INTO incident_status_page_publications (
			incident_id, status_page_id, tenant_id, published_at, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $4, $4)
	`, incidentID, fixture.statusPageID, fixture.tenantID, fixture.publishedAt); err != nil {
		t.Fatalf("insert incident publication: %v", err)
	}

	for _, monitorID := range fixture.monitorIDs {
		if _, err := db.ExecContext(ctx, `
			INSERT INTO incident_status_page_monitors (
				incident_id, status_page_id, monitor_id, created_at
			) VALUES ($1, $2, $3, $4)
		`, incidentID, fixture.statusPageID, monitorID, fixture.publishedAt); err != nil {
			t.Fatalf("insert incident status page monitor: %v", err)
		}
	}

	for _, update := range fixture.updates {
		if _, err := db.ExecContext(ctx, `
			INSERT INTO incident_timeline_entries (
				id, tenant_id, incident_id, entry_type, message, metadata, created_at
			) VALUES ($1, $2, $3, 'public_update', $4, '{}'::jsonb, $5)
		`, uuid.New(), fixture.tenantID, incidentID, update.message, update.createdAt); err != nil {
			t.Fatalf("insert incident public update: %v", err)
		}
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
