package dashboard

import (
	"context"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/google/uuid"
	"github.com/lib/pq"

	"github.com/yassinebenameur/probara/api/internal/models"
	sharedanalytics "github.com/yassinebenameur/probara/shared/analytics"
	shareddb "github.com/yassinebenameur/probara/shared/db"
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
		WHERE tenant_id = $1
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
		ORDER BY id
	`)).
		WithArgs(tenantID).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(firstMonitorID))

	analytics := &fakeAnalyticsReader{}
	svc := NewService(&shareddb.Client{DB: sqlDB}, nil, analytics, &fakeTenantSettingsReader{})

	stats, err := svc.getStats(context.Background(), tenantID, models.DashboardRange30d, now.AddDate(0, 0, -29), now, nil)
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
				COALESCE(SUM(mdr.total_checks), 0) AS total_checks,
				COALESCE(SUM(mdr.success_checks), 0) AS success_checks,
				COALESCE(SUM(mdr.total_checks - mdr.success_checks), 0) AS problem_checks,
				latest.current_status,
				latest.latest_check_at
			FROM monitors m
			LEFT JOIN monitor_daily_rollups mdr
				ON mdr.monitor_id = m.id
				AND mdr.tenant_id = m.tenant_id
				AND mdr.bucket_day >= $2::date
				AND mdr.bucket_day < $3::date
			LEFT JOIN LATERAL (
				SELECT
					mdr_latest.latest_status AS current_status,
					mdr_latest.latest_check_at
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
			GROUP BY m.id, m.name, latest.current_status, latest.latest_check_at
			HAVING COALESCE(SUM(mdr.total_checks - mdr.success_checks), 0) > 0
			    OR (latest.current_status IS NOT NULL AND latest.current_status <> 'success')
			ORDER BY problem_checks DESC, name ASC
			LIMIT $4
		)
		SELECT id, name, current_status, total_checks, success_checks, problem_checks, latest_check_at
		FROM rollup_candidates
		ORDER BY problem_checks DESC, name ASC
	`)).
		WithArgs(tenantID, rangeStart, rangeEnd, problemMonitorLimit*4).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "name", "current_status", "total_checks", "success_checks", "problem_checks", "latest_check_at",
		}).AddRow(monitorID, "api", "failure", 10, 7, 3, rangeEnd.Add(-2*time.Hour)))

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

	svc := NewService(&shareddb.Client{DB: sqlDB}, nil, &fakeAnalyticsReader{}, &fakeTenantSettingsReader{})

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

	svc := NewService(&shareddb.Client{DB: sqlDB}, nil, &fakeAnalyticsReader{}, &fakeTenantSettingsReader{})

	groups, err := svc.loadGroups(context.Background(), tenantID, models.DashboardRange30d, rangeStart, rangeEnd, nil, []string{"api"})
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

func TestService_LoadGroups_24hUsesHourlyRollups(t *testing.T) {
	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New() error = %v", err)
	}
	defer sqlDB.Close()

	tenantID := uuid.New()
	monitorID := uuid.New()
	rangeStart := time.Date(2026, time.April, 1, 18, 0, 0, 0, time.UTC)
	rangeEnd := time.Date(2026, time.April, 2, 18, 0, 0, 0, time.UTC)

	mock.ExpectQuery("monitor_hourly_rollups").
		WithArgs(tenantID, rangeStart, rangeEnd).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "name", "tags", "current_status", "uptime", "needs_attention",
		}).AddRow(monitorID, "api", "{api}", "failure", 97.5, true))

	svc := NewService(&shareddb.Client{DB: sqlDB}, nil, &fakeAnalyticsReader{}, &fakeTenantSettingsReader{})

	groups, err := svc.loadGroups(context.Background(), tenantID, models.DashboardRange24h, rangeStart, rangeEnd, nil, []string{"api"})
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
