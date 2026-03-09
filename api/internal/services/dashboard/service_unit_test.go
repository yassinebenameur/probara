package dashboard

import (
	"context"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/api/internal/models"
	sharedanalytics "github.com/yassinebenameur/probara/shared/analytics"
	shareddb "github.com/yassinebenameur/probara/shared/db"
)

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
	svc := NewService(&shareddb.Client{DB: sqlDB}, nil, analytics)

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
