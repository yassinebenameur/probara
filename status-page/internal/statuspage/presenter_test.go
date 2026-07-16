package statuspage

import (
	"context"
	"testing"
	"time"

	testcontainers "github.com/testcontainers/testcontainers-go"

	"github.com/google/uuid"
	sharedanalytics "github.com/yassinebenameur/probara/shared/analytics"
	"github.com/yassinebenameur/probara/shared/testutil"
)

func TestMonitorPresenter_ConfigureExtractsMonitorURLs(t *testing.T) {
	svc := &Service{presenters: newMonitorPresenters()}

	tests := []struct {
		name        string
		monitorType string
		configJSON  []byte
		wantURL     string
	}{
		{name: "http", monitorType: "http", configJSON: []byte(`{"url":"https://example.com"}`), wantURL: "https://example.com"},
		{name: "ping", monitorType: "ping", configJSON: []byte(`{"host":"example.com"}`), wantURL: "example.com"},
		{name: "dns", monitorType: "dns", configJSON: []byte(`{"host":"dns.example.com"}`), wantURL: "dns.example.com"},
		{name: "sip", monitorType: "sip", configJSON: []byte(`{"host":"sip.example.com","port":5061}`), wantURL: "sip.example.com:5061"},
		{name: "tcp", monitorType: "tcp", configJSON: []byte(`{"host":"db.example.com","port":5432}`), wantURL: "db.example.com:5432"},
		{name: "tcp without port", monitorType: "tcp", configJSON: []byte(`{"host":"db.example.com"}`), wantURL: "db.example.com"},
		{name: "agent", monitorType: "agent", configJSON: nil, wantURL: "System Agent"},
		{name: "push", monitorType: "push", configJSON: nil, wantURL: "Push Monitor"},
		{name: "group", monitorType: "group", configJSON: nil, wantURL: "Group Monitor"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			monitor := &MonitorStatus{}
			svc.monitorPresenter(tt.monitorType).Configure(monitor, tt.configJSON)
			if monitor.URL != tt.wantURL {
				t.Fatalf("URL = %q, want %q", monitor.URL, tt.wantURL)
			}
		})
	}
}

func TestMonitorPresenter_UnknownTypeFallsBackSafely(t *testing.T) {
	svc := &Service{presenters: newMonitorPresenters()}
	monitor := &MonitorStatus{}

	svc.monitorPresenter("totally_unknown_type").Configure(monitor, []byte(`{"host":"ignored"}`))

	if monitor.URL != "" {
		t.Fatalf("URL = %q, want empty string", monitor.URL)
	}
}

func TestService_GetStatusPageMonitors_GroupPresenterAggregatesNestedGroups(t *testing.T) {
	testcontainers.SkipIfProviderIsNotHealthy(t)

	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	analyticsRepo := sharedanalytics.NewRepository(dbClient)
	svc := NewService(dbClient, analyticsRepo)
	now := time.Now().UTC()
	svc.now = func() time.Time { return now }

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "status-group")
	statusPageID := testutil.InsertStatusPage(ctx, t, dbClient, tenantID, "group-status", "Group Status")
	parentGroupID := testutil.InsertGroupMonitor(ctx, t, dbClient, tenantID, "parent")
	childGroupID := testutil.InsertGroupMonitor(ctx, t, dbClient, tenantID, "child")
	monitorA := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "leaf-a")
	monitorB := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "leaf-b")

	testutil.AddMonitorToGroup(ctx, t, dbClient, monitorA, childGroupID)
	testutil.AddMonitorToGroup(ctx, t, dbClient, monitorB, parentGroupID)
	testutil.AddMonitorToGroup(ctx, t, dbClient, childGroupID, parentGroupID)
	testutil.AddMonitorToStatusPage(ctx, t, dbClient, statusPageID, parentGroupID, 0)

	testutil.InsertCheckResult(ctx, t, dbClient, tenantID, monitorA, now.Add(-5*time.Minute), "success", "monitor", testutil.IntPtr(120))
	testutil.InsertCheckResult(ctx, t, dbClient, tenantID, monitorB, now.Add(-2*time.Minute), "failure", "monitor", nil)

	monitors, err := svc.GetStatusPageMonitors(ctx, statusPageID, tenantID)
	if err != nil {
		t.Fatalf("GetStatusPageMonitors() error = %v", err)
	}
	if len(monitors) != 1 {
		t.Fatalf("monitor count = %d, want 1", len(monitors))
	}
	if monitors[0].URL != "Group Monitor" {
		t.Fatalf("group URL = %q, want Group Monitor", monitors[0].URL)
	}
	if monitors[0].Status != "degraded" {
		t.Fatalf("group status = %q, want degraded", monitors[0].Status)
	}
}

func TestService_ResolveStatusPageOperationalMonitorIDs_GroupPresenterReturnsLeaves(t *testing.T) {
	testcontainers.SkipIfProviderIsNotHealthy(t)

	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	svc := NewService(dbClient, sharedanalytics.NewRepository(dbClient))
	tenantID := testutil.InsertTenant(ctx, t, dbClient, "status-operational")
	statusPageID := testutil.InsertStatusPage(ctx, t, dbClient, tenantID, "operational-status", "Operational Status")
	parentGroupID := testutil.InsertGroupMonitor(ctx, t, dbClient, tenantID, "parent")
	childGroupID := testutil.InsertGroupMonitor(ctx, t, dbClient, tenantID, "child")
	leafA := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "leaf-a")
	leafB := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "leaf-b")

	testutil.AddMonitorToGroup(ctx, t, dbClient, leafA, childGroupID)
	testutil.AddMonitorToGroup(ctx, t, dbClient, childGroupID, parentGroupID)
	testutil.AddMonitorToGroup(ctx, t, dbClient, leafB, parentGroupID)
	testutil.AddMonitorToStatusPage(ctx, t, dbClient, statusPageID, parentGroupID, 0)

	ids, err := svc.resolveStatusPageOperationalMonitorIDs(ctx, statusPageID, tenantID)
	if err != nil {
		t.Fatalf("resolveStatusPageOperationalMonitorIDs() error = %v", err)
	}
	if len(ids) != 2 {
		t.Fatalf("operational monitor count = %d, want 2", len(ids))
	}
	want := map[uuid.UUID]bool{leafA: true, leafB: true}
	for _, id := range ids {
		if !want[id] {
			t.Fatalf("unexpected operational ID %s", id)
		}
	}
}
