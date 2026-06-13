package dashboard

import (
	"context"
	"io"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/google/uuid"
	"github.com/lib/pq"
	"github.com/sirupsen/logrus"
	logrustest "github.com/sirupsen/logrus/hooks/test"

	"github.com/yassinebenameur/probara/api/internal/models"
	sharedanalytics "github.com/yassinebenameur/probara/shared/analytics"
	shareddb "github.com/yassinebenameur/probara/shared/db"
	"github.com/yassinebenameur/probara/shared/logger"
	"github.com/yassinebenameur/probara/shared/maintenance"
)

type fakeTenantSettingsReader struct{}

func (f *fakeTenantSettingsReader) GetTenantSettings(_ context.Context, _ uuid.UUID) (*models.TenantSettings, error) {
	return &models.TenantSettings{DashboardGroupTags: []string{}}, nil
}

type fakeAnalyticsReader struct {
	calls int
}

func (f *fakeAnalyticsReader) GetScopeAnalytics(ctx context.Context, tenantID uuid.UUID, monitorIDs []uuid.UUID, rangeValue sharedanalytics.Range, now time.Time) (*sharedanalytics.Result, error) {
	f.calls++
	avgLatency := 245.0
	return &sharedanalytics.Result{
		GeneratedAt: now,
		Source:      sharedanalytics.SourceRollup,
		Summary: sharedanalytics.Summary{
			SLAPct:       98.5,
			AvgLatencyMS: &avgLatency,
		},
	}, nil
}

func TestService_GetStats_LongRangeUsesInjectedAnalyticsReader(t *testing.T) {
	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New() error = %v", err)
	}
	defer sqlDB.Close()

	tenantID := uuid.New()
	now := time.Now().UTC()

	mock.ExpectQuery(regexp.QuoteMeta(`
		SELECT
			COUNT(*) AS total_monitors,
			COUNT(*) FILTER (WHERE enabled) AS active_monitors,
			COUNT(*) FILTER (WHERE type = 'http') AS http_monitors,
			COUNT(*) FILTER (WHERE type = 'agent') AS agent_monitors
		FROM monitors
		WHERE tenant_id = $1 AND deleted_at IS NULL
	`)).
		WithArgs(tenantID).
		WillReturnRows(sqlmock.NewRows([]string{
			"total_monitors", "active_monitors", "http_monitors", "agent_monitors",
		}).AddRow(3, 2, 2, 0))

	firstMonitorID := uuid.New()
	mock.ExpectQuery(regexp.QuoteMeta(`
		SELECT id
		FROM monitors
		WHERE tenant_id = $1
		  AND enabled = TRUE
		  AND type <> 'group'
		  AND deleted_at IS NULL
		ORDER BY id
	`)).
		WithArgs(tenantID).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(firstMonitorID))

	analytics := &fakeAnalyticsReader{}
	svc := NewService(&shareddb.Client{DB: sqlDB}, nil, analytics, &fakeTenantSettingsReader{}, nil)

	stats, err := svc.getStats(context.Background(), tenantID, models.DashboardRange30d, now.AddDate(0, 0, -29), now, nil, nil)
	if err != nil {
		t.Fatalf("getStats() error = %v", err)
	}

	if analytics.calls != 1 {
		t.Fatalf("analytics calls = %d, want 1", analytics.calls)
	}
	if stats.OverallUptime != 98.5 {
		t.Fatalf("OverallUptime = %.1f, want 98.5", stats.OverallUptime)
	}
	if stats.AvgResponseMS != 245 {
		t.Fatalf("AvgResponseMS = %.1f, want 245", stats.AvgResponseMS)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("sql expectations: %v", err)
	}
}

func TestService_GetProblemMonitors_LongRangeUsesRollupCandidatesThenScopedRawCounts(t *testing.T) {
	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New() error = %v", err)
	}
	defer sqlDB.Close()

	tenantID := uuid.New()
	monitorID := uuid.New()
	rangeStart := time.Date(2026, time.April, 1, 0, 0, 0, 0, time.UTC)
	rangeEnd := time.Date(2026, time.April, 30, 0, 0, 0, 0, time.UTC)

	mock.ExpectQuery(regexp.QuoteMeta(`
		WITH rollup_candidates AS (
			SELECT
				m.id,
				m.name,
				m.current_state,
				COALESCE(SUM(mdr.total_checks), 0) AS total_checks,
				COALESCE(SUM(mdr.success_checks), 0) AS success_checks,
				COALESCE(SUM(mdr.total_checks - mdr.success_checks), 0) AS problem_checks,
				latest.latest_check_at
			FROM monitors m
			LEFT JOIN monitor_daily_rollups mdr
				ON mdr.monitor_id = m.id
				AND mdr.tenant_id = m.tenant_id
				AND mdr.bucket_day >= $2::date
				AND mdr.bucket_day < $3::date
			LEFT JOIN LATERAL (
				SELECT mdr_latest.latest_check_at
				FROM monitor_daily_rollups mdr_latest
				WHERE mdr_latest.monitor_id = m.id
				  AND mdr_latest.tenant_id = m.tenant_id
				  AND mdr_latest.bucket_day >= $2::date
				  AND mdr_latest.bucket_day < $3::date
				ORDER BY mdr_latest.bucket_day DESC
				LIMIT 1
			) latest ON TRUE
			WHERE m.tenant_id = $1
			  AND m.enabled = TRUE
			  AND m.type <> 'group'
			  AND m.deleted_at IS NULL
			GROUP BY m.id, m.name, m.current_state, latest.latest_check_at
			HAVING COALESCE(SUM(mdr.total_checks - mdr.success_checks), 0) > 0
			    OR m.current_state = 'down'
			ORDER BY problem_checks DESC, name ASC
			LIMIT $4
		)
		SELECT id, name, current_state, total_checks, success_checks, problem_checks, latest_check_at
		FROM rollup_candidates
		ORDER BY problem_checks DESC, name ASC
	`)).
		WithArgs(tenantID, rangeStart, rangeEnd, problemMonitorLimit*4).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "name", "current_state", "total_checks", "success_checks", "problem_checks", "latest_check_at",
		}).AddRow(monitorID, "api", "down", 10, 7, 3, rangeEnd.Add(-2*time.Hour)))

	mock.ExpectQuery(regexp.QuoteMeta(`
		SELECT
			cr.monitor_id,
			COUNT(*) FILTER (WHERE cr.status = 'failure') AS failure_count,
			COUNT(*) FILTER (WHERE cr.status = 'error') AS error_count,
			MAX(cr.created_at) FILTER (WHERE cr.status IN ('failure', 'error')) AS latest_failure_at
		FROM check_results cr
		WHERE cr.tenant_id = $1
		  AND cr.result_source <> 'platform'
		  AND cr.created_at >= $2
		  AND cr.created_at < $3
		  AND cr.monitor_id = ANY($4)
		GROUP BY cr.monitor_id
	`)).
		WithArgs(tenantID, rangeStart, rangeEnd, pq.Array([]uuid.UUID{monitorID})).
		WillReturnRows(sqlmock.NewRows([]string{
			"monitor_id", "failure_count", "error_count", "latest_failure_at",
		}).AddRow(monitorID, 2, 1, rangeEnd.Add(-time.Hour)))

	svc := NewService(&shareddb.Client{DB: sqlDB}, nil, &fakeAnalyticsReader{}, &fakeTenantSettingsReader{}, nil)

	monitors, err := svc.getProblemMonitors(context.Background(), tenantID, models.DashboardRange30d, rangeStart, rangeEnd, problemMonitorLimit, nil)
	if err != nil {
		t.Fatalf("getProblemMonitors() error = %v", err)
	}
	if len(monitors) != 1 {
		t.Fatalf("len(monitors) = %d, want 1", len(monitors))
	}
	if monitors[0].FailureCount != 2 || monitors[0].ErrorCount != 1 {
		t.Fatalf("counts = (%d,%d), want (2,1)", monitors[0].FailureCount, monitors[0].ErrorCount)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("sql expectations: %v", err)
	}
}

func TestService_LoadGroups_LongRangeUsesRollups(t *testing.T) {
	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New() error = %v", err)
	}
	defer sqlDB.Close()

	tenantID := uuid.New()
	monitorID := uuid.New()
	rangeStart := time.Date(2026, time.April, 1, 0, 0, 0, 0, time.UTC)
	rangeEnd := time.Date(2026, time.April, 30, 0, 0, 0, 0, time.UTC)

	mock.ExpectQuery("monitor_daily_rollups").
		WithArgs(tenantID, rangeStart, rangeEnd).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "name", "tags", "current_status", "uptime", "needs_attention",
		}).AddRow(monitorID, "api", "{api}", "success", 99.5, false))

	svc := NewService(&shareddb.Client{DB: sqlDB}, nil, &fakeAnalyticsReader{}, &fakeTenantSettingsReader{}, nil)

	groups, err := svc.loadGroups(context.Background(), tenantID, models.DashboardRange30d, rangeStart, rangeEnd, nil, []string{"api"}, nil)
	if err != nil {
		t.Fatalf("loadGroups() error = %v", err)
	}
	if len(groups) != 1 {
		t.Fatalf("len(groups) = %d, want 1", len(groups))
	}
	if groups[0].MonitorCount != 1 {
		t.Fatalf("MonitorCount = %d, want 1", groups[0].MonitorCount)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("sql expectations: %v", err)
	}
}

func TestService_LoadGroups_24hUsesExactRollingHelper(t *testing.T) {
	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New() error = %v", err)
	}
	defer sqlDB.Close()

	tenantID := uuid.New()
	monitorID := uuid.New()
	rangeStart := time.Date(2026, time.April, 1, 18, 0, 0, 0, time.UTC)
	rangeEnd := time.Date(2026, time.April, 2, 18, 0, 0, 0, time.UTC)

	// Step 1: listGroupMonitorRows queries the monitors table.
	latestStatus := "failure"
	mock.ExpectQuery("SELECT m.id, m.name").
		WithArgs(tenantID).
		WillReturnRows(sqlmock.NewRows([]string{"id", "name", "tags"}).
			AddRow(monitorID, "api", "{api}"))

	// Step 2: loadExactRolling24hSummary reads the rollup cursor first (cheap
	// one-row lookup), then runs the stitched query with parameter bounds.
	mock.ExpectQuery("rollup_job_state").
		WillReturnRows(sqlmock.NewRows([]string{"last_created_at", "last_check_result_id"}))
	mock.ExpectQuery("monitor_hourly_rollups").
		WithArgs(tenantID, sqlmock.AnyArg(), sqlmock.AnyArg(), pq.Array([]uuid.UUID{monitorID}), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{
			"monitor_id", "total_checks", "success_checks",
			"failure_checks_raw", "error_checks_raw", "bad_checks_rollup", "error_checks_rollup",
			"latency_sum_ms", "latency_count", "latest_status", "latest_check_at",
		}).AddRow(monitorID, 40, 39, 0, 0, 1, 0, 3900.0, 39, latestStatus, time.Now().UTC()))

	svc := NewService(&shareddb.Client{DB: sqlDB}, nil, &fakeAnalyticsReader{}, &fakeTenantSettingsReader{}, nil)

	groups, err := svc.loadGroups(context.Background(), tenantID, models.DashboardRange24h, rangeStart, rangeEnd, nil, []string{"api"}, nil)
	if err != nil {
		t.Fatalf("loadGroups() error = %v", err)
	}
	if len(groups) != 1 {
		t.Fatalf("len(groups) = %d, want 1", len(groups))
	}
	if groups[0].AttentionCount != 1 {
		t.Fatalf("AttentionCount = %d, want 1", groups[0].AttentionCount)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("sql expectations: %v", err)
	}
}

// TestService_LoadRolling24hData_SecondCallAndProblemMonitorsHitCache asserts
// that the heavy rolling-24h queries (monitor list, exact-rolling summary,
// hourly series) run exactly ONCE: a second loadRolling24hData call within the
// TTL is served from the cache, and getProblemMonitors24h reuses the cached
// totals + monitor set, adding only its cheap top-N name lookup. sqlmock uses
// ordered expectations, so any extra DB query fails the test.
func TestService_LoadRolling24hData_SecondCallAndProblemMonitorsHitCache(t *testing.T) {
	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New() error = %v", err)
	}
	defer sqlDB.Close()

	tenantID := uuid.New()
	monitorID := uuid.New()
	latestStatus := "failure"
	latestAt := time.Now().UTC().Add(-10 * time.Minute)

	// Build pass 1 of 1: listEnabledOperationalMonitorIDs.
	mock.ExpectQuery(regexp.QuoteMeta(`
		SELECT id
		FROM monitors
		WHERE tenant_id = $1
		  AND enabled = TRUE
		  AND type <> 'group'
		  AND deleted_at IS NULL
		ORDER BY id
	`)).
		WithArgs(tenantID).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(monitorID))

	// Cursor-lag warning check: one cheap cursor read at the top of the build.
	mock.ExpectQuery("rollup_job_state").
		WillReturnRows(sqlmock.NewRows([]string{"last_created_at", "last_check_result_id"}))

	// loadExactRolling24hSummary: cursor read + stitched query.
	mock.ExpectQuery("rollup_job_state").
		WillReturnRows(sqlmock.NewRows([]string{"last_created_at", "last_check_result_id"}))
	mock.ExpectQuery("monitor_hourly_rollups").
		WillReturnRows(sqlmock.NewRows([]string{
			"monitor_id", "total_checks", "success_checks",
			"failure_checks_raw", "error_checks_raw", "bad_checks_rollup", "error_checks_rollup",
			"latency_sum_ms", "latency_count", "latest_status", "latest_check_at",
		}).AddRow(monitorID, 40, 38, 1, 1, 0, 0, 3800.0, 38, latestStatus, latestAt))

	// loadHourlyBucketSeries24h: cursor read + series query (no rows needed —
	// the helper pre-fills 24 empty buckets).
	mock.ExpectQuery("rollup_job_state").
		WillReturnRows(sqlmock.NewRows([]string{"last_created_at", "last_check_result_id"}))
	mock.ExpectQuery("raw_per_hour").
		WillReturnRows(sqlmock.NewRows([]string{
			"bucket_hour", "total_checks", "success_checks", "latency_sum_ms", "latency_count",
		}))

	svc := NewService(&shareddb.Client{DB: sqlDB}, nil, &fakeAnalyticsReader{}, &fakeTenantSettingsReader{}, nil)
	ctx := context.Background()

	data1, ids1, err := svc.loadRolling24hData(ctx, tenantID, nil)
	if err != nil {
		t.Fatalf("loadRolling24hData() #1 error = %v", err)
	}
	if len(ids1) != 1 || ids1[0] != monitorID {
		t.Fatalf("monitorIDs = %v, want [%s]", ids1, monitorID)
	}

	// Second call within the TTL: must be served from the cache (no new
	// sqlmock expectations are registered, so any DB hit errors out).
	data2, ids2, err := svc.loadRolling24hData(ctx, tenantID, nil)
	if err != nil {
		t.Fatalf("loadRolling24hData() #2 error = %v", err)
	}
	if data1 != data2 {
		t.Fatalf("second call did not return the cached data pointer")
	}
	if len(ids2) != 1 || ids2[0] != monitorID {
		t.Fatalf("cached monitorIDs = %v, want [%s]", ids2, monitorID)
	}

	// Problem monitors reuse the cached totals + monitor set; only the cheap
	// top-N name lookup hits the DB.
	mock.ExpectQuery(regexp.QuoteMeta(`
		SELECT id, name, current_state
		FROM monitors
		WHERE tenant_id = $1 AND id = ANY($2) AND deleted_at IS NULL
	`)).
		WithArgs(tenantID, pq.Array([]uuid.UUID{monitorID})).
		WillReturnRows(sqlmock.NewRows([]string{"id", "name", "current_state"}).
			AddRow(monitorID, "api", "down"))

	monitors, err := svc.getProblemMonitors24h(ctx, tenantID, time.Time{}, time.Time{}, problemMonitorLimit, nil)
	if err != nil {
		t.Fatalf("getProblemMonitors24h() error = %v", err)
	}
	if len(monitors) != 1 || monitors[0].MonitorID != monitorID {
		t.Fatalf("monitors = %+v, want one entry for %s", monitors, monitorID)
	}
	if monitors[0].FailureCount != 1 || monitors[0].ErrorCount != 1 {
		t.Fatalf("counts = (%d,%d), want (1,1)", monitors[0].FailureCount, monitors[0].ErrorCount)
	}
	if monitors[0].LatestFailureAt == nil || !monitors[0].LatestFailureAt.Equal(latestAt) {
		t.Fatalf("LatestFailureAt = %v, want %v", monitors[0].LatestFailureAt, latestAt)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("sql expectations: %v", err)
	}
}

func TestLoadHourlyBucketSeries24h_BoundsRawScanAtRollupCursor(t *testing.T) {
	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New() error = %v", err)
	}
	defer sqlDB.Close()

	tenantID := uuid.New()
	monitorID := uuid.New()
	now := time.Date(2026, time.June, 1, 11, 54, 0, 0, time.UTC)
	startHour := now.Truncate(time.Hour).Add(-23 * time.Hour)

	rows := sqlmock.NewRows([]string{
		"bucket_hour", "total_checks", "success_checks", "latency_sum_ms", "latency_count",
	})
	for i := 0; i < 24; i++ {
		rows.AddRow(startHour.Add(time.Duration(i)*time.Hour), int64(0), int64(0), 0.0, int64(0))
	}

	// The cursor is read first; the raw scan in the main query must then start
	// at the cursor (not startHour) via a plain parameter the planner can use.
	cursorAt := now.Add(-30 * time.Minute)
	cursorID := uuid.New()
	mock.ExpectQuery("rollup_job_state").
		WillReturnRows(sqlmock.NewRows([]string{"last_created_at", "last_check_result_id"}).
			AddRow(cursorAt, cursorID))

	mock.ExpectQuery("raw_per_hour").
		WithArgs(tenantID, startHour, startHour.Add(24*time.Hour), pq.Array([]uuid.UUID{monitorID}), cursorAt, cursorAt, cursorID).
		WillReturnRows(rows)

	series, err := loadHourlyBucketSeries24h(context.Background(), &shareddb.Client{DB: sqlDB}, tenantID, []uuid.UUID{monitorID}, now)
	if err != nil {
		t.Fatalf("loadHourlyBucketSeries24h() error = %v", err)
	}
	if len(series) != 24 {
		t.Fatalf("len(series) = %d, want 24", len(series))
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("sql expectations: %v", err)
	}
}

// TestService_GetMonitorHealth_24hStatusFromStateMachineNoLateral pins the D1
// behaviour for the 24h range: latest_status comes from monitors.current_state
// (a monitor whose last in-window check FAILED but whose state machine says
// 'up' displays as up; 'suspect' displays as up; 'unknown' as nil), and
// latest_check_at comes from the precomputed rolling-24h totals. The exact
// query shape is pinned via QuoteMeta: there is no LATERAL on check_results,
// and sqlmock's ordered expectations prove no other query runs at all.
func TestService_GetMonitorHealth_24hStatusFromStateMachineNoLateral(t *testing.T) {
	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New() error = %v", err)
	}
	defer sqlDB.Close()

	tenantID := uuid.New()
	monRecovered := uuid.New() // current_state 'up', last in-window check 'failure'
	monSuspect := uuid.New()   // current_state 'suspect'
	monDown := uuid.New()      // current_state 'down'
	monUnknown := uuid.New()   // current_state 'unknown'

	mock.ExpectQuery(regexp.QuoteMeta(`
		SELECT m.id, m.name, m.enabled, m.current_state, ` + maintenance.InMaintenancePredicate("m") + ` AS in_maintenance
		FROM monitors m
		WHERE m.tenant_id = $1 AND m.deleted_at IS NULL
	 ORDER BY m.name`)).
		WithArgs(tenantID).
		WillReturnRows(sqlmock.NewRows([]string{"id", "name", "enabled", "current_state", "in_maintenance"}).
			AddRow(monDown, "a-down", true, "down", false).
			AddRow(monRecovered, "b-recovered", true, "up", false).
			AddRow(monSuspect, "c-suspect", true, "suspect", false).
			AddRow(monUnknown, "d-unknown", true, "unknown", false))

	failure := "failure"
	recoveredAt := time.Now().UTC().Add(-5 * time.Minute)
	precomp := &rolling24hData{
		totals: map[uuid.UUID]MonitorRolling24hTotals{
			// Last in-window check failed, but the state machine has since
			// confirmed recovery: the dashboard must show the state, not the raw row.
			monRecovered: {TotalChecks: 10, SuccessChecks: 9, FailureChecks: 1, LatestStatus: &failure, LatestCheckAt: &recoveredAt},
		},
	}

	svc := NewService(&shareddb.Client{DB: sqlDB}, nil, &fakeAnalyticsReader{}, &fakeTenantSettingsReader{}, nil)
	now := time.Now().UTC()
	health, err := svc.getMonitorHealth(context.Background(), tenantID, models.DashboardRange24h, now.Add(-24*time.Hour), now, nil, precomp)
	if err != nil {
		t.Fatalf("getMonitorHealth() error = %v", err)
	}
	if len(health) != 4 {
		t.Fatalf("len(health) = %d, want 4", len(health))
	}

	byID := make(map[uuid.UUID]models.DashboardMonitorHealth, len(health))
	for _, h := range health {
		byID[h.MonitorID] = h
	}
	if got := byID[monDown].LatestStatus; got == nil || *got != "failure" {
		t.Fatalf("down monitor LatestStatus = %v, want failure", got)
	}
	if got := byID[monRecovered].LatestStatus; got == nil || *got != "success" {
		t.Fatalf("recovered monitor LatestStatus = %v, want success (state machine, not raw last check)", got)
	}
	if got := byID[monSuspect].LatestStatus; got == nil || *got != "success" {
		t.Fatalf("suspect monitor LatestStatus = %v, want success", got)
	}
	if got := byID[monUnknown].LatestStatus; got != nil {
		t.Fatalf("unknown monitor LatestStatus = %v, want nil", got)
	}
	if got := byID[monRecovered].LatestCheckAt; got == nil || !got.Equal(recoveredAt) {
		t.Fatalf("recovered monitor LatestCheckAt = %v, want %v (from precomp totals)", got, recoveredAt)
	}
	if got := byID[monUnknown].LatestCheckAt; got != nil {
		t.Fatalf("unknown monitor LatestCheckAt = %v, want nil (no totals entry)", got)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("sql expectations (no extra query / no LATERAL): %v", err)
	}
}

// TestService_GetMonitorHealth_1hBatchedLatestCheckAt pins the 1h path: one
// monitors query (status from current_state) plus ONE batched MAX(created_at)
// aggregate over the raw window — not a per-monitor LATERAL.
func TestService_GetMonitorHealth_1hBatchedLatestCheckAt(t *testing.T) {
	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New() error = %v", err)
	}
	defer sqlDB.Close()

	tenantID := uuid.New()
	monitorID := uuid.New()
	rangeStart := time.Date(2026, time.June, 1, 10, 0, 0, 0, time.UTC)
	rangeEnd := rangeStart.Add(time.Hour)
	lastCheckAt := rangeEnd.Add(-3 * time.Minute)

	mock.ExpectQuery(regexp.QuoteMeta(`
		SELECT m.id, m.name, m.enabled, m.current_state, ` + maintenance.InMaintenancePredicate("m") + ` AS in_maintenance
		FROM monitors m
		WHERE m.tenant_id = $1 AND m.deleted_at IS NULL
	 ORDER BY m.name`)).
		WithArgs(tenantID).
		WillReturnRows(sqlmock.NewRows([]string{"id", "name", "enabled", "current_state", "in_maintenance"}).
			AddRow(monitorID, "api", true, "down", false))

	mock.ExpectQuery(regexp.QuoteMeta(`
		SELECT cr.monitor_id, MAX(cr.created_at) AS latest_check_at
		FROM check_results cr
		JOIN monitors m ON m.id = cr.monitor_id AND m.tenant_id = cr.tenant_id
		WHERE cr.tenant_id = $1
		  AND m.deleted_at IS NULL
		  AND cr.result_source <> 'platform'
		  AND cr.created_at >= $2
		  AND cr.created_at < $3
		  
		GROUP BY cr.monitor_id
	`)).
		WithArgs(tenantID, rangeStart, rangeEnd).
		WillReturnRows(sqlmock.NewRows([]string{"monitor_id", "latest_check_at"}).
			AddRow(monitorID, lastCheckAt))

	svc := NewService(&shareddb.Client{DB: sqlDB}, nil, &fakeAnalyticsReader{}, &fakeTenantSettingsReader{}, nil)
	health, err := svc.getMonitorHealth(context.Background(), tenantID, models.DashboardRange1h, rangeStart, rangeEnd, nil, nil)
	if err != nil {
		t.Fatalf("getMonitorHealth() error = %v", err)
	}
	if len(health) != 1 {
		t.Fatalf("len(health) = %d, want 1", len(health))
	}
	if health[0].LatestStatus == nil || *health[0].LatestStatus != "failure" {
		t.Fatalf("LatestStatus = %v, want failure (current_state down)", health[0].LatestStatus)
	}
	if health[0].LatestCheckAt == nil || !health[0].LatestCheckAt.Equal(lastCheckAt) {
		t.Fatalf("LatestCheckAt = %v, want %v", health[0].LatestCheckAt, lastCheckAt)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("sql expectations: %v", err)
	}
}

// TestService_GetMonitorHealth_RollupRangeStatusFromState pins the 7d+ path:
// latest_check_at still comes from the newest daily-rollup row, but the status
// is derived from monitors.current_state (the rollup's latest_status column is
// no longer selected at all).
func TestService_GetMonitorHealth_RollupRangeStatusFromState(t *testing.T) {
	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New() error = %v", err)
	}
	defer sqlDB.Close()

	tenantID := uuid.New()
	monitorID := uuid.New()
	rangeStart := time.Date(2026, time.May, 1, 0, 0, 0, 0, time.UTC)
	rangeEnd := time.Date(2026, time.May, 31, 0, 0, 0, 0, time.UTC)
	rollupLatestAt := rangeEnd.Add(-2 * time.Hour)

	mock.ExpectQuery(regexp.QuoteMeta(`
		SELECT
			m.id,
			m.name,
			m.enabled,
			m.current_state,
			lr.latest_check_at,
			`+maintenance.InMaintenancePredicate("m")+` AS in_maintenance
		FROM monitors m
		LEFT JOIN LATERAL (
			SELECT mdr.latest_check_at
			FROM monitor_daily_rollups mdr
			WHERE mdr.monitor_id = m.id
			  AND mdr.tenant_id = m.tenant_id
			  AND mdr.bucket_day >= $2::date
			  AND mdr.bucket_day < $3::date
			ORDER BY mdr.bucket_day DESC
			LIMIT 1
		) lr ON TRUE
		WHERE m.tenant_id = $1 AND m.deleted_at IS NULL
		ORDER BY m.name
	`)).
		WithArgs(tenantID, rangeStart, rangeEnd).
		WillReturnRows(sqlmock.NewRows([]string{"id", "name", "enabled", "current_state", "latest_check_at", "in_maintenance"}).
			AddRow(monitorID, "api", true, "suspect", rollupLatestAt, false))

	svc := NewService(&shareddb.Client{DB: sqlDB}, nil, &fakeAnalyticsReader{}, &fakeTenantSettingsReader{}, nil)
	health, err := svc.getMonitorHealth(context.Background(), tenantID, models.DashboardRange30d, rangeStart, rangeEnd, nil, nil)
	if err != nil {
		t.Fatalf("getMonitorHealth() error = %v", err)
	}
	if len(health) != 1 {
		t.Fatalf("len(health) = %d, want 1", len(health))
	}
	if health[0].LatestStatus == nil || *health[0].LatestStatus != "success" {
		t.Fatalf("LatestStatus = %v, want success (current_state suspect)", health[0].LatestStatus)
	}
	if health[0].LatestCheckAt == nil || !health[0].LatestCheckAt.Equal(rollupLatestAt) {
		t.Fatalf("LatestCheckAt = %v, want %v (daily rollup)", health[0].LatestCheckAt, rollupLatestAt)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("sql expectations: %v", err)
	}
}

// TestService_GetOpsSummary_CountsFollowStateMachine asserts the up/down/paused
// counters over health rows derived from current_state: down → down, up and
// suspect → up, unknown → skipped, disabled → paused.
func TestService_GetOpsSummary_CountsFollowStateMachine(t *testing.T) {
	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New() error = %v", err)
	}
	defer sqlDB.Close()

	tenantID := uuid.New()
	mock.ExpectQuery("FROM alerts").
		WithArgs(tenantID).
		WillReturnRows(sqlmock.NewRows([]string{"active_alerts", "acknowledged_alerts"}).AddRow(0, 0))

	rowFor := func(state string, enabled bool) models.DashboardMonitorHealth {
		return models.DashboardMonitorHealth{
			MonitorID:    uuid.New(),
			Enabled:      enabled,
			LatestStatus: monitorStateToStatus(state),
		}
	}
	health := []models.DashboardMonitorHealth{
		rowFor("up", true),
		rowFor("suspect", true),
		rowFor("down", true),
		rowFor("unknown", true),
		rowFor("down", false), // disabled wins: paused even though state is down
	}

	svc := NewService(&shareddb.Client{DB: sqlDB}, nil, &fakeAnalyticsReader{}, &fakeTenantSettingsReader{}, nil)
	summary, err := svc.getOpsSummary(context.Background(), tenantID, health, nil)
	if err != nil {
		t.Fatalf("getOpsSummary() error = %v", err)
	}
	if summary.UpMonitors != 2 {
		t.Fatalf("UpMonitors = %d, want 2 (up + suspect)", summary.UpMonitors)
	}
	if summary.DownMonitors != 1 {
		t.Fatalf("DownMonitors = %d, want 1", summary.DownMonitors)
	}
	if summary.PausedMonitors != 1 {
		t.Fatalf("PausedMonitors = %d, want 1", summary.PausedMonitors)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("sql expectations: %v", err)
	}
}

// TestService_LoadRolling24hData_RollupCursorLagWarning asserts the structured
// warning fires when the rollup cursor trails now by more than the threshold,
// and stays silent when the cursor is fresh.
func TestService_LoadRolling24hData_RollupCursorLagWarning(t *testing.T) {
	run := func(t *testing.T, cursorAt time.Time) []*logrus.Entry {
		t.Helper()
		sqlDB, mock, err := sqlmock.New()
		if err != nil {
			t.Fatalf("sqlmock.New() error = %v", err)
		}
		defer sqlDB.Close()

		tenantID := uuid.New()
		monitorID := uuid.New()
		cursorID := uuid.New()

		mock.ExpectQuery("SELECT id").
			WithArgs(tenantID).
			WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(monitorID))
		// Lag-check cursor read.
		mock.ExpectQuery("rollup_job_state").
			WillReturnRows(sqlmock.NewRows([]string{"last_created_at", "last_check_result_id"}).
				AddRow(cursorAt, cursorID))
		// loadExactRolling24hSummary: cursor read + stitched query.
		mock.ExpectQuery("rollup_job_state").
			WillReturnRows(sqlmock.NewRows([]string{"last_created_at", "last_check_result_id"}).
				AddRow(cursorAt, cursorID))
		mock.ExpectQuery("monitor_hourly_rollups").
			WillReturnRows(sqlmock.NewRows([]string{
				"monitor_id", "total_checks", "success_checks",
				"failure_checks_raw", "error_checks_raw", "bad_checks_rollup", "error_checks_rollup",
				"latency_sum_ms", "latency_count", "latest_status", "latest_check_at",
			}))
		// loadHourlyBucketSeries24h: cursor read + series query.
		mock.ExpectQuery("rollup_job_state").
			WillReturnRows(sqlmock.NewRows([]string{"last_created_at", "last_check_result_id"}).
				AddRow(cursorAt, cursorID))
		mock.ExpectQuery("raw_per_hour").
			WillReturnRows(sqlmock.NewRows([]string{
				"bucket_hour", "total_checks", "success_checks", "latency_sum_ms", "latency_count",
			}))

		log := logger.New("dashboard-test", "warn")
		log.Logger.SetOutput(io.Discard)
		hook := logrustest.NewLocal(log.Logger)

		svc := NewService(&shareddb.Client{DB: sqlDB}, nil, &fakeAnalyticsReader{}, &fakeTenantSettingsReader{}, log)
		if _, _, err := svc.loadRolling24hData(context.Background(), tenantID, nil); err != nil {
			t.Fatalf("loadRolling24hData() error = %v", err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatalf("sql expectations: %v", err)
		}
		return hook.AllEntries()
	}

	t.Run("lagging cursor warns", func(t *testing.T) {
		entries := run(t, time.Now().UTC().Add(-3*time.Hour))
		if len(entries) != 1 {
			t.Fatalf("log entries = %d, want 1 warning", len(entries))
		}
		entry := entries[0]
		if entry.Level != logrus.WarnLevel {
			t.Fatalf("entry level = %s, want warning", entry.Level)
		}
		if !strings.Contains(entry.Message, "rollup cursor is lagging") {
			t.Fatalf("entry message = %q, want rollup-cursor lag warning", entry.Message)
		}
		if _, ok := entry.Data["lag"]; !ok {
			t.Fatalf("entry fields = %v, want lag field", entry.Data)
		}
	})

	t.Run("fresh cursor is silent", func(t *testing.T) {
		entries := run(t, time.Now().UTC().Add(-10*time.Minute))
		if len(entries) != 0 {
			t.Fatalf("log entries = %d, want 0", len(entries))
		}
	})
}
