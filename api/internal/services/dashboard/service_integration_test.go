package dashboard

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/google/uuid"
	testcontainers "github.com/testcontainers/testcontainers-go"

	"github.com/yassinebenameur/probara/api/internal/models"
	groupservice "github.com/yassinebenameur/probara/api/internal/services/groups"
	resultservice "github.com/yassinebenameur/probara/api/internal/services/results"
	"github.com/yassinebenameur/probara/shared/testutil"
)

func TestService_GetOverview_LongRangeParityMatchesMonitorAnalytics(t *testing.T) {
	testcontainers.SkipIfProviderIsNotHealthy(t)

	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	dashboardSvc := NewService(dbClient, nil)
	groupSvc := groupservice.NewService(dbClient)
	resultsSvc := resultservice.NewService(dbClient, groupSvc)
	tenantID := testutil.InsertTenant(ctx, t, dbClient, "dashboard-rollup")
	monitorA := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "monitor-a")
	monitorB := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "monitor-b")
	day1 := time.Date(2026, time.March, 5, 0, 0, 0, 0, time.UTC)
	day2 := time.Date(2026, time.March, 6, 0, 0, 0, 0, time.UTC)

	testutil.InsertDailyRollup(ctx, t, dbClient, tenantID, monitorA, day1, 10, 8, 1000, 10, "failure", day1.Add(22*time.Hour))
	testutil.InsertDailyRollup(ctx, t, dbClient, tenantID, monitorA, day2, 10, 10, 2000, 10, "success", day2.Add(11*time.Hour))
	testutil.InsertDailyRollup(ctx, t, dbClient, tenantID, monitorB, day1, 10, 5, 3000, 10, "failure", day1.Add(21*time.Hour))

	for _, tc := range []struct {
		name             string
		dashboardRange   models.DashboardRange
		analyticsRange   models.MonitorAnalyticsRange
		expectedTrendLen int
	}{
		{name: "30d", dashboardRange: models.DashboardRange30d, analyticsRange: models.MonitorAnalyticsRange30d, expectedTrendLen: 30},
		{name: "90d", dashboardRange: models.DashboardRange90d, analyticsRange: models.MonitorAnalyticsRange90d, expectedTrendLen: 90},
		{name: "365d", dashboardRange: models.DashboardRange365d, analyticsRange: models.MonitorAnalyticsRange365d, expectedTrendLen: 365},
	} {
		t.Run(tc.name, func(t *testing.T) {
			overview, err := dashboardSvc.GetOverview(ctx, tenantID, &models.DashboardOverviewQuery{Range: tc.dashboardRange})
			if err != nil {
				t.Fatalf("GetOverview(%s) error = %v", tc.dashboardRange, err)
			}
			if overview.Range != tc.dashboardRange {
				t.Fatalf("Range = %s, want %s", overview.Range, tc.dashboardRange)
			}
			if len(overview.Trend) != tc.expectedTrendLen {
				t.Fatalf("trend length = %d, want %d", len(overview.Trend), tc.expectedTrendLen)
			}
			if len(overview.Activity24h) != 0 {
				t.Fatalf("Activity24h length = %d, want 0 for long range", len(overview.Activity24h))
			}
			if len(overview.RecentFailures) != 0 {
				t.Fatalf("RecentFailures length = %d, want 0 for long range", len(overview.RecentFailures))
			}

			monitorAAnalytics, err := resultsSvc.GetMonitorAnalytics(ctx, tenantID, monitorA, tc.analyticsRange)
			if err != nil {
				t.Fatalf("GetMonitorAnalytics(monitorA, %s) error = %v", tc.analyticsRange, err)
			}
			monitorBAnalytics, err := resultsSvc.GetMonitorAnalytics(ctx, tenantID, monitorB, tc.analyticsRange)
			if err != nil {
				t.Fatalf("GetMonitorAnalytics(monitorB, %s) error = %v", tc.analyticsRange, err)
			}

			expectedSLA := (monitorAAnalytics.Summary.SLAPct + monitorBAnalytics.Summary.SLAPct) / 2
			assertDashboardClose(t, overview.Stats.OverallUptime, expectedSLA)

			pointDay1 := findTrendPoint(t, overview.Trend, day1)
			expectedDay1Uptime := averageMonitorSeriesAt(day1, monitorAAnalytics.UptimeSeries, monitorBAnalytics.UptimeSeries)
			assertDashboardClose(t, pointDay1.Uptime, expectedDay1Uptime)

			pointDay2 := findTrendPoint(t, overview.Trend, day2)
			expectedDay2Uptime := averageMonitorSeriesAt(day2, monitorAAnalytics.UptimeSeries, monitorBAnalytics.UptimeSeries)
			assertDashboardClose(t, pointDay2.Uptime, expectedDay2Uptime)
		})
	}
}

func TestService_GetOverview_ActionSummaryAndProblemMonitors(t *testing.T) {
	testcontainers.SkipIfProviderIsNotHealthy(t)

	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	dashboardSvc := NewService(dbClient, nil)
	tenantID := testutil.InsertTenant(ctx, t, dbClient, "dashboard-action")
	monitorA := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "monitor-a")
	monitorB := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "monitor-b")
	monitorC := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "monitor-c")
	monitorPaused := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "monitor-paused")
	_ = testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "monitor-no-data")

	if _, err := dbClient.ExecContext(ctx, `UPDATE monitors SET enabled = FALSE WHERE id = $1`, monitorPaused); err != nil {
		t.Fatalf("disable paused monitor: %v", err)
	}

	now := time.Now().UTC()
	bucketDay := time.Date(now.Year(), now.Month(), now.Day()-1, 0, 0, 0, 0, time.UTC)
	testutil.InsertDailyRollup(ctx, t, dbClient, tenantID, monitorA, bucketDay, 10, 7, 700, 7, "error", bucketDay.Add(23*time.Hour))
	testutil.InsertDailyRollup(ctx, t, dbClient, tenantID, monitorB, bucketDay, 10, 9, 900, 9, "failure", bucketDay.Add(22*time.Hour))
	testutil.InsertDailyRollup(ctx, t, dbClient, tenantID, monitorC, bucketDay, 10, 10, 1000, 10, "success", bucketDay.Add(21*time.Hour))

	testutil.InsertCheckResult(ctx, t, dbClient, tenantID, monitorA, now.Add(-72*time.Hour), "failure", "monitor", nil)
	testutil.InsertCheckResult(ctx, t, dbClient, tenantID, monitorA, now.Add(-48*time.Hour), "error", "monitor", nil)
	testutil.InsertCheckResult(ctx, t, dbClient, tenantID, monitorA, now.Add(-24*time.Hour), "failure", "monitor", nil)
	testutil.InsertCheckResult(ctx, t, dbClient, tenantID, monitorB, now.Add(-36*time.Hour), "failure", "monitor", nil)
	testutil.InsertCheckResult(ctx, t, dbClient, tenantID, monitorC, now.Add(-12*time.Hour), "success", "monitor", testutil.IntPtr(120))

	policyID := uuid.New()
	if _, err := dbClient.ExecContext(ctx, `
		INSERT INTO alert_policies (id, tenant_id, name, failure_threshold, failure_window_seconds, created_at, updated_at)
		VALUES ($1, $2, 'default-policy', 2, 300, NOW(), NOW())
	`, policyID, tenantID); err != nil {
		t.Fatalf("insert alert policy: %v", err)
	}
	if _, err := dbClient.ExecContext(ctx, `
		INSERT INTO alerts (id, tenant_id, monitor_id, alert_policy_id, status, triggered_at, failure_count, created_at, updated_at)
		VALUES
			($1, $4, $5, $7, 'active', NOW(), 3, NOW(), NOW()),
			($2, $4, $6, $7, 'acknowledged', NOW(), 2, NOW(), NOW()),
			($3, $4, $5, $7, 'resolved', NOW(), 1, NOW(), NOW())
	`, uuid.New(), uuid.New(), uuid.New(), tenantID, monitorA, monitorB, policyID); err != nil {
		t.Fatalf("insert alerts: %v", err)
	}

	overview, err := dashboardSvc.GetOverview(ctx, tenantID, &models.DashboardOverviewQuery{
		Range:         models.DashboardRange30d,
		FailuresLimit: 10,
	})
	if err != nil {
		t.Fatalf("GetOverview(30d) error = %v", err)
	}

	if overview.OpsSummary.UpMonitors != 1 {
		t.Fatalf("OpsSummary.UpMonitors = %d, want 1", overview.OpsSummary.UpMonitors)
	}
	if overview.OpsSummary.DownMonitors != 2 {
		t.Fatalf("OpsSummary.DownMonitors = %d, want 2", overview.OpsSummary.DownMonitors)
	}
	if overview.OpsSummary.PausedMonitors != 1 {
		t.Fatalf("OpsSummary.PausedMonitors = %d, want 1", overview.OpsSummary.PausedMonitors)
	}
	if overview.OpsSummary.ActiveAlerts != 1 {
		t.Fatalf("OpsSummary.ActiveAlerts = %d, want 1", overview.OpsSummary.ActiveAlerts)
	}
	if overview.OpsSummary.AcknowledgedAlerts != 1 {
		t.Fatalf("OpsSummary.AcknowledgedAlerts = %d, want 1", overview.OpsSummary.AcknowledgedAlerts)
	}

	if len(overview.ProblemMonitors) != 2 {
		t.Fatalf("ProblemMonitors length = %d, want 2", len(overview.ProblemMonitors))
	}
	if overview.ProblemMonitors[0].MonitorID != monitorA {
		t.Fatalf("ProblemMonitors[0].MonitorID = %s, want %s", overview.ProblemMonitors[0].MonitorID, monitorA)
	}
	if overview.ProblemMonitors[0].FailureCount != 2 || overview.ProblemMonitors[0].ErrorCount != 1 {
		t.Fatalf("ProblemMonitors[0] counts = (%d,%d), want (2,1)", overview.ProblemMonitors[0].FailureCount, overview.ProblemMonitors[0].ErrorCount)
	}
	if math.Abs(overview.ProblemMonitors[0].Uptime-70.0) > 0.0001 {
		t.Fatalf("ProblemMonitors[0].Uptime = %.4f, want 70.0", overview.ProblemMonitors[0].Uptime)
	}
	if overview.ProblemMonitors[0].CurrentStatus == nil || *overview.ProblemMonitors[0].CurrentStatus != "error" {
		t.Fatalf("ProblemMonitors[0].CurrentStatus = %v, want error", overview.ProblemMonitors[0].CurrentStatus)
	}
	if overview.ProblemMonitors[1].MonitorID != monitorB {
		t.Fatalf("ProblemMonitors[1].MonitorID = %s, want %s", overview.ProblemMonitors[1].MonitorID, monitorB)
	}

	if len(overview.RecentFailures) != 4 {
		t.Fatalf("RecentFailures length = %d, want 4", len(overview.RecentFailures))
	}
}

func TestService_GetOverview_ActionSummaryEmptyTenant(t *testing.T) {
	testcontainers.SkipIfProviderIsNotHealthy(t)

	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	dashboardSvc := NewService(dbClient, nil)
	tenantID := testutil.InsertTenant(ctx, t, dbClient, "dashboard-empty")

	overview, err := dashboardSvc.GetOverview(ctx, tenantID, &models.DashboardOverviewQuery{
		Range: models.DashboardRange30d,
	})
	if err != nil {
		t.Fatalf("GetOverview(30d) error = %v", err)
	}

	if overview.OpsSummary.UpMonitors != 0 || overview.OpsSummary.DownMonitors != 0 || overview.OpsSummary.PausedMonitors != 0 {
		t.Fatalf("OpsSummary monitor counts = %+v, want all zero", overview.OpsSummary)
	}
	if overview.OpsSummary.ActiveAlerts != 0 || overview.OpsSummary.AcknowledgedAlerts != 0 {
		t.Fatalf("OpsSummary alert counts = %+v, want all zero", overview.OpsSummary)
	}
	if len(overview.ProblemMonitors) != 0 {
		t.Fatalf("ProblemMonitors length = %d, want 0", len(overview.ProblemMonitors))
	}
	if len(overview.RecentFailures) != 0 {
		t.Fatalf("RecentFailures length = %d, want 0", len(overview.RecentFailures))
	}
}

func findTrendPoint(t *testing.T, trend []models.DashboardTrendPoint, bucketStart time.Time) models.DashboardTrendPoint {
	t.Helper()
	for _, point := range trend {
		if point.BucketStart.Equal(bucketStart) {
			return point
		}
	}
	t.Fatalf("trend point for %v not found", bucketStart)
	return models.DashboardTrendPoint{}
}

func averageMonitorSeriesAt(bucketStart time.Time, seriesList ...[]models.MonitorAnalyticsSeriesPoint) float64 {
	var values []float64
	for _, series := range seriesList {
		for _, point := range series {
			if point.BucketStart.Equal(bucketStart) && point.HasData {
				values = append(values, point.UptimePct)
				break
			}
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

func assertDashboardClose(t *testing.T, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 0.0001 {
		t.Fatalf("value = %.6f, want %.6f", got, want)
	}
}
