package statuspages

import (
	"context"
	"testing"

	testcontainers "github.com/testcontainers/testcontainers-go"

	"github.com/google/uuid"
	"github.com/yassinebenameur/probara/shared/testutil"
)

func TestService_ListStatusPages_FallsBackToLegacyMonitorsWhenSectionMonitorTableMissing(t *testing.T) {
	testcontainers.SkipIfProviderIsNotHealthy(t)

	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "status-pages-legacy-fallback")
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

	svc := NewService(dbClient)
	result, err := svc.ListStatusPages(ctx, tenantID, 1, 20)
	if err != nil {
		t.Fatalf("ListStatusPages() error = %v", err)
	}

	if len(result.Items) != 1 {
		t.Fatalf("ListStatusPages() returned %d items, want 1", len(result.Items))
	}
	if got := result.Items[0].MonitorIDs; len(got) != 1 || got[0] != monitorID {
		t.Fatalf("ListStatusPages() monitor_ids = %v, want [%s]", got, monitorID)
	}
}
