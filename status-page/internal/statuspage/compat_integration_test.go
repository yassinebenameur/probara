package statuspage

import (
	"context"
	"testing"

	testcontainers "github.com/testcontainers/testcontainers-go"

	"github.com/google/uuid"
	sharedanalytics "github.com/yassinebenameur/probara/shared/analytics"
	"github.com/yassinebenameur/probara/shared/testutil"
)

func TestService_GetStatusPageSections_FallsBackToLegacyMonitorsWhenSectionMonitorTableMissing(t *testing.T) {
	testcontainers.SkipIfProviderIsNotHealthy(t)

	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "status-page-legacy-fallback")
	monitorID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "legacy-monitor")
	statusPageID := testutil.InsertStatusPage(ctx, t, dbClient, tenantID, "legacy-status", "Legacy Status")
	testutil.AddMonitorToStatusPage(ctx, t, dbClient, statusPageID, monitorID, 0)

	sectionID := uuid.New()
	if _, err := dbClient.ExecContext(ctx, `
		INSERT INTO status_page_sections (id, status_page_id, title, position, created_at, updated_at)
		VALUES ($1, $2, 'Services', 0, NOW(), NOW())
	`, sectionID, statusPageID); err != nil {
		t.Fatalf("insert status page section: %v", err)
	}
	if _, err := dbClient.ExecContext(ctx, `DROP TABLE status_page_section_monitors`); err != nil {
		t.Fatalf("drop status_page_section_monitors: %v", err)
	}

	svc := NewService(dbClient, sharedanalytics.NewRepository(dbClient))
	sections, err := svc.GetStatusPageSections(ctx, statusPageID, tenantID)
	if err != nil {
		t.Fatalf("GetStatusPageSections() error = %v", err)
	}

	if len(sections) != 1 {
		t.Fatalf("GetStatusPageSections() returned %d sections, want 1", len(sections))
	}
	if got := sections[0].Monitors; len(got) != 1 || got[0].ID != monitorID.String() {
		t.Fatalf("GetStatusPageSections() monitors = %#v, want [%s]", got, monitorID)
	}
}
