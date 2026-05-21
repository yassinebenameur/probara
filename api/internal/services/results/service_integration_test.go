package results

import (
	"context"
	"math"
	"testing"
	"time"

	testcontainers "github.com/testcontainers/testcontainers-go"

	"github.com/yassinebenameur/probara/api/internal/models"
	groupservice "github.com/yassinebenameur/probara/api/internal/services/groups"
	sharedanalytics "github.com/yassinebenameur/probara/shared/analytics"
	"github.com/yassinebenameur/probara/shared/testutil"
)

func TestService_GetMonitorAnalytics_SelectsRawVsRollupSource(t *testing.T) {
	testcontainers.SkipIfProviderIsNotHealthy(t)

	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	groupSvc := groupservice.NewService(dbClient)
	analyticsRepo := sharedanalytics.NewRepository(dbClient)
	svc := NewService(dbClient, groupSvc, analyticsRepo)
	tenantID := testutil.InsertTenant(ctx, t, dbClient, "results-source-selection")
	monitorID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "monitor-a")
	now := time.Now().UTC()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)

	testutil.InsertCheckResult(ctx, t, dbClient, tenantID, monitorID, now.Add(-40*time.Minute), "success", "monitor", testutil.IntPtr(110))
	testutil.InsertCheckResult(ctx, t, dbClient, tenantID, monitorID, now.Add(-20*time.Minute), "failure", "monitor", nil)
	testutil.InsertCheckResult(ctx, t, dbClient, tenantID, monitorID, now.Add(-10*time.Minute), "success", "monitor", testutil.IntPtr(210))
	testutil.InsertDailyRollup(ctx, t, dbClient, tenantID, monitorID, today.AddDate(0, 0, -2), 10, 9, 900, 9, "success", today.AddDate(0, 0, -2).Add(23*time.Hour))
	testutil.InsertDailyRollup(ctx, t, dbClient, tenantID, monitorID, today.AddDate(0, 0, -1), 10, 10, 1200, 10, "success", today.AddDate(0, 0, -1).Add(23*time.Hour))
	testutil.InsertDowntimePeriod(ctx, t, dbClient, tenantID, monitorID, today.AddDate(0, 0, -2).Add(8*time.Hour), today.AddDate(0, 0, -2).Add(9*time.Hour))

	rawResp, err := svc.GetMonitorAnalytics(ctx, tenantID, monitorID, models.MonitorAnalyticsRange24h)
	if err != nil {
		t.Fatalf("GetMonitorAnalytics(24h) error = %v", err)
	}
	if rawResp.Source != models.AnalyticsSourceRaw {
		t.Fatalf("24h source = %s, want %s", rawResp.Source, models.AnalyticsSourceRaw)
	}
	if rawResp.Summary.P95LatencyMS == nil {
		t.Fatalf("24h P95LatencyMS = nil, want value")
	}

	rollupResp, err := svc.GetMonitorAnalytics(ctx, tenantID, monitorID, models.MonitorAnalyticsRange90d)
	if err != nil {
		t.Fatalf("GetMonitorAnalytics(90d) error = %v", err)
	}
	if rollupResp.Source != models.AnalyticsSourceRollup {
		t.Fatalf("90d source = %s, want %s", rollupResp.Source, models.AnalyticsSourceRollup)
	}
	if rollupResp.Summary.P95LatencyMS != nil {
		t.Fatalf("90d P95LatencyMS = %v, want nil", rollupResp.Summary.P95LatencyMS)
	}
	if len(rollupResp.DowntimePeriods) != 1 {
		t.Fatalf("90d downtime periods = %d, want 1", len(rollupResp.DowntimePeriods))
	}
}

func TestService_GetMonitorAnalytics_GroupRollupParityMatchesLeafMonitorAverage(t *testing.T) {
	testcontainers.SkipIfProviderIsNotHealthy(t)

	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	groupSvc := groupservice.NewService(dbClient)
	analyticsRepo := sharedanalytics.NewRepository(dbClient)
	svc := NewService(dbClient, groupSvc, analyticsRepo)
	tenantID := testutil.InsertTenant(ctx, t, dbClient, "results-group-parity")
	monitorA := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "monitor-a")
	monitorB := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "monitor-b")
	groupID := testutil.InsertGroupMonitor(ctx, t, dbClient, tenantID, "group-ab")
	testutil.AddMonitorToGroup(ctx, t, dbClient, monitorA, groupID)
	testutil.AddMonitorToGroup(ctx, t, dbClient, monitorB, groupID)

	now := time.Now().UTC()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	day1 := today.AddDate(0, 0, -2)
	day2 := today.AddDate(0, 0, -1)
	testutil.InsertDailyRollup(ctx, t, dbClient, tenantID, monitorA, day1, 10, 8, 1000, 10, "failure", day1.Add(22*time.Hour))
	testutil.InsertDailyRollup(ctx, t, dbClient, tenantID, monitorA, day2, 10, 10, 2000, 10, "success", day2.Add(11*time.Hour))
	testutil.InsertDailyRollup(ctx, t, dbClient, tenantID, monitorB, day1, 10, 5, 3000, 10, "failure", day1.Add(21*time.Hour))

	groupResp, err := svc.GetMonitorAnalytics(ctx, tenantID, groupID, models.MonitorAnalyticsRange30d)
	if err != nil {
		t.Fatalf("GetMonitorAnalytics(group) error = %v", err)
	}
	if groupResp.Source != models.AnalyticsSourceRollup {
		t.Fatalf("group source = %s, want %s", groupResp.Source, models.AnalyticsSourceRollup)
	}

	monitorAResp, err := svc.GetMonitorAnalytics(ctx, tenantID, monitorA, models.MonitorAnalyticsRange30d)
	if err != nil {
		t.Fatalf("GetMonitorAnalytics(monitorA) error = %v", err)
	}
	monitorBResp, err := svc.GetMonitorAnalytics(ctx, tenantID, monitorB, models.MonitorAnalyticsRange30d)
	if err != nil {
		t.Fatalf("GetMonitorAnalytics(monitorB) error = %v", err)
	}

	expectedSLA := (monitorAResp.Summary.SLAPct + monitorBResp.Summary.SLAPct) / 2
	assertClose(t, groupResp.Summary.SLAPct, expectedSLA)
	assertClose(t, groupResp.Summary.UptimePct, expectedSLA)

	day1Group := findMonitorSeriesPoint(t, groupResp.UptimeSeries, day1)
	day1A := findMonitorSeriesPoint(t, monitorAResp.UptimeSeries, day1)
	day1B := findMonitorSeriesPoint(t, monitorBResp.UptimeSeries, day1)
	expectedDay1 := averageSeriesUptime(day1A, day1B)
	assertClose(t, day1Group.UptimePct, expectedDay1)

	day2Group := findMonitorSeriesPoint(t, groupResp.UptimeSeries, day2)
	day2A := findMonitorSeriesPoint(t, monitorAResp.UptimeSeries, day2)
	day2B := findMonitorSeriesPoint(t, monitorBResp.UptimeSeries, day2)
	expectedDay2 := averageSeriesUptime(day2A, day2B)
	assertClose(t, day2Group.UptimePct, expectedDay2)
}

func findMonitorSeriesPoint(t *testing.T, series []models.MonitorAnalyticsSeriesPoint, bucketStart time.Time) models.MonitorAnalyticsSeriesPoint {
	t.Helper()
	for _, point := range series {
		if point.BucketStart.Equal(bucketStart) {
			return point
		}
	}
	t.Fatalf("series point for %v not found", bucketStart)
	return models.MonitorAnalyticsSeriesPoint{}
}

func averageSeriesUptime(points ...models.MonitorAnalyticsSeriesPoint) float64 {
	var values []float64
	for _, point := range points {
		if point.HasData {
			values = append(values, point.UptimePct)
		}
	}
	if len(values) == 0 {
		return 0
	}
	var sum float64
	for _, value := range values {
		sum += value
	}
	return sum / float64(len(values))
}

func assertClose(t *testing.T, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 0.0001 {
		t.Fatalf("value = %.6f, want %.6f", got, want)
	}
}
