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
	dashboardSvc := NewService(dbClient, nil, analyticsRepo, &fakeTenantSettingsReader{}, nil)
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
			assertDashboardClose(t, derefUptime(t, overview.Stats.OverallUptime), expectedSLA)

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

	dashboardSvc := NewService(dbClient, nil, sharedanalytics.NewRepository(dbClient), &fakeTenantSettingsReader{}, nil)
	tenantID := testutil.InsertTenant(ctx, t, dbClient, "dashboard-action")
	monitorA := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "monitor-a")
	monitorB := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "monitor-b")
	monitorC := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "monitor-c")
	monitorPaused := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "monitor-paused")
	_ = testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "monitor-no-data")

	if _, err := dbClient.ExecContext(ctx, `UPDATE monitors SET enabled = FALSE WHERE id = $1`, monitorPaused); err != nil {
		t.Fatalf("disable paused monitor: %v", err)
	}
	// Seed current_state via the persisted state machine: monitorA and monitorB are
	// confirmed down, monitorC is up. Ops summary and monitor health derive from
	// current_state (D1), not from rollup latest_status.
	if _, err := dbClient.ExecContext(ctx, `UPDATE monitors SET current_state = 'down' WHERE id = ANY($1)`, pq.Array([]uuid.UUID{monitorA, monitorB})); err != nil {
		t.Fatalf("set current_state down: %v", err)
	}
	if _, err := dbClient.ExecContext(ctx, `UPDATE monitors SET current_state = 'up' WHERE id = $1`, monitorC); err != nil {
		t.Fatalf("set current_state up: %v", err)
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
	// current_state='down' maps to CurrentStatus="failure" and CurrentState="down".
	if overview.ProblemMonitors[0].CurrentStatus == nil || *overview.ProblemMonitors[0].CurrentStatus != "failure" {
		t.Fatalf("ProblemMonitors[0].CurrentStatus = %v, want failure", overview.ProblemMonitors[0].CurrentStatus)
	}
	if overview.ProblemMonitors[0].CurrentState != "down" {
		t.Fatalf("ProblemMonitors[0].CurrentState = %q, want down", overview.ProblemMonitors[0].CurrentState)
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

	dashboardSvc := NewService(dbClient, nil, sharedanalytics.NewRepository(dbClient), &fakeTenantSettingsReader{}, nil)
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

	dashboardSvc := NewService(dbClient, nil, sharedanalytics.NewRepository(dbClient), &fakeTenantSettingsReader{}, nil)
	tenantID := testutil.InsertTenant(ctx, t, dbClient, "dashboard-groups-no-data")
	monitorID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "monitor-no-data")
	setMonitorTags(ctx, t, dbClient, monitorID, []string{"api"})

	now := time.Now().UTC()
	rows, err := dashboardSvc.queryMonitorsForGroups(ctx, tenantID, models.DashboardRange24h, now.Add(-24*time.Hour), now, nil, nil)
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

	dashboardSvc := NewService(dbClient, nil, sharedanalytics.NewRepository(dbClient), &fakeTenantSettingsReader{}, nil)
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
	groups, err := dashboardSvc.loadGroups(ctx, tenantID, models.DashboardRange24h, rangeStart, rangeEnd, nil, []string{"api"}, nil)
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

	dashboardSvc := NewService(dbClient, nil, sharedanalytics.NewRepository(dbClient), &fakeTenantSettingsReader{}, nil)
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
	groups, err := dashboardSvc.loadGroups(ctx, tenantID, models.DashboardRange24h, rangeStart, rangeEnd, nil, []string{"api"}, nil)
	if err != nil {
		t.Fatalf("loadGroups() error = %v", err)
	}
	if len(groups) != 1 {
		t.Fatalf("len(groups) = %d, want 1", len(groups))
	}
	// Rollup: 23 total, 23 success. Raw lag-tail: 1 failure.
	// Total = 24, success = 23 → 23/24 ≈ 95.83%.
	assertDashboardClose(t, derefUptime(t, groups[0].Uptime), 95.8333333333)
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

	dashboardSvc := NewService(dbClient, alertservice.NewService(dbClient, nil), sharedanalytics.NewRepository(dbClient), &fakeTenantSettingsReader{}, nil)
	tenantID := testutil.InsertTenant(ctx, t, dbClient, "dashboard-tags")
	monitorA := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "monitor-a")
	monitorB := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "monitor-b")
	monitorC := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "monitor-c")

	setMonitorTags(ctx, t, dbClient, monitorA, []string{"prod", "api"})
	setMonitorTags(ctx, t, dbClient, monitorB, []string{"prod"})
	setMonitorTags(ctx, t, dbClient, monitorC, []string{"api"})

	// monitorA is confirmed down in the state machine; ops summary follows
	// current_state (D1), not the rollup latest_status.
	if _, err := dbClient.ExecContext(ctx, `UPDATE monitors SET current_state = 'down' WHERE id = $1`, monitorA); err != nil {
		t.Fatalf("set current_state down: %v", err)
	}

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
	assertDashboardClose(t, derefUptime(t, overview.Stats.OverallUptime), 70.0)
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
	if len(overview.RecentAlerts) != 1 || overview.RecentAlerts[0].MonitorID == nil || *overview.RecentAlerts[0].MonitorID != monitorA {
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

func derefUptime(t *testing.T, p *float64) float64 {
	t.Helper()
	if p == nil {
		t.Fatal("uptime is nil, want a value")
	}
	return *p
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

	svc := NewService(dbClient, nil, sharedanalytics.NewRepository(dbClient), &fakeTenantSettingsReader{}, nil)
	resp, err := svc.GetSummary(ctx, tenantID, &models.DashboardOverviewQuery{Range: models.DashboardRange24h})
	if err != nil {
		t.Fatalf("GetSummary() error = %v", err)
	}
	// 20 buckets * (9 success / 10 total) = 90% uptime; latency 100ms/check.
	if u := derefUptime(t, resp.Stats.OverallUptime); math.Abs(u-90) > 0.01 {
		t.Fatalf("OverallUptime = %f, want 90", u)
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

	svc := NewService(dbClient, nil, sharedanalytics.NewRepository(dbClient), &fakeTenantSettingsReader{}, nil)
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

	svc := NewService(dbClient, nil, sharedanalytics.NewRepository(dbClient), &fakeTenantSettingsReader{}, nil)
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

	svc := NewService(dbClient, nil, sharedanalytics.NewRepository(dbClient), &fakeTenantSettingsWithTags{tags: []string{"team-a"}}, nil)
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
	if u := derefUptime(t, g.Uptime); math.Abs(u-50) > 0.01 {
		t.Fatalf("team-a Uptime = %f, want 50", u)
	}
}

func TestService_GetGroupSparkline_24hHourAligned(t *testing.T) {
	testcontainers.SkipIfProviderIsNotHealthy(t)

	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "sparkline-24h")
	monitorID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "spark-mon")
	_, err := dbClient.ExecContext(ctx, `UPDATE monitors SET tags = ARRAY['team-a'] WHERE id = $1`, monitorID)
	if err != nil {
		t.Fatalf("update tags: %v", err)
	}

	now := time.Now().UTC()
	bucket := now.Truncate(time.Hour).Add(-1 * time.Hour)
	testutil.InsertHourlyRollup(ctx, t, dbClient, tenantID, monitorID, bucket, 10, 5, 500, 5, "failure", bucket.Add(59*time.Minute))
	testutil.InsertRollupJobState(ctx, t, dbClient, "monitor_daily_rollups", now.Add(-1*time.Minute), uuid.New())

	svc := NewService(dbClient, nil, sharedanalytics.NewRepository(dbClient), &fakeTenantSettingsReader{}, nil)
	tag := "team-a"
	resp, err := svc.GetGroupSparkline(ctx, tenantID, &models.DashboardGroupSparklineQuery{
		Tag:   &tag,
		Range: models.DashboardRange24h,
	})
	if err != nil {
		t.Fatalf("GetGroupSparkline() error = %v", err)
	}
	if len(resp.Buckets) != 12 {
		t.Fatalf("Buckets length = %d, want 12 (resampled)", len(resp.Buckets))
	}
	found50 := false
	for _, v := range resp.Buckets {
		if v != nil && math.Abs(*v-50) < 0.01 {
			found50 = true
			break
		}
	}
	if !found50 {
		t.Fatalf("no 50%% bucket in sparkline %v", resp.Buckets)
	}
}

func TestService_GetProblemMonitors_24hCountsFromExactRollingSummary(t *testing.T) {
	testcontainers.SkipIfProviderIsNotHealthy(t)

	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "problems-24h-rolling")
	monitorID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "problem-mon")

	now := time.Now().UTC()
	bucket := now.Truncate(time.Hour).Add(-2 * time.Hour)
	// 10 checks / 3 success → 7 bad, all attributed to FailureChecks via rollup.
	testutil.InsertHourlyRollup(ctx, t, dbClient, tenantID, monitorID, bucket, 10, 3, 300, 3, "failure", bucket.Add(59*time.Minute))
	testutil.InsertRollupJobState(ctx, t, dbClient, "monitor_daily_rollups", now.Add(-1*time.Minute), uuid.New())

	svc := NewService(dbClient, nil, sharedanalytics.NewRepository(dbClient), &fakeTenantSettingsReader{}, nil)
	resp, err := svc.GetProblemMonitors(ctx, tenantID, &models.DashboardListQuery{
		Range: models.DashboardRange24h,
		Limit: 5,
	})
	if err != nil {
		t.Fatalf("GetProblemMonitors() error = %v", err)
	}
	if len(resp.ProblemMonitors) != 1 {
		t.Fatalf("ProblemMonitors length = %d, want 1", len(resp.ProblemMonitors))
	}
	got := resp.ProblemMonitors[0]
	if got.FailureCount+got.ErrorCount != 7 {
		t.Fatalf("failures+errors = %d, want 7", got.FailureCount+got.ErrorCount)
	}
	if math.Abs(got.Uptime-30) > 0.01 {
		t.Fatalf("Uptime = %f, want 30", got.Uptime)
	}
}

func TestService_GetOverview_24hHourlyRollupSmoke(t *testing.T) {
	testcontainers.SkipIfProviderIsNotHealthy(t)

	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "smoke-24h")
	monitorID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "smoke-mon")

	now := time.Now().UTC()
	endHour := now.Truncate(time.Hour)
	// Seed 5 hourly rollup rows in the past, each 10 total / 5 success.
	for i := 1; i <= 5; i++ {
		bucket := endHour.Add(time.Duration(-i) * time.Hour)
		testutil.InsertHourlyRollup(ctx, t, dbClient, tenantID, monitorID, bucket, 10, 5, 500, 5, "failure", bucket.Add(59*time.Minute))
	}
	testutil.InsertRollupJobState(ctx, t, dbClient, "monitor_daily_rollups", now.Add(-1*time.Minute), uuid.New())

	svc := NewService(dbClient, nil, sharedanalytics.NewRepository(dbClient), &fakeTenantSettingsReader{}, nil)
	resp, err := svc.GetOverview(ctx, tenantID, &models.DashboardOverviewQuery{Range: models.DashboardRange24h})
	if err != nil {
		t.Fatalf("GetOverview() error = %v", err)
	}
	// Per-monitor uptime = 25/50 = 50% (single monitor).
	if u := derefUptime(t, resp.Stats.OverallUptime); math.Abs(u-50) > 0.01 {
		t.Fatalf("OverallUptime = %f, want 50", u)
	}
	// Activity sums: 5 buckets * 10 checks = 50 checks; 5 * 5 failures = 25 failures.
	totalChecks := 0
	totalFailures := 0
	for _, p := range resp.Activity24h {
		totalChecks += p.Checks
		totalFailures += p.Failures
	}
	if totalChecks != 50 {
		t.Fatalf("activity check sum = %d, want 50", totalChecks)
	}
	if totalFailures != 25 {
		t.Fatalf("activity failure sum = %d, want 25", totalFailures)
	}
	if len(resp.ProblemMonitors) != 1 {
		t.Fatalf("ProblemMonitors length = %d, want 1", len(resp.ProblemMonitors))
	}
}

func TestService_GetProblemMonitors_1hUsesNarrowWindowNotLast24h(t *testing.T) {
	testcontainers.SkipIfProviderIsNotHealthy(t)

	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "problems-1h")
	monitorID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "problem-mon-1h")

	now := time.Now().UTC()
	// One failure 30 minutes ago (inside both 1h and 24h windows).
	recent := now.Add(-30 * time.Minute)
	testutil.InsertCheckResult(ctx, t, dbClient, tenantID, monitorID, recent, "failure", "monitor", nil)
	// One failure 12 hours ago (outside 1h window, inside 24h window).
	old := now.Add(-12 * time.Hour)
	testutil.InsertCheckResult(ctx, t, dbClient, tenantID, monitorID, old, "failure", "monitor", nil)

	svc := NewService(dbClient, nil, sharedanalytics.NewRepository(dbClient), &fakeTenantSettingsReader{}, nil)
	resp, err := svc.GetProblemMonitors(ctx, tenantID, &models.DashboardListQuery{
		Range: models.DashboardRange1h,
		Limit: 5,
	})
	if err != nil {
		t.Fatalf("GetProblemMonitors() error = %v", err)
	}
	if len(resp.ProblemMonitors) != 1 {
		t.Fatalf("ProblemMonitors length = %d, want 1", len(resp.ProblemMonitors))
	}
	got := resp.ProblemMonitors[0]
	// 1h window must see only the recent failure, not the 12h-old one.
	if got.FailureCount != 1 {
		t.Fatalf("FailureCount = %d, want 1 (12h-old failure must be outside 1h window)", got.FailureCount)
	}
}

func TestService_GetRecentFailures_ResolvedAtTagsLimitAndOrdering(t *testing.T) {
	testcontainers.SkipIfProviderIsNotHealthy(t)

	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "recent-failures")
	monitorA := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "rf-monitor-a")
	monitorB := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "rf-monitor-b")
	setMonitorTags(ctx, t, dbClient, monitorA, []string{"prod"})
	setMonitorTags(ctx, t, dbClient, monitorB, []string{"api"})

	now := time.Now().UTC().Truncate(time.Second)
	// Monitor A timeline: success before any failure must NOT count as resolution;
	// each failure resolves at the FIRST success strictly after it.
	testutil.InsertCheckResult(ctx, t, dbClient, tenantID, monitorA, now.Add(-11*time.Hour), "success", "monitor", testutil.IntPtr(100))
	failA1 := now.Add(-10 * time.Hour)
	testutil.InsertCheckResult(ctx, t, dbClient, tenantID, monitorA, failA1, "failure", "monitor", nil)
	succA1 := now.Add(-9 * time.Hour)
	testutil.InsertCheckResult(ctx, t, dbClient, tenantID, monitorA, succA1, "success", "monitor", testutil.IntPtr(110))
	failA2 := now.Add(-8 * time.Hour)
	testutil.InsertCheckResult(ctx, t, dbClient, tenantID, monitorA, failA2, "failure", "monitor", nil)
	succA2First := now.Add(-7*time.Hour - 30*time.Minute)
	testutil.InsertCheckResult(ctx, t, dbClient, tenantID, monitorA, succA2First, "success", "monitor", testutil.IntPtr(120))
	testutil.InsertCheckResult(ctx, t, dbClient, tenantID, monitorA, now.Add(-7*time.Hour), "success", "monitor", testutil.IntPtr(130))
	// Monitor B timeline: failure + error with no later success → still firing.
	failB1 := now.Add(-6 * time.Hour)
	testutil.InsertCheckResult(ctx, t, dbClient, tenantID, monitorB, failB1, "failure", "monitor", nil)
	errB2 := now.Add(-5 * time.Hour)
	testutil.InsertCheckResult(ctx, t, dbClient, tenantID, monitorB, errB2, "error", "monitor", nil)

	svc := NewService(dbClient, nil, sharedanalytics.NewRepository(dbClient), &fakeTenantSettingsReader{}, nil)

	resp, err := svc.GetRecentFailures(ctx, tenantID, &models.DashboardListQuery{
		Range: models.DashboardRange24h,
		Limit: 10,
	})
	if err != nil {
		t.Fatalf("GetRecentFailures() error = %v", err)
	}
	if len(resp.RecentFailures) != 4 {
		t.Fatalf("RecentFailures length = %d, want 4", len(resp.RecentFailures))
	}

	type expectation struct {
		monitorID  uuid.UUID
		status     string
		occurredAt time.Time
		resolvedAt *time.Time
	}
	expected := []expectation{
		{monitorB, "error", errB2, nil},
		{monitorB, "failure", failB1, nil},
		{monitorA, "failure", failA2, &succA2First},
		{monitorA, "failure", failA1, &succA1},
	}
	for i, want := range expected {
		got := resp.RecentFailures[i]
		if got.MonitorID != want.monitorID {
			t.Fatalf("RecentFailures[%d].MonitorID = %s, want %s", i, got.MonitorID, want.monitorID)
		}
		if got.Status != want.status {
			t.Fatalf("RecentFailures[%d].Status = %s, want %s", i, got.Status, want.status)
		}
		if !got.OccurredAt.Equal(want.occurredAt) {
			t.Fatalf("RecentFailures[%d].OccurredAt = %s, want %s", i, got.OccurredAt, want.occurredAt)
		}
		if want.resolvedAt == nil {
			if got.ResolvedAt != nil {
				t.Fatalf("RecentFailures[%d].ResolvedAt = %v, want nil", i, got.ResolvedAt)
			}
			if got.State != models.DashboardFailureStateFiring {
				t.Fatalf("RecentFailures[%d].State = %s, want firing", i, got.State)
			}
		} else {
			if got.ResolvedAt == nil || !got.ResolvedAt.Equal(*want.resolvedAt) {
				t.Fatalf("RecentFailures[%d].ResolvedAt = %v, want %s (first success after failure)", i, got.ResolvedAt, *want.resolvedAt)
			}
			if got.State != models.DashboardFailureStateResolved {
				t.Fatalf("RecentFailures[%d].State = %s, want resolved", i, got.State)
			}
		}
	}

	// Limit smaller than total failures → newest N only, still desc.
	limited, err := svc.GetRecentFailures(ctx, tenantID, &models.DashboardListQuery{
		Range: models.DashboardRange24h,
		Limit: 2,
	})
	if err != nil {
		t.Fatalf("GetRecentFailures(limit=2) error = %v", err)
	}
	if len(limited.RecentFailures) != 2 {
		t.Fatalf("limited RecentFailures length = %d, want 2", len(limited.RecentFailures))
	}
	if !limited.RecentFailures[0].OccurredAt.Equal(errB2) || !limited.RecentFailures[1].OccurredAt.Equal(failB1) {
		t.Fatalf("limited RecentFailures = [%s, %s], want newest two [%s, %s]",
			limited.RecentFailures[0].OccurredAt, limited.RecentFailures[1].OccurredAt, errB2, failB1)
	}

	// Tag filter → only monitorA failures, resolved_at still computed.
	tagged, err := svc.GetRecentFailures(ctx, tenantID, &models.DashboardListQuery{
		Range: models.DashboardRange24h,
		Limit: 10,
		Tags:  []string{"prod"},
	})
	if err != nil {
		t.Fatalf("GetRecentFailures(tags=prod) error = %v", err)
	}
	if len(tagged.RecentFailures) != 2 {
		t.Fatalf("tagged RecentFailures length = %d, want 2", len(tagged.RecentFailures))
	}
	for _, failure := range tagged.RecentFailures {
		if failure.MonitorID != monitorA {
			t.Fatalf("tagged RecentFailures included monitor %s, want only %s", failure.MonitorID, monitorA)
		}
	}
	if tagged.RecentFailures[0].ResolvedAt == nil || !tagged.RecentFailures[0].ResolvedAt.Equal(succA2First) {
		t.Fatalf("tagged RecentFailures[0].ResolvedAt = %v, want %s", tagged.RecentFailures[0].ResolvedAt, succA2First)
	}
}

// TestService_GetSummary_MonitorHealthFollowsStateMachine pins D1 end-to-end:
// for every range the monitor-health grid and ops summary derive displayed
// status from monitors.current_state — including a recovered monitor whose
// LAST in-window check failed, and a 'suspect' monitor — while latest_check_at
// stays range-scoped (rolling totals for 24h, raw window for 1h, daily rollup
// for 7d+).
func TestService_GetSummary_MonitorHealthFollowsStateMachine(t *testing.T) {
	testcontainers.SkipIfProviderIsNotHealthy(t)

	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	svc := NewService(dbClient, nil, sharedanalytics.NewRepository(dbClient), &fakeTenantSettingsReader{}, nil)
	tenantID := testutil.InsertTenant(ctx, t, dbClient, "dashboard-state-machine")
	monRecovered := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "a-recovered")
	monSuspect := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "b-suspect")
	monDown := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "c-down")
	monPaused := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "d-paused")
	monUnknown := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "e-unknown")

	if _, err := dbClient.ExecContext(ctx, `UPDATE monitors SET enabled = FALSE WHERE id = $1`, monPaused); err != nil {
		t.Fatalf("disable paused monitor: %v", err)
	}
	for id, state := range map[uuid.UUID]string{
		monRecovered: "up",
		monSuspect:   "suspect",
		monDown:      "down",
	} {
		if _, err := dbClient.ExecContext(ctx, `UPDATE monitors SET current_state = $2 WHERE id = $1`, id, state); err != nil {
			t.Fatalf("set current_state %s: %v", state, err)
		}
	}

	// Truncate to seconds so timestamps survive the Postgres µs round trip and
	// time.Equal comparisons hold.
	now := time.Now().UTC().Truncate(time.Second)
	// monRecovered's LAST in-window check is a FAILURE, but the state machine has
	// since confirmed recovery (current_state='up'): the dashboard must show up.
	recoveredCheckAt := now.Add(-30 * time.Minute)
	testutil.InsertCheckResult(ctx, t, dbClient, tenantID, monRecovered, recoveredCheckAt, "failure", "monitor", nil)
	downCheckAt := now.Add(-45 * time.Minute)
	testutil.InsertCheckResult(ctx, t, dbClient, tenantID, monDown, downCheckAt, "failure", "monitor", nil)
	// Cursor 3h back so both raw rows are in the rolling raw tail regardless of
	// where "now" falls within the hour.
	testutil.InsertRollupJobState(ctx, t, dbClient, "monitor_daily_rollups", now.Add(-3*time.Hour), uuid.New())

	// Daily rollup for the 7d path with a CONTRARY latest_status: status must
	// still come from current_state, latest_check_at from the rollup.
	bucketDay := time.Date(now.Year(), now.Month(), now.Day()-1, 0, 0, 0, 0, time.UTC)
	rollupLatestAt := bucketDay.Add(23 * time.Hour)
	testutil.InsertDailyRollup(ctx, t, dbClient, tenantID, monRecovered, bucketDay, 10, 8, 800, 8, "failure", rollupLatestAt)

	healthByID := func(rows []models.DashboardMonitorHealth) map[uuid.UUID]models.DashboardMonitorHealth {
		m := make(map[uuid.UUID]models.DashboardMonitorHealth, len(rows))
		for _, r := range rows {
			m[r.MonitorID] = r
		}
		return m
	}
	assertStatuses := func(t *testing.T, byID map[uuid.UUID]models.DashboardMonitorHealth) {
		t.Helper()
		if got := byID[monRecovered].LatestStatus; got == nil || *got != "success" {
			t.Fatalf("recovered LatestStatus = %v, want success (state machine wins over failing last check)", got)
		}
		if got := byID[monSuspect].LatestStatus; got == nil || *got != "success" {
			t.Fatalf("suspect LatestStatus = %v, want success", got)
		}
		if got := byID[monDown].LatestStatus; got == nil || *got != "failure" {
			t.Fatalf("down LatestStatus = %v, want failure", got)
		}
		if got := byID[monUnknown].LatestStatus; got != nil {
			t.Fatalf("unknown LatestStatus = %v, want nil", got)
		}
	}
	assertOps := func(t *testing.T, ops models.DashboardOpsSummary) {
		t.Helper()
		if ops.UpMonitors != 2 {
			t.Fatalf("UpMonitors = %d, want 2 (up + suspect)", ops.UpMonitors)
		}
		if ops.DownMonitors != 1 {
			t.Fatalf("DownMonitors = %d, want 1", ops.DownMonitors)
		}
		if ops.PausedMonitors != 1 {
			t.Fatalf("PausedMonitors = %d, want 1", ops.PausedMonitors)
		}
	}

	for _, rangeValue := range []models.DashboardRange{models.DashboardRange24h, models.DashboardRange1h, models.DashboardRange7d} {
		t.Run(string(rangeValue), func(t *testing.T) {
			resp, err := svc.GetSummary(ctx, tenantID, &models.DashboardOverviewQuery{Range: rangeValue})
			if err != nil {
				t.Fatalf("GetSummary(%s) error = %v", rangeValue, err)
			}
			if len(resp.MonitorHealth) != 5 {
				t.Fatalf("MonitorHealth length = %d, want 5", len(resp.MonitorHealth))
			}
			byID := healthByID(resp.MonitorHealth)
			assertStatuses(t, byID)
			assertOps(t, resp.OpsSummary)

			switch rangeValue {
			case models.DashboardRange24h, models.DashboardRange1h:
				if got := byID[monRecovered].LatestCheckAt; got == nil || !got.Equal(recoveredCheckAt) {
					t.Fatalf("recovered LatestCheckAt = %v, want %v", got, recoveredCheckAt)
				}
				if got := byID[monDown].LatestCheckAt; got == nil || !got.Equal(downCheckAt) {
					t.Fatalf("down LatestCheckAt = %v, want %v", got, downCheckAt)
				}
				if got := byID[monUnknown].LatestCheckAt; got != nil {
					t.Fatalf("unknown LatestCheckAt = %v, want nil", got)
				}
			case models.DashboardRange7d:
				if got := byID[monRecovered].LatestCheckAt; got == nil || !got.Equal(rollupLatestAt) {
					t.Fatalf("recovered LatestCheckAt = %v, want %v (daily rollup)", got, rollupLatestAt)
				}
			}
		})
	}
}
