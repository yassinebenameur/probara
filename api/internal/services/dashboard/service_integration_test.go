package dashboard

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
	testcontainers "github.com/testcontainers/testcontainers-go"

	"github.com/yassinebenameur/probara/api/internal/models"
	alertservice "github.com/yassinebenameur/probara/api/internal/services/alerts"
	groupservice "github.com/yassinebenameur/probara/api/internal/services/groups"
	resultservice "github.com/yassinebenameur/probara/api/internal/services/results"
	sharedanalytics "github.com/yassinebenameur/probara/shared/analytics"
	shareddb "github.com/yassinebenameur/probara/shared/db"
	"github.com/yassinebenameur/probara/shared/testutil"
)

func TestService_GetOverview_LongRangeParityMatchesMonitorAnalytics(t *testing.T) {
	testcontainers.SkipIfProviderIsNotHealthy(t)

	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	analyticsRepo := sharedanalytics.NewRepository(dbClient)
	dashboardSvc := NewService(dbClient, nil, analyticsRepo, &fakeTenantSettingsReader{})
	groupSvc := groupservice.NewService(dbClient)
	resultsSvc := resultservice.NewService(dbClient, groupSvc, analyticsRepo)
	tenantID := testutil.InsertTenant(ctx, t, dbClient, "dashboard-rollup")
	monitorA := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "monitor-a")
	monitorB := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "monitor-b")
	now := time.Now().UTC()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	day1 := today.AddDate(0, 0, -2)
	day2 := today.AddDate(0, 0, -1)

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

	dashboardSvc := NewService(dbClient, nil, sharedanalytics.NewRepository(dbClient), &fakeTenantSettingsReader{})
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

	dashboardSvc := NewService(dbClient, nil, sharedanalytics.NewRepository(dbClient), &fakeTenantSettingsReader{})
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

func TestService_QueryMonitorsForGroups_NoCheckDataDoesNotScanNullAttention(t *testing.T) {
	testcontainers.SkipIfProviderIsNotHealthy(t)

	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	dashboardSvc := NewService(dbClient, nil, sharedanalytics.NewRepository(dbClient), &fakeTenantSettingsReader{})
	tenantID := testutil.InsertTenant(ctx, t, dbClient, "dashboard-groups-no-data")
	monitorID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "monitor-no-data")
	setMonitorTags(ctx, t, dbClient, monitorID, []string{"api"})

	now := time.Now().UTC()
	rows, err := dashboardSvc.queryMonitorsForGroups(ctx, tenantID, models.DashboardRange24h, now.Add(-24*time.Hour), now, nil)
	if err != nil {
		t.Fatalf("queryMonitorsForGroups() error = %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows length = %d, want 1", len(rows))
	}
	if rows[0].AttentionCount != 0 {
		t.Fatalf("AttentionCount = %d, want 0", rows[0].AttentionCount)
	}
}

func TestService_LoadGroups_24hHourlyRollupIgnoresOutsideWindowRawRows(t *testing.T) {
	testcontainers.SkipIfProviderIsNotHealthy(t)

	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	dashboardSvc := NewService(dbClient, nil, sharedanalytics.NewRepository(dbClient), &fakeTenantSettingsReader{})
	tenantID := testutil.InsertTenant(ctx, t, dbClient, "dashboard-groups-24h-hybrid")
	monitorID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "api-monitor")
	setMonitorTags(ctx, t, dbClient, monitorID, []string{"api"})

	// Use now-relative times so the exact-rolling window [now-24h, now) covers the data.
	// Anchor on the current hour so rollup bucket boundaries are stable.
	now := time.Now().UTC()
	endHour := now.Truncate(time.Hour)
	// rollup bucket: 16h ago (well inside the 24h rolling window)
	rollupBucket := endHour.Add(-16 * time.Hour)
	// rollup cursor: 2h ago — rollup_end = trunc_hour(cursor) so raw_complement covers [trunc_hour(cursor), now)
	cursorAt := now.Add(-2 * time.Hour)
	// raw row INSIDE raw_complement region: 1h ago (>= trunc_hour(cursorAt)), status=success
	insideAt := now.Add(-1 * time.Hour)
	// raw rows OUTSIDE window: >24h ago (before window start) and in the future (after now)
	beforeWindow := now.Add(-26 * time.Hour)
	afterWindow := now.Add(2 * time.Hour)

	if _, err := dbClient.ExecContext(ctx, `
		INSERT INTO monitor_hourly_rollups (
			tenant_id, monitor_id, bucket_hour, total_checks, success_checks,
			latency_success_sum_ms, latency_success_count, latest_status, latest_check_at,
			created_at, updated_at
		) VALUES ($1, $2, $3, 16, 15, 1500, 15, 'failure', $4, NOW(), NOW())
	`, tenantID, monitorID, rollupBucket, rollupBucket.Add(59*time.Minute)); err != nil {
		t.Fatalf("insert hourly rollup: %v", err)
	}
	if _, err := dbClient.ExecContext(ctx, `
		INSERT INTO rollup_job_state (job_name, last_created_at, last_check_result_id, last_run_at, updated_at)
		VALUES ('monitor_daily_rollups', $1, $2, NOW(), NOW())
	`, cursorAt, uuid.New()); err != nil {
		t.Fatalf("insert rollup state: %v", err)
	}

	// Rows before the window — should be ignored.
	for i := 0; i < 4; i++ {
		testutil.InsertCheckResult(ctx, t, dbClient, tenantID, monitorID, beforeWindow.Add(time.Duration(i)*time.Hour), "failure", "monitor", nil)
	}
	// Row inside the raw-complement region (after cursor, before now) — drives current_status.
	testutil.InsertCheckResult(ctx, t, dbClient, tenantID, monitorID, insideAt, "success", "monitor", testutil.IntPtr(100))
	// Rows after window end — should be ignored by the helper's w_end boundary.
	for i := 0; i < 4; i++ {
		testutil.InsertCheckResult(ctx, t, dbClient, tenantID, monitorID, afterWindow.Add(time.Duration(i)*time.Hour), "failure", "monitor", nil)
	}

	rangeStart := now.Add(-24 * time.Hour)
	rangeEnd := now
	groups, err := dashboardSvc.loadGroups(ctx, tenantID, models.DashboardRange24h, rangeStart, rangeEnd, nil, []string{"api"})
	if err != nil {
		t.Fatalf("loadGroups() error = %v", err)
	}
	if len(groups) != 1 {
		t.Fatalf("len(groups) = %d, want 1", len(groups))
	}
	// Rollup bucket (endHour-16h): 16 total, 15 success, 1 bad → needsAttention = true.
	// Raw row at now-1h (success) falls in raw_complement [trunc_hour(cursor), now).
	// Rows before w_start and after w_end are excluded.
	// The rollup bad check drives AttentionCount=1.
	if groups[0].AttentionCount != 1 {
		t.Fatalf("AttentionCount = %d, want 1", groups[0].AttentionCount)
	}
	if len(groups[0].Members) != 1 {
		t.Fatalf("Members length = %d, want 1", len(groups[0].Members))
	}
	// current_status comes from latest check in the window: insideAt row (now-1h) = "success"
	if groups[0].Members[0].CurrentStatus == nil || *groups[0].Members[0].CurrentStatus != "success" {
		t.Fatalf("CurrentStatus = %v, want success", groups[0].Members[0].CurrentStatus)
	}
}

func TestService_LoadGroups_24hHourlyRollupIncludesRawLagTail(t *testing.T) {
	testcontainers.SkipIfProviderIsNotHealthy(t)

	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	dashboardSvc := NewService(dbClient, nil, sharedanalytics.NewRepository(dbClient), &fakeTenantSettingsReader{})
	tenantID := testutil.InsertTenant(ctx, t, dbClient, "dashboard-groups-24h-hourly-lag")
	monitorID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "api-monitor")
	setMonitorTags(ctx, t, dbClient, monitorID, []string{"api"})

	// Use now-relative times for the exact-rolling window.
	// Rollup covers a full hour inside the window; cursor is 2h ago;
	// one raw failure row falls after the cursor (lag tail).
	now := time.Now().UTC()
	endHour := now.Truncate(time.Hour)
	// rollup bucket: 10h ago — a complete hour well within the 24h window
	rollupBucket := endHour.Add(-10 * time.Hour)
	// cursor: 2h ago — rollup has processed up to this point
	cursorAt := now.Add(-2 * time.Hour)
	// raw lag-tail row: 1h ago (after cursor, inside window) — status=failure
	lagTailAt := now.Add(-1 * time.Hour)

	if _, err := dbClient.ExecContext(ctx, `
		INSERT INTO monitor_hourly_rollups (
			tenant_id, monitor_id, bucket_hour, total_checks, success_checks,
			latency_success_sum_ms, latency_success_count, latest_status, latest_check_at,
			created_at, updated_at
		) VALUES ($1, $2, $3, 23, 23, 2300, 23, 'success', $4, NOW(), NOW())
	`, tenantID, monitorID, rollupBucket, rollupBucket.Add(59*time.Minute)); err != nil {
		t.Fatalf("insert hourly rollup: %v", err)
	}
	if _, err := dbClient.ExecContext(ctx, `
		INSERT INTO rollup_job_state (job_name, last_created_at, last_check_result_id, last_run_at, updated_at)
		VALUES ('monitor_daily_rollups', $1, $2, NOW(), NOW())
	`, cursorAt, uuid.New()); err != nil {
		t.Fatalf("insert rollup state: %v", err)
	}
	// Lag-tail failure row after cursor: should be included in raw_complement.
	testutil.InsertCheckResult(ctx, t, dbClient, tenantID, monitorID, lagTailAt, "failure", "monitor", nil)

	rangeStart := now.Add(-24 * time.Hour)
	rangeEnd := now
	groups, err := dashboardSvc.loadGroups(ctx, tenantID, models.DashboardRange24h, rangeStart, rangeEnd, nil, []string{"api"})
	if err != nil {
		t.Fatalf("loadGroups() error = %v", err)
	}
	if len(groups) != 1 {
		t.Fatalf("len(groups) = %d, want 1", len(groups))
	}
	// Rollup: 23 total, 23 success. Raw lag-tail: 1 failure.
	// Total = 24, success = 23 → 23/24 ≈ 95.83%.
	assertDashboardClose(t, groups[0].Uptime, 95.8333333333)
	if groups[0].AttentionCount != 1 {
		t.Fatalf("AttentionCount = %d, want 1", groups[0].AttentionCount)
	}
	// current_status: latest check is the lag-tail failure.
	if groups[0].Members[0].CurrentStatus == nil || *groups[0].Members[0].CurrentStatus != "failure" {
		t.Fatalf("CurrentStatus = %v, want failure", groups[0].Members[0].CurrentStatus)
	}
}

func TestService_GetOverview_TagFilteredScopeAndZeroMatch(t *testing.T) {
	testcontainers.SkipIfProviderIsNotHealthy(t)

	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	dashboardSvc := NewService(dbClient, alertservice.NewService(dbClient, nil), sharedanalytics.NewRepository(dbClient), &fakeTenantSettingsReader{})
	tenantID := testutil.InsertTenant(ctx, t, dbClient, "dashboard-tags")
	monitorA := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "monitor-a")
	monitorB := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "monitor-b")
	monitorC := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "monitor-c")

	setMonitorTags(ctx, t, dbClient, monitorA, []string{"prod", "api"})
	setMonitorTags(ctx, t, dbClient, monitorB, []string{"prod"})
	setMonitorTags(ctx, t, dbClient, monitorC, []string{"api"})

	now := time.Now().UTC()
	bucketDay := time.Date(now.Year(), now.Month(), now.Day()-1, 0, 0, 0, 0, time.UTC)
	testutil.InsertDailyRollup(ctx, t, dbClient, tenantID, monitorA, bucketDay, 10, 7, 700, 7, "error", bucketDay.Add(23*time.Hour))
	testutil.InsertDailyRollup(ctx, t, dbClient, tenantID, monitorB, bucketDay, 10, 10, 900, 10, "success", bucketDay.Add(22*time.Hour))
	testutil.InsertDailyRollup(ctx, t, dbClient, tenantID, monitorC, bucketDay, 10, 8, 800, 8, "failure", bucketDay.Add(21*time.Hour))

	testutil.InsertCheckResult(ctx, t, dbClient, tenantID, monitorA, now.Add(-72*time.Hour), "failure", "monitor", nil)
	testutil.InsertCheckResult(ctx, t, dbClient, tenantID, monitorA, now.Add(-48*time.Hour), "error", "monitor", nil)
	testutil.InsertCheckResult(ctx, t, dbClient, tenantID, monitorA, now.Add(-24*time.Hour), "failure", "monitor", nil)
	testutil.InsertCheckResult(ctx, t, dbClient, tenantID, monitorB, now.Add(-24*time.Hour), "success", "monitor", testutil.IntPtr(110))
	testutil.InsertCheckResult(ctx, t, dbClient, tenantID, monitorC, now.Add(-12*time.Hour), "failure", "monitor", nil)

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
			($1, $3, $4, $6, 'active', NOW(), 3, NOW(), NOW()),
			($2, $3, $5, $6, 'acknowledged', NOW(), 2, NOW(), NOW())
	`, uuid.New(), uuid.New(), tenantID, monitorA, monitorC, policyID); err != nil {
		t.Fatalf("insert alerts: %v", err)
	}

	overview, err := dashboardSvc.GetOverview(ctx, tenantID, &models.DashboardOverviewQuery{
		Range:         models.DashboardRange30d,
		FailuresLimit: 10,
		AlertsLimit:   10,
		Tags:          []string{"prod", "api"},
	})
	if err != nil {
		t.Fatalf("GetOverview(30d, tags) error = %v", err)
	}

	if len(overview.AvailableTags) != 2 || overview.AvailableTags[0] != "api" || overview.AvailableTags[1] != "prod" {
		t.Fatalf("AvailableTags = %#v, want [api prod]", overview.AvailableTags)
	}
	if overview.Stats.TotalMonitors != 1 || overview.Stats.ActiveMonitors != 1 || overview.Stats.HTTPMonitors != 1 {
		t.Fatalf("Stats = %+v, want one matching monitor", overview.Stats)
	}
	assertDashboardClose(t, overview.Stats.OverallUptime, 70.0)
	if overview.OpsSummary.UpMonitors != 0 || overview.OpsSummary.DownMonitors != 1 || overview.OpsSummary.ActiveAlerts != 1 || overview.OpsSummary.AcknowledgedAlerts != 0 {
		t.Fatalf("OpsSummary = %+v, want one down monitor and one active alert", overview.OpsSummary)
	}
	if len(overview.ProblemMonitors) != 1 || overview.ProblemMonitors[0].MonitorID != monitorA {
		t.Fatalf("ProblemMonitors = %+v, want only monitorA", overview.ProblemMonitors)
	}
	if len(overview.RecentFailures) != 3 {
		t.Fatalf("RecentFailures length = %d, want 3", len(overview.RecentFailures))
	}
	for _, failure := range overview.RecentFailures {
		if failure.MonitorID != monitorA {
			t.Fatalf("RecentFailures included monitor %s, want only %s", failure.MonitorID, monitorA)
		}
	}
	if len(overview.RecentAlerts) != 1 || overview.RecentAlerts[0].MonitorID != monitorA {
		t.Fatalf("RecentAlerts = %+v, want only monitorA alert", overview.RecentAlerts)
	}

	zeroMatch, err := dashboardSvc.GetOverview(ctx, tenantID, &models.DashboardOverviewQuery{
		Range: models.DashboardRange30d,
		Tags:  []string{"missing"},
	})
	if err != nil {
		t.Fatalf("GetOverview(30d, missing tag) error = %v", err)
	}

	if zeroMatch.Stats.TotalMonitors != 0 || zeroMatch.Stats.ActiveMonitors != 0 {
		t.Fatalf("zero-match stats = %+v, want zero monitor counts", zeroMatch.Stats)
	}
	if len(zeroMatch.ProblemMonitors) != 0 || len(zeroMatch.RecentFailures) != 0 || len(zeroMatch.RecentAlerts) != 0 {
		t.Fatalf("zero-match lists should be empty, got problems=%d failures=%d alerts=%d", len(zeroMatch.ProblemMonitors), len(zeroMatch.RecentFailures), len(zeroMatch.RecentAlerts))
	}
	if len(zeroMatch.AvailableTags) != 2 || zeroMatch.AvailableTags[0] != "api" || zeroMatch.AvailableTags[1] != "prod" {
		t.Fatalf("zero-match available tags = %#v, want [api prod]", zeroMatch.AvailableTags)
	}
}

func setMonitorTags(ctx context.Context, t *testing.T, dbClient shareddb.DB, monitorID uuid.UUID, tags []string) {
	t.Helper()
	if _, err := dbClient.ExecContext(ctx, `UPDATE monitors SET tags = $2 WHERE id = $1`, monitorID, pq.Array(tags)); err != nil {
		t.Fatalf("set monitor tags: %v", err)
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

func TestService_GetSummary_24hStatsExactRollingFromHourlyRollup(t *testing.T) {
	testcontainers.SkipIfProviderIsNotHealthy(t)

	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "stats-24h-rolling")
	monitorID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "stats-mon")

	// Seed only hourly rollups inside the rolling window (no raw rows), so the
	// stats values must come from the rollup path or this test will read zeros.
	now := time.Now().UTC()
	// Build 20 full rollup hours inside (now-24h, now] (skipping the partial leading hour).
	leadingEdgeEnd := now.Add(-24 * time.Hour).Truncate(time.Hour).Add(time.Hour)
	for i := 0; i < 20; i++ {
		bucket := leadingEdgeEnd.Add(time.Duration(i) * time.Hour)
		testutil.InsertHourlyRollup(ctx, t, dbClient, tenantID, monitorID, bucket, 10, 9, 900, 9, "success", bucket.Add(59*time.Minute))
	}
	testutil.InsertRollupJobState(ctx, t, dbClient, "monitor_daily_rollups", now.Add(-1*time.Minute), uuid.New())

	svc := NewService(dbClient, nil, sharedanalytics.NewRepository(dbClient), &fakeTenantSettingsReader{})
	resp, err := svc.GetSummary(ctx, tenantID, &models.DashboardOverviewQuery{Range: models.DashboardRange24h})
	if err != nil {
		t.Fatalf("GetSummary() error = %v", err)
	}
	// 20 buckets * (9 success / 10 total) = 90% uptime; latency 100ms/check.
	if math.Abs(resp.Stats.OverallUptime-90) > 0.01 {
		t.Fatalf("OverallUptime = %f, want 90", resp.Stats.OverallUptime)
	}
	if math.Abs(resp.Stats.AvgResponseMS-100) > 0.01 {
		t.Fatalf("AvgResponseMS = %f, want 100", resp.Stats.AvgResponseMS)
	}
}

func TestService_GetSummary_24hTrendHourAligned(t *testing.T) {
	testcontainers.SkipIfProviderIsNotHealthy(t)

	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "trend-24h")
	monitorID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "trend-mon")

	now := time.Now().UTC()
	endHour := now.Truncate(time.Hour)
	bucketA := endHour.Add(-3 * time.Hour)
	bucketB := endHour.Add(-1 * time.Hour)
	testutil.InsertHourlyRollup(ctx, t, dbClient, tenantID, monitorID, bucketA, 10, 10, 1000, 10, "success", bucketA.Add(59*time.Minute))
	testutil.InsertHourlyRollup(ctx, t, dbClient, tenantID, monitorID, bucketB, 10, 5, 500, 5, "failure", bucketB.Add(59*time.Minute))
	testutil.InsertRollupJobState(ctx, t, dbClient, "monitor_daily_rollups", now.Add(-1*time.Minute), uuid.New())

	svc := NewService(dbClient, nil, sharedanalytics.NewRepository(dbClient), &fakeTenantSettingsReader{})
	resp, err := svc.GetSummary(ctx, tenantID, &models.DashboardOverviewQuery{Range: models.DashboardRange24h})
	if err != nil {
		t.Fatalf("GetSummary() error = %v", err)
	}
	if len(resp.Trend) != 24 {
		t.Fatalf("len(Trend) = %d, want 24", len(resp.Trend))
	}
	pointA := findTrendPoint(t, resp.Trend, bucketA)
	if math.Abs(pointA.Uptime-100) > 0.01 {
		t.Fatalf("Trend[bucketA].Uptime = %f, want 100", pointA.Uptime)
	}
	if pointA.TotalChecks != 10 {
		t.Fatalf("Trend[bucketA].TotalChecks = %d, want 10", pointA.TotalChecks)
	}
	pointB := findTrendPoint(t, resp.Trend, bucketB)
	if math.Abs(pointB.Uptime-50) > 0.01 {
		t.Fatalf("Trend[bucketB].Uptime = %f, want 50", pointB.Uptime)
	}
}

func TestService_GetSummary_24hActivityHourAligned(t *testing.T) {
	testcontainers.SkipIfProviderIsNotHealthy(t)

	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "activity-24h")
	monitorID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "activity-mon")

	now := time.Now().UTC()
	endHour := now.Truncate(time.Hour)
	bucket := endHour.Add(-2 * time.Hour)
	testutil.InsertHourlyRollup(ctx, t, dbClient, tenantID, monitorID, bucket, 10, 7, 700, 7, "failure", bucket.Add(59*time.Minute))
	testutil.InsertRollupJobState(ctx, t, dbClient, "monitor_daily_rollups", now.Add(-1*time.Minute), uuid.New())

	svc := NewService(dbClient, nil, sharedanalytics.NewRepository(dbClient), &fakeTenantSettingsReader{})
	resp, err := svc.GetSummary(ctx, tenantID, &models.DashboardOverviewQuery{Range: models.DashboardRange24h})
	if err != nil {
		t.Fatalf("GetSummary() error = %v", err)
	}
	if len(resp.Activity24h) != 24 {
		t.Fatalf("len(Activity24h) = %d, want 24", len(resp.Activity24h))
	}
	var found *models.DashboardActivityHour
	for i := range resp.Activity24h {
		if resp.Activity24h[i].BucketStart.Equal(bucket) {
			found = &resp.Activity24h[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("activity bucket %v not found", bucket)
	}
	if found.Checks != 10 || found.Failures != 3 {
		t.Fatalf("activity[bucket] = {checks:%d, failures:%d}, want {10, 3}", found.Checks, found.Failures)
	}
}

// fakeTenantSettingsWithTags is a test-local tenant settings reader that returns
// a fixed set of dashboard_group_tags. Used when GetSummary must see specific tags.
type fakeTenantSettingsWithTags struct {
	tags []string
}

func (f *fakeTenantSettingsWithTags) GetTenantSettings(_ context.Context, _ uuid.UUID) (*models.TenantSettings, error) {
	return &models.TenantSettings{DashboardGroupTags: f.tags}, nil
}

func TestService_LoadGroups_24hUsesExactRollingSummary(t *testing.T) {
	testcontainers.SkipIfProviderIsNotHealthy(t)

	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "groups-24h-rolling")
	monitorID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "group-mon")
	_, err := dbClient.ExecContext(ctx, `UPDATE monitors SET tags = ARRAY['team-a'] WHERE id = $1`, monitorID)
	if err != nil {
		t.Fatalf("update tags: %v", err)
	}

	now := time.Now().UTC()
	bucket := now.Truncate(time.Hour).Add(-1 * time.Hour)
	// Rollup latest_status = failure, totals 5/10 → 50% uptime.
	testutil.InsertHourlyRollup(ctx, t, dbClient, tenantID, monitorID, bucket, 10, 5, 500, 5, "failure", bucket.Add(59*time.Minute))
	testutil.InsertRollupJobState(ctx, t, dbClient, "monitor_daily_rollups", now.Add(-1*time.Minute), uuid.New())

	svc := NewService(dbClient, nil, sharedanalytics.NewRepository(dbClient), &fakeTenantSettingsWithTags{tags: []string{"team-a"}})
	resp, err := svc.GetSummary(ctx, tenantID, &models.DashboardOverviewQuery{Range: models.DashboardRange24h})
	if err != nil {
		t.Fatalf("GetSummary() error = %v", err)
	}
	if len(resp.Groups) == 0 {
		t.Fatalf("Groups empty")
	}
	g := resp.Groups[0]
	if g.AttentionCount != 1 {
		t.Fatalf("team-a AttentionCount = %d, want 1", g.AttentionCount)
	}
	if math.Abs(g.Uptime-50) > 0.01 {
		t.Fatalf("team-a Uptime = %f, want 50", g.Uptime)
	}
}
