package statuspage

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	testcontainers "github.com/testcontainers/testcontainers-go"

	sharedanalytics "github.com/yassinebenameur/probara/shared/analytics"
	"github.com/yassinebenameur/probara/shared/testutil"
)

func TestService_GetStatusPageBySlug_LongRangeParityMatchesSharedAnalytics(t *testing.T) {
	testcontainers.SkipIfProviderIsNotHealthy(t)

	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	svc := NewService(dbClient)
	repo := sharedanalytics.NewRepository(dbClient)
	tenantID := testutil.InsertTenant(ctx, t, dbClient, "status-page-rollup")
	monitorA := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "monitor-a")
	monitorB := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "monitor-b")
	statusPageID := testutil.InsertStatusPage(ctx, t, dbClient, tenantID, "rollup-status", "Rollup Status")
	testutil.AddMonitorToStatusPage(ctx, t, dbClient, statusPageID, monitorA, 0)
	testutil.AddMonitorToStatusPage(ctx, t, dbClient, statusPageID, monitorB, 1)

	now := time.Date(2026, time.March, 6, 12, 0, 0, 0, time.UTC)
	svc.now = func() time.Time { return now }
	day1 := time.Date(2026, time.March, 5, 0, 0, 0, 0, time.UTC)
	day2 := time.Date(2026, time.March, 6, 0, 0, 0, 0, time.UTC)
	testutil.InsertDailyRollup(ctx, t, dbClient, tenantID, monitorA, day1, 10, 8, 1000, 10, "failure", day1.Add(22*time.Hour))
	testutil.InsertDailyRollup(ctx, t, dbClient, tenantID, monitorA, day2, 10, 10, 2000, 10, "success", day2.Add(11*time.Hour))
	testutil.InsertDailyRollup(ctx, t, dbClient, tenantID, monitorB, day1, 10, 5, 3000, 10, "failure", day1.Add(21*time.Hour))
	testutil.InsertDowntimePeriod(ctx, t, dbClient, tenantID, monitorA, day1.Add(8*time.Hour), day1.Add(9*time.Hour))
	testutil.InsertOpenDowntime(ctx, t, dbClient, tenantID, monitorA, day2.Add(10*time.Hour), day2.Add(11*time.Hour))

	page, err := svc.GetStatusPageBySlug(ctx, "rollup-status")
	if err != nil {
		t.Fatalf("GetStatusPageBySlug() error = %v", err)
	}

	monitor := findMonitorStatus(t, page.Monitors, monitorA)

	for _, rangeValue := range []sharedanalytics.Range{sharedanalytics.Range30d, sharedanalytics.Range90d, sharedanalytics.Range365d} {
		t.Run(string(rangeValue), func(t *testing.T) {
			expectedMonitor, err := repo.GetScopeAnalytics(ctx, tenantID, []uuid.UUID{monitorA}, rangeValue, now)
			if err != nil {
				t.Fatalf("GetScopeAnalytics(monitor, %s) error = %v", rangeValue, err)
			}
			expectedGlobal, err := repo.GetScopeAnalytics(ctx, tenantID, []uuid.UUID{monitorA, monitorB}, rangeValue, now)
			if err != nil {
				t.Fatalf("GetScopeAnalytics(global, %s) error = %v", rangeValue, err)
			}

			expectedMonitorDaily := mapDailySeries(expectedMonitor.Series)
			expectedMonitorLatency := mapLatencySeries(expectedMonitor.Series, rangeValue)
			expectedMonitorDowntime := mapDowntimePeriods(expectedMonitor.Downtime)
			expectedGlobalDaily := mapDailySeries(expectedGlobal.Series)

			switch rangeValue {
			case sharedanalytics.Range30d:
				assertDeepEqual(t, monitor.UptimeHistory30d, expectedMonitorDaily)
				assertDeepEqual(t, monitor.LatencyHistory30d, expectedMonitorLatency)
				assertDeepEqual(t, monitor.DowntimePeriods30d, expectedMonitorDowntime)
				assertDeepEqual(t, page.UptimeHistory30, expectedGlobalDaily)
			case sharedanalytics.Range90d:
				assertDeepEqual(t, monitor.UptimeHistory90d, expectedMonitorDaily)
				assertDeepEqual(t, monitor.LatencyHistory90d, expectedMonitorLatency)
				assertDeepEqual(t, monitor.DowntimePeriods90d, expectedMonitorDowntime)
				assertDeepEqual(t, page.UptimeHistory90, expectedGlobalDaily)
			case sharedanalytics.Range365d:
				assertDeepEqual(t, monitor.UptimeHistory365d, expectedMonitorDaily)
				assertDeepEqual(t, monitor.LatencyHistory365d, expectedMonitorLatency)
				assertDeepEqual(t, monitor.DowntimePeriods365d, expectedMonitorDowntime)
				assertDeepEqual(t, page.UptimeHistory365, expectedGlobalDaily)
			}
		})
	}
}

func findMonitorStatus(t *testing.T, monitors []MonitorStatus, monitorID uuid.UUID) MonitorStatus {
	t.Helper()
	for _, monitor := range monitors {
		if monitor.ID == monitorID.String() {
			return monitor
		}
	}
	t.Fatalf("monitor %s not found on status page", monitorID)
	return MonitorStatus{}
}

func assertDeepEqual(t *testing.T, got, want interface{}) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("value mismatch\ngot:  %#v\nwant: %#v", got, want)
	}
}
