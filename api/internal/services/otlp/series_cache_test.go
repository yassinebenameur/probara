package otlp

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/shared/metricstore"
)

// cpuPoints is two samples of one series: the second must resolve from the
// transaction's own map, not the registry and not the shared cache.
func cpuPoints(ts time.Time) []seriesPoint {
	attrs := map[string]string{"state": "used"}
	return []seriesPoint{
		{MetricName: "system.cpu.utilization", Attributes: attrs, Unit: "1", MetricType: "gauge", Temporality: "unspecified", TS: ts, Value: 0.4},
		{MetricName: "system.cpu.utilization", Attributes: attrs, Unit: "1", MetricType: "gauge", Temporality: "unspecified", TS: ts.Add(time.Second), Value: 0.5},
	}
}

// expectWriteUpToCommit queues one writeBatchOnce of cpuPoints in strict
// order. Exactly one registry upsert is expected: sqlmock fails the test on a
// second one, which is what a repeated series in one batch must not cause.
func expectWriteUpToCommit(mock sqlmock.Sqlmock, seriesID int64) {
	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT COUNT\(\*\) FROM metric_series`).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	mock.ExpectQuery(`INSERT INTO metric_series`).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(seriesID))
	mock.ExpectExec(`INSERT INTO metric_samples`).WillReturnResult(sqlmock.NewResult(0, 2))
	mock.ExpectExec(`UPDATE metric_series SET last_seen_at`).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`INSERT INTO metric_rollup_dirty`).WillReturnResult(sqlmock.NewResult(0, 1))
}

func TestWriteBatchOnceCachesSeriesOnlyAfterCommit(t *testing.T) {
	ctx := context.Background()
	monitorID, tenantID := uuid.New(), uuid.New()
	points := cpuPoints(time.Now().Add(-time.Minute))
	hash := metricstore.AttrHash(points[0].MetricName, points[0].Attributes)

	t.Run("commit failure leaves the cache empty", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		svc := NewService(db, nil, 2000, 60)

		expectWriteUpToCommit(mock, 41)
		mock.ExpectCommit().WillReturnError(errors.New("connection reset"))

		if _, _, _, _, err := svc.writeBatchOnce(ctx, tenantID, monitorID, points); err == nil {
			t.Fatal("writeBatchOnce succeeded despite a failed commit")
		}
		if id, ok := svc.cache.get(monitorID, hash); ok {
			t.Fatalf("rolled-back series id %d was cached", id)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("failure before commit leaves the cache empty", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		svc := NewService(db, nil, 2000, 60)

		mock.ExpectBegin()
		mock.ExpectQuery(`SELECT COUNT\(\*\) FROM metric_series`).
			WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
		mock.ExpectQuery(`INSERT INTO metric_series`).
			WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(42))
		mock.ExpectExec(`INSERT INTO metric_samples`).
			WillReturnError(errors.New(`no partition of relation "metric_samples" found for row`))
		mock.ExpectRollback()

		_, _, _, _, err = svc.writeBatchOnce(ctx, tenantID, monitorID, points)
		if !metricstore.IsMissingPartitionErr(err) {
			t.Fatalf("err = %v, want the missing-partition error", err)
		}
		if id, ok := svc.cache.get(monitorID, hash); ok {
			t.Fatalf("rolled-back series id %d was cached", id)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("successful commit caches the series once", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		svc := NewService(db, nil, 2000, 60)

		expectWriteUpToCommit(mock, 43)
		mock.ExpectCommit()

		accepted, _, _, _, err := svc.writeBatchOnce(ctx, tenantID, monitorID, points)
		if err != nil {
			t.Fatalf("writeBatchOnce: %v", err)
		}
		if accepted != 2 {
			t.Fatalf("accepted = %d, want 2", accepted)
		}
		if id, ok := svc.cache.get(monitorID, hash); !ok || id != 43 {
			t.Fatalf("cache = (%d, %v), want (43, true)", id, ok)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	})
}
