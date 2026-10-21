//go:build !unit

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

	repo := sharedanalytics.NewRepository(dbClient)
	svc := NewService(dbClient, repo)
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

	for _, rangeValue := range []sharedanalytics.Range{sharedanalytics.Range7d, sharedanalytics.Range30d, sharedanalytics.Range90d, sharedanalytics.Range365d} {
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
			case sharedanalytics.Range7d:
				assertDeepEqual(t, monitor.UptimeHistory7d, expectedMonitorDaily)
				assertDeepEqual(t, page.UptimeHistory7, expectedGlobalDaily)
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

func TestService_GetStatusPageBySlug_GlobalUptimeUsesOperationalLeafMonitorIDsForGroups(t *testing.T) {
	testcontainers.SkipIfProviderIsNotHealthy(t)

	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	repo := sharedanalytics.NewRepository(dbClient)
	svc := NewService(dbClient, repo)
	tenantID := testutil.InsertTenant(ctx, t, dbClient, "status-page-group-global-uptime")
	leafMonitorID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "leaf-monitor")
	groupMonitorID := testutil.InsertGroupMonitor(ctx, t, dbClient, tenantID, "leaf-group")
	testutil.AddMonitorToGroup(ctx, t, dbClient, leafMonitorID, groupMonitorID)

	statusPageID := testutil.InsertStatusPage(ctx, t, dbClient, tenantID, "group-status", "Group Status")
	testutil.AddMonitorToStatusPage(ctx, t, dbClient, statusPageID, groupMonitorID, 0)

	now := time.Now().UTC().Truncate(time.Minute)
	analyticsNow := time.Date(now.Year(), now.Month(), now.Day(), 12, 0, 0, 0, time.UTC)
	svc.now = func() time.Time { return analyticsNow }

	successLatency := 120
	failureLatency := 900
	testutil.InsertCheckResult(ctx, t, dbClient, tenantID, leafMonitorID, now.Add(-65*time.Minute), "success", "monitor", &successLatency)
	testutil.InsertCheckResult(ctx, t, dbClient, tenantID, leafMonitorID, now.Add(-25*time.Minute), "success", "monitor", &successLatency)
	testutil.InsertCheckResult(ctx, t, dbClient, tenantID, leafMonitorID, now.Add(-5*time.Minute), "failure", "monitor", &failureLatency)

	day1 := analyticsNow.AddDate(0, 0, -1)
	day2 := analyticsNow
	testutil.InsertDailyRollup(ctx, t, dbClient, tenantID, leafMonitorID, day1, 10, 9, 900, 9, "success", day1.Add(20*time.Hour))
	testutil.InsertDailyRollup(ctx, t, dbClient, tenantID, leafMonitorID, day2, 10, 7, 700, 7, "failure", day2.Add(10*time.Hour))

	page, err := svc.GetStatusPageBySlug(ctx, "group-status")
	if err != nil {
		t.Fatalf("GetStatusPageBySlug() error = %v", err)
	}

	expected1h, err := svc.GetGlobalMinuteUptime(ctx, []uuid.UUID{leafMonitorID}, tenantID)
	if err != nil {
		t.Fatalf("GetGlobalMinuteUptime() error = %v", err)
	}
	assertDeepEqual(t, page.UptimeHistory1h, expected1h)

	expected24h, err := svc.GetMonitorHourlyUptime(ctx, leafMonitorID, tenantID)
	if err != nil {
		t.Fatalf("GetMonitorHourlyUptime() error = %v", err)
	}
	assertHourlySeriesEqual(t, page.UptimeHistory1, expected24h)

	expected7d, err := repo.GetScopeAnalytics(ctx, tenantID, []uuid.UUID{leafMonitorID}, sharedanalytics.Range7d, analyticsNow)
	if err != nil {
		t.Fatalf("GetScopeAnalytics(global 7d) error = %v", err)
	}
	expectedDaily := mapDailySeries(expected7d.Series)
	assertDeepEqual(t, page.UptimeHistory7, expectedDaily)

	groupMonitor := findMonitorStatus(t, page.Monitors, groupMonitorID)
	assertDeepEqual(t, groupMonitor.UptimeHistory7d, expectedDaily)
}

// The 1h strip is 60 one-minute buckets from raw check_results. A group's
// strip sums its members' checks per minute, the page-level strip sums every
// operational leaf, and platform-source results (freshness failures) are
// excluded from all of them.
func TestService_GetStatusPageBySlug_OneHourStripUsesMinuteBuckets(t *testing.T) {
	testcontainers.SkipIfProviderIsNotHealthy(t)

	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	svc := NewService(dbClient, sharedanalytics.NewRepository(dbClient))
	now := time.Now().UTC()
	svc.now = func() time.Time { return now }

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "status-page-1h")
	memberA := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "member-a")
	memberB := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "member-b")
	leafC := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "leaf-c")
	groupID := testutil.InsertGroupMonitor(ctx, t, dbClient, tenantID, "group")
	testutil.AddMonitorToGroup(ctx, t, dbClient, memberA, groupID)
	testutil.AddMonitorToGroup(ctx, t, dbClient, memberB, groupID)
	statusPageID := testutil.InsertStatusPage(ctx, t, dbClient, tenantID, "one-hour", "One Hour")
	testutil.AddMonitorToStatusPage(ctx, t, dbClient, statusPageID, groupID, 0)
	testutil.AddMonitorToStatusPage(ctx, t, dbClient, statusPageID, leafC, 1)

	recent := now.Add(-3 * time.Minute)
	older := now.Add(-40 * time.Minute)
	testutil.InsertCheckResult(ctx, t, dbClient, tenantID, memberA, recent, "success", "monitor", testutil.IntPtr(100))
	testutil.InsertCheckResult(ctx, t, dbClient, tenantID, memberA, recent, "failure", "platform", nil)
	testutil.InsertCheckResult(ctx, t, dbClient, tenantID, memberB, recent, "failure", "monitor", nil)
	testutil.InsertCheckResult(ctx, t, dbClient, tenantID, leafC, older, "failure", "monitor", nil)
	testutil.InsertCheckResult(ctx, t, dbClient, tenantID, leafC, now.Add(-2*time.Hour), "success", "monitor", testutil.IntPtr(90))

	page, err := svc.GetStatusPageBySlug(ctx, "one-hour")
	if err != nil {
		t.Fatalf("GetStatusPageBySlug() error = %v", err)
	}

	start, _ := oneHourWindow(now)
	bucket := func(at time.Time) int { return int(at.Truncate(time.Minute).Sub(start) / time.Minute) }
	uptimeAt := func(series []MinuteUptime, idx int) float64 {
		t.Helper()
		if len(series) != 60 {
			t.Fatalf("1h series has %d buckets, want 60", len(series))
		}
		return series[idx].Uptime
	}

	group := findMonitorStatus(t, page.Monitors, groupID)
	leaf := findMonitorStatus(t, page.Monitors, leafC)
	if got := uptimeAt(group.UptimeHistory1h, bucket(recent)); got != 50 {
		t.Fatalf("group 1h bucket = %v, want 50 (one success, one failure; platform row excluded)", got)
	}
	if got := uptimeAt(leaf.UptimeHistory1h, bucket(older)); got != 0 {
		t.Fatalf("leaf 1h bucket = %v, want 0", got)
	}
	if got := uptimeAt(leaf.UptimeHistory1h, bucket(recent)); got != -1 {
		t.Fatalf("leaf 1h empty bucket = %v, want -1", got)
	}
	if got := uptimeAt(page.UptimeHistory1h, bucket(recent)); got != 50 {
		t.Fatalf("global 1h bucket = %v, want 50", got)
	}
	if got := uptimeAt(page.UptimeHistory1h, bucket(older)); got != 0 {
		t.Fatalf("global 1h bucket = %v, want 0", got)
	}
	if want := now.Truncate(time.Minute).Format("15:04"); page.UptimeHistory1h[59].Time != want {
		t.Fatalf("newest 1h bucket = %q, want the current minute %q", page.UptimeHistory1h[59].Time, want)
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

func assertHourlySeriesEqual(t *testing.T, got, want []HourlyUptime) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("hourly history length mismatch\ngot:  %#v\nwant: %#v", got, want)
	}
	for i := range got {
		if got[i].Uptime != want[i].Uptime {
			t.Fatalf("hourly uptime mismatch at index %d\ngot:  %#v\nwant: %#v", i, got, want)
		}
	}
}

func assertDeepEqual(t *testing.T, got, want interface{}) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("value mismatch\ngot:  %#v\nwant: %#v", got, want)
	}
}
