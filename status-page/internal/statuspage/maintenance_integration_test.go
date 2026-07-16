package statuspage

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	testcontainers "github.com/testcontainers/testcontainers-go"

	sharedanalytics "github.com/yassinebenameur/probara/shared/analytics"
	shareddb "github.com/yassinebenameur/probara/shared/db"
	"github.com/yassinebenameur/probara/shared/testutil"
)

func insertTestMaintenanceWindow(ctx context.Context, t *testing.T, db *shareddb.Client, tenantID uuid.UUID, title string, startsAt, endsAt time.Time, monitorIDs ...uuid.UUID) uuid.UUID {
	t.Helper()
	windowID := uuid.New()
	if _, err := db.ExecContext(ctx, `
		INSERT INTO maintenance_windows (id, tenant_id, title, description, starts_at, ends_at)
		VALUES ($1, $2, $3, 'window description', $4, $5)
	`, windowID, tenantID, title, startsAt, endsAt); err != nil {
		t.Fatalf("insert maintenance window: %v", err)
	}
	for _, monitorID := range monitorIDs {
		if _, err := db.ExecContext(ctx, `
			INSERT INTO maintenance_window_monitors (maintenance_window_id, monitor_id)
			VALUES ($1, $2)
		`, windowID, monitorID); err != nil {
			t.Fatalf("insert maintenance window monitor: %v", err)
		}
	}
	return windowID
}

// A monitor covered by an active window must render with status "maintenance",
// overriding its persisted state, and the page must list active and upcoming
// windows while excluding windows for off-page monitors and ended windows.
func TestStatusPageMaintenanceWindows(t *testing.T) {
	testcontainers.SkipIfProviderIsNotHealthy(t)

	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	now := time.Now().UTC()
	tenantID := testutil.InsertTenant(ctx, t, dbClient, "status-mw")
	statusPageID := testutil.InsertStatusPage(ctx, t, dbClient, tenantID, "mw-status", "MW Status")

	onPage := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "on-page")
	healthy := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "healthy")
	offPage := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "off-page")
	testutil.AddMonitorToStatusPage(ctx, t, dbClient, statusPageID, onPage, 0)
	testutil.AddMonitorToStatusPage(ctx, t, dbClient, statusPageID, healthy, 1)

	// onPage is DOWN but inside an active maintenance window.
	if _, err := dbClient.ExecContext(ctx,
		`UPDATE monitors SET current_state = 'down' WHERE id = $1`, onPage); err != nil {
		t.Fatalf("set state: %v", err)
	}
	if _, err := dbClient.ExecContext(ctx,
		`UPDATE monitors SET current_state = 'up' WHERE id IN ($1, $2)`, healthy, offPage); err != nil {
		t.Fatalf("set state: %v", err)
	}

	insertTestMaintenanceWindow(ctx, t, dbClient, tenantID, "active window",
		now.Add(-time.Hour), now.Add(time.Hour), onPage)
	insertTestMaintenanceWindow(ctx, t, dbClient, tenantID, "upcoming window",
		now.Add(24*time.Hour), now.Add(26*time.Hour), onPage)
	insertTestMaintenanceWindow(ctx, t, dbClient, tenantID, "off-page window",
		now.Add(-time.Hour), now.Add(time.Hour), offPage)
	insertTestMaintenanceWindow(ctx, t, dbClient, tenantID, "ended window",
		now.Add(-3*time.Hour), now.Add(-2*time.Hour), onPage)
	insertTestMaintenanceWindow(ctx, t, dbClient, tenantID, "far future window",
		now.Add(30*24*time.Hour), now.Add(31*24*time.Hour), onPage)

	svc := NewService(dbClient, sharedanalytics.NewRepository(dbClient))
	page, err := svc.GetStatusPageBySlug(ctx, "mw-status")
	if err != nil {
		t.Fatalf("GetStatusPageBySlug() error = %v", err)
	}

	statusByName := map[string]string{}
	for _, m := range page.Monitors {
		statusByName[m.Name] = m.Status
	}
	if got := statusByName["on-page"]; got != "maintenance" {
		t.Errorf("on-page status = %q, want maintenance (down state must be overridden)", got)
	}
	if got := statusByName["healthy"]; got != "up" {
		t.Errorf("healthy status = %q, want up", got)
	}

	if !page.HasMaintenance {
		t.Errorf("HasMaintenance = false, want true")
	}
	titles := map[string]StatusPageMaintenanceWindow{}
	for _, w := range page.MaintenanceWindows {
		titles[w.Title] = w
	}
	if len(page.MaintenanceWindows) != 2 {
		t.Fatalf("maintenance windows = %d (%v), want 2 (active + upcoming)", len(page.MaintenanceWindows), titles)
	}
	active, ok := titles["active window"]
	if !ok || !active.IsActive {
		t.Errorf("active window missing or not flagged active: %+v", titles)
	}
	upcoming, ok := titles["upcoming window"]
	if !ok || upcoming.IsActive {
		t.Errorf("upcoming window missing or wrongly active: %+v", titles)
	}
	if len(active.AffectedMonitors) != 1 || active.AffectedMonitors[0] != "on-page" {
		t.Errorf("active window affected = %v, want [on-page]", active.AffectedMonitors)
	}

	// Maintenance must not flag the page as having issues.
	if page.HasIssues {
		t.Errorf("HasIssues = true, want false (maintenance is not an issue)")
	}
}

// A window targeting a group covers the group's members on the page.
func TestStatusPageMaintenanceGroupExpansion(t *testing.T) {
	testcontainers.SkipIfProviderIsNotHealthy(t)

	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	now := time.Now().UTC()
	tenantID := testutil.InsertTenant(ctx, t, dbClient, "status-mw-group")
	statusPageID := testutil.InsertStatusPage(ctx, t, dbClient, tenantID, "mw-group", "MW Group")

	memberID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "member")
	groupID := testutil.InsertGroupMonitor(ctx, t, dbClient, tenantID, "the-group")
	testutil.AddMonitorToGroup(ctx, t, dbClient, memberID, groupID)
	testutil.AddMonitorToStatusPage(ctx, t, dbClient, statusPageID, memberID, 0)

	insertTestMaintenanceWindow(ctx, t, dbClient, tenantID, "group window",
		now.Add(-time.Hour), now.Add(time.Hour), groupID)

	svc := NewService(dbClient, sharedanalytics.NewRepository(dbClient))
	page, err := svc.GetStatusPageBySlug(ctx, "mw-group")
	if err != nil {
		t.Fatalf("GetStatusPageBySlug() error = %v", err)
	}

	var memberStatus string
	for _, m := range page.Monitors {
		if m.Name == "member" {
			memberStatus = m.Status
		}
	}
	if memberStatus != "maintenance" {
		t.Errorf("member status = %q, want maintenance via group-targeted window", memberStatus)
	}
	if len(page.MaintenanceWindows) != 1 {
		t.Fatalf("maintenance windows = %d, want 1", len(page.MaintenanceWindows))
	}
	if got := page.MaintenanceWindows[0].AffectedMonitors; len(got) != 1 || got[0] != "member" {
		t.Errorf("affected monitors = %v, want [member] (page-scoped names)", got)
	}
}
