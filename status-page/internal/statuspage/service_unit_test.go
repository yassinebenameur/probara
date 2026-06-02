package statuspage

import (
	"context"
	"math"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/google/uuid"

	shareddb "github.com/yassinebenameur/probara/shared/db"
)

func TestService_GetGlobalHourlyUptime_UsesHourlyRollupsAndBoundedRawScan(t *testing.T) {
	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New() error = %v", err)
	}
	defer sqlDB.Close()

	statusPageID := uuid.New()
	tenantID := uuid.New()
	monitorA := uuid.New()
	monitorB := uuid.New()

	mock.ExpectQuery(regexp.QuoteMeta(`
		SELECT spsm.monitor_id
		FROM status_page_sections sps
		JOIN status_page_section_monitors spsm ON spsm.section_id = sps.id
		WHERE sps.status_page_id = $1
		ORDER BY sps.position ASC, spsm.position ASC, spsm.monitor_id ASC
	`)).
		WithArgs(statusPageID).
		WillReturnRows(sqlmock.NewRows([]string{"monitor_id"}).AddRow(monitorA).AddRow(monitorB))

	mock.ExpectQuery(regexp.QuoteMeta(`
		SELECT m.id, m.type
		FROM monitors m
		WHERE m.id = ANY($1) AND m.tenant_id = $2 AND m.deleted_at IS NULL
	`)).
		WithArgs(sqlmock.AnyArg(), tenantID).
		WillReturnRows(sqlmock.NewRows([]string{"id", "type"}).
			AddRow(monitorA, "http").
			AddRow(monitorB, "http"))

	rows := sqlmock.NewRows([]string{"bucket_hour", "total_checks", "success_checks"}).
		AddRow(time.Date(2026, 6, 2, 8, 0, 0, 0, time.UTC), int64(12), int64(10))

	mock.ExpectQuery("(?s)monitor_hourly_rollups.*raw_bounds").
		WithArgs(tenantID, sqlmock.AnyArg()).
		WillReturnRows(rows)

	svc := NewService(&shareddb.Client{DB: sqlDB}, nil)
	if _, err := svc.GetGlobalHourlyUptime(context.Background(), statusPageID, tenantID); err != nil {
		t.Fatalf("GetGlobalHourlyUptime() error = %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestService_BatchCurrentStatus_UsesPerMonitorLateralLimit(t *testing.T) {
	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New() error = %v", err)
	}
	defer sqlDB.Close()

	tenantID := uuid.New()
	monitorA := uuid.New()
	checkedAt := time.Date(2026, 6, 2, 9, 0, 0, 0, time.UTC)

	mock.ExpectQuery("(?s)CROSS JOIN LATERAL.*ORDER BY cr\\.created_at DESC\\s+LIMIT 1").
		WithArgs(sqlmock.AnyArg(), tenantID).
		WillReturnRows(sqlmock.NewRows([]string{
			"monitor_id", "status", "http_status", "latency_ms", "created_at", "metrics_data",
		}).AddRow(monitorA, "success", 200, 123, checkedAt, []byte("{}")))

	svc := NewService(&shareddb.Client{DB: sqlDB}, nil)
	statuses, err := svc.batchCurrentStatus(context.Background(), []uuid.UUID{monitorA}, tenantID)
	if err != nil {
		t.Fatalf("batchCurrentStatus() error = %v", err)
	}
	if statuses[monitorA] == nil || statuses[monitorA].Status != "up" {
		t.Fatalf("batchCurrentStatus() status = %#v, want up", statuses[monitorA])
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestService_BatchHistory_UsesPerMonitorLateralLimit(t *testing.T) {
	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New() error = %v", err)
	}
	defer sqlDB.Close()

	tenantID := uuid.New()
	monitorA := uuid.New()
	checkedAt := time.Date(2026, 6, 2, 9, 0, 0, 0, time.UTC)

	mock.ExpectQuery("(?s)CROSS JOIN LATERAL.*ORDER BY cr\\.created_at DESC\\s+LIMIT 50").
		WithArgs(sqlmock.AnyArg(), tenantID).
		WillReturnRows(sqlmock.NewRows([]string{
			"monitor_id", "status", "latency_ms", "created_at",
		}).AddRow(monitorA, "success", 123, checkedAt))

	svc := NewService(&shareddb.Client{DB: sqlDB}, nil)
	history, err := svc.batchHistory(context.Background(), []uuid.UUID{monitorA}, tenantID)
	if err != nil {
		t.Fatalf("batchHistory() error = %v", err)
	}
	if len(history[monitorA]) != 1 || history[monitorA][0].Status != "up" {
		t.Fatalf("batchHistory() history = %#v, want one up point", history[monitorA])
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestService_BatchHourlyUptime_UsesHourlyRollupsAndBoundedRawScan(t *testing.T) {
	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New() error = %v", err)
	}
	defer sqlDB.Close()

	tenantID := uuid.New()
	monitorA := uuid.New()
	bucket := time.Date(2026, 6, 2, 9, 0, 0, 0, time.UTC)

	mock.ExpectQuery("(?s)monitor_hourly_rollups.*raw_bounds").
		WithArgs(tenantID, sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{
			"monitor_id", "bucket_hour", "total_checks", "success_checks",
		}).AddRow(monitorA, bucket, int64(12), int64(10)))

	svc := NewService(&shareddb.Client{DB: sqlDB}, nil)
	hourly, err := svc.batchHourlyUptime(context.Background(), []uuid.UUID{monitorA}, tenantID)
	if err != nil {
		t.Fatalf("batchHourlyUptime() error = %v", err)
	}
	found := false
	for _, point := range hourly[monitorA] {
		if point.Hour != bucket.Format("15:04") {
			continue
		}
		found = true
		if math.Abs(point.Uptime-((10.0/12.0)*100.0)) > 0.000001 {
			t.Fatalf("batchHourlyUptime() bucket uptime = %f, want %f", point.Uptime, (10.0/12.0)*100.0)
		}
	}
	if !found {
		t.Fatalf("batchHourlyUptime() missing bucket %s in %#v", bucket.Format("15:04"), hourly[monitorA])
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestService_BatchUptimeSummary_UsesHourlyRollupsAndBoundedRawScan(t *testing.T) {
	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New() error = %v", err)
	}
	defer sqlDB.Close()

	tenantID := uuid.New()
	monitorA := uuid.New()

	mock.ExpectQuery("(?s)monitor_hourly_rollups.*raw_bounds").
		WithArgs(tenantID, sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{
			"monitor_id", "total_24h", "success_24h", "total_1h", "success_1h", "avg_latency_1h", "avg_latency_24h",
		}).AddRow(monitorA, int64(12), int64(10), int64(3), int64(3), 90.0, 100.0))

	svc := NewService(&shareddb.Client{DB: sqlDB}, nil)
	summary, err := svc.batchUptimeSummary(context.Background(), []uuid.UUID{monitorA}, tenantID)
	if err != nil {
		t.Fatalf("batchUptimeSummary() error = %v", err)
	}
	if summary[monitorA] == nil || summary[monitorA].Uptime24h == nil || math.Abs(*summary[monitorA].Uptime24h-((10.0/12.0)*100.0)) > 0.000001 {
		t.Fatalf("batchUptimeSummary() = %#v, want rollup-backed 24h uptime", summary[monitorA])
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}
