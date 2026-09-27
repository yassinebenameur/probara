package scheduler

// Integration tests for the metric-store maintenance passes
// (metric_rollups.go): the dirty-ledger consumer's wholesale monitor-hour
// rebuild (reset-aware counter increase, NULL increase for gauges) and daily
// partition create/drop.

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	testcontainers "github.com/testcontainers/testcontainers-go"

	"github.com/yassinebenameur/probara/shared/config"
	shareddb "github.com/yassinebenameur/probara/shared/db"
	"github.com/yassinebenameur/probara/shared/logger"
	"github.com/yassinebenameur/probara/shared/metrics"
	"github.com/yassinebenameur/probara/shared/metricstore"
	"github.com/yassinebenameur/probara/shared/testutil"
)

func insertMetricSample(ctx context.Context, t *testing.T, db *shareddb.Client, seriesID int64, ts time.Time, value float64) {
	t.Helper()
	if _, err := metricstore.InsertSamples(ctx, db.DB, []metricstore.Sample{{SeriesID: seriesID, TS: ts, Value: value}}); err != nil {
		t.Fatalf("insert metric sample: %v", err)
	}
}

func TestConsumeMetricDirtyBucketsRebuildsHour(t *testing.T) {
	testcontainers.SkipIfProviderIsNotHealthy(t)

	ctx := context.Background()
	dbClient, cleanup := setupRollupTestDB(ctx, t)
	defer cleanup()

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "tenant-metric-rollup")
	monitorID := testutil.InsertAgentMonitor(ctx, t, dbClient, tenantID, "metric-host", uuid.New().String(), 60)

	bucket := time.Now().UTC().Add(-2 * time.Hour).Truncate(time.Hour)
	if err := metricstore.EnsurePartitions(ctx, dbClient.DB, bucket.Add(-time.Hour), bucket.Add(time.Hour)); err != nil {
		t.Fatalf("ensure partitions: %v", err)
	}

	gaugeID, err := metricstore.ResolveSeries(ctx, dbClient.DB, metricstore.SeriesKey{
		TenantID: tenantID, MonitorID: monitorID,
		MetricName: "system.cpu.utilization", Unit: "1", MetricType: "gauge",
		Temporality: "unspecified", Attributes: map[string]string{"state": "used"},
	})
	if err != nil {
		t.Fatalf("resolve gauge series: %v", err)
	}
	counterID, err := metricstore.ResolveSeries(ctx, dbClient.DB, metricstore.SeriesKey{
		TenantID: tenantID, MonitorID: monitorID,
		MetricName: "system.network.io", Unit: "By", MetricType: "sum",
		IsMonotonic: true, Temporality: "cumulative",
		Attributes: map[string]string{"device": "eth0", "direction": "receive"},
	})
	if err != nil {
		t.Fatalf("resolve counter series: %v", err)
	}

	// Gauge: three in-bucket samples.
	insertMetricSample(ctx, t, dbClient, gaugeID, bucket.Add(5*time.Minute), 0.10)
	insertMetricSample(ctx, t, dbClient, gaugeID, bucket.Add(25*time.Minute), 0.30)
	insertMetricSample(ctx, t, dbClient, gaugeID, bucket.Add(45*time.Minute), 0.20)
	// Counter: predecessor in the previous hour (boundary delta 10), then
	// +50, then a reset (150 → 120 contributes 120). increase = 10+50+120.
	insertMetricSample(ctx, t, dbClient, counterID, bucket.Add(-10*time.Minute), 90)
	insertMetricSample(ctx, t, dbClient, counterID, bucket.Add(10*time.Minute), 100)
	insertMetricSample(ctx, t, dbClient, counterID, bucket.Add(30*time.Minute), 150)
	insertMetricSample(ctx, t, dbClient, counterID, bucket.Add(50*time.Minute), 120)

	if err := metricstore.MarkDirty(ctx, dbClient.DB, monitorID, []time.Time{bucket}); err != nil {
		t.Fatalf("mark dirty: %v", err)
	}

	s := newTestScheduler(dbClient)
	processed, drained, err := s.consumeMetricDirtyBuckets(ctx)
	if err != nil {
		t.Fatalf("consumeMetricDirtyBuckets: %v", err)
	}
	if processed != 1 || !drained {
		t.Fatalf("processed=%d drained=%v, want 1/true", processed, drained)
	}

	var count int
	var minV, maxV, sumV, firstV, lastV float64
	var increase *float64
	if err := dbClient.QueryRowContext(ctx, `
		SELECT sample_count, min_value, max_value, sum_value, first_value, last_value, increase
		FROM metric_rollups_hourly WHERE series_id = $1 AND bucket = $2
	`, gaugeID, bucket).Scan(&count, &minV, &maxV, &sumV, &firstV, &lastV, &increase); err != nil {
		t.Fatalf("query gauge rollup: %v", err)
	}
	if count != 3 || minV != 0.10 || maxV != 0.30 || firstV != 0.10 || lastV != 0.20 {
		t.Fatalf("gauge rollup = count %d min %v max %v first %v last %v", count, minV, maxV, firstV, lastV)
	}
	if increase != nil {
		t.Fatalf("gauge increase = %v, want NULL", *increase)
	}

	if err := dbClient.QueryRowContext(ctx, `
		SELECT sample_count, increase FROM metric_rollups_hourly WHERE series_id = $1 AND bucket = $2
	`, counterID, bucket).Scan(&count, &increase); err != nil {
		t.Fatalf("query counter rollup: %v", err)
	}
	if count != 3 {
		t.Fatalf("counter sample_count = %d, want 3 (lookback sample excluded)", count)
	}
	if increase == nil || *increase != 180 {
		t.Fatalf("counter increase = %v, want 180 (reset-aware)", increase)
	}

	if n := countLedger(ctx, t, dbClient); n != 0 {
		t.Fatalf("ledger rows after drain = %d, want 0", n)
	}

	// Re-mark and rebuild after a late sample lands: wholesale REPLACE picks
	// it up (no double counting).
	insertMetricSample(ctx, t, dbClient, gaugeID, bucket.Add(55*time.Minute), 0.90)
	if err := metricstore.MarkDirty(ctx, dbClient.DB, monitorID, []time.Time{bucket}); err != nil {
		t.Fatalf("re-mark dirty: %v", err)
	}
	if _, _, err := s.consumeMetricDirtyBuckets(ctx); err != nil {
		t.Fatalf("re-consume: %v", err)
	}
	if err := dbClient.QueryRowContext(ctx, `
		SELECT sample_count, max_value, last_value FROM metric_rollups_hourly WHERE series_id = $1 AND bucket = $2
	`, gaugeID, bucket).Scan(&count, &maxV, &lastV); err != nil {
		t.Fatalf("query rebuilt gauge rollup: %v", err)
	}
	if count != 4 || maxV != 0.90 || lastV != 0.90 {
		t.Fatalf("rebuilt gauge rollup = count %d max %v last %v, want 4/0.9/0.9", count, maxV, lastV)
	}
}

func countLedger(ctx context.Context, t *testing.T, db *shareddb.Client) int {
	t.Helper()
	var n int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM metric_rollup_dirty`).Scan(&n); err != nil {
		t.Fatalf("count ledger: %v", err)
	}
	return n
}

func TestMaintainMetricPartitionsCreatesAheadAndDropsExpired(t *testing.T) {
	testcontainers.SkipIfProviderIsNotHealthy(t)

	ctx := context.Background()
	dbClient, cleanup := setupRollupTestDB(ctx, t)
	defer cleanup()

	cfg := &config.SchedulerConfig{
		BaseConfig:             config.BaseConfig{ServiceName: "scheduler_test"},
		MetricRawRetentionDays: 30,
	}
	s := NewScheduler(cfg, logger.New("scheduler_test", "error"), metrics.NewRegistry("scheduler_test_parts"), dbClient, nil)

	// Seed an expired partition (40 days old) with a row.
	oldDay := time.Now().UTC().AddDate(0, 0, -40).Truncate(24 * time.Hour)
	if err := metricstore.EnsurePartitions(ctx, dbClient.DB, oldDay, oldDay); err != nil {
		t.Fatalf("ensure old partition: %v", err)
	}
	oldName := "metric_samples_" + oldDay.Format("20060102")

	if err := s.maintainMetricPartitions(ctx); err != nil {
		t.Fatalf("maintainMetricPartitions: %v", err)
	}

	var exists bool
	if err := dbClient.QueryRowContext(ctx, `SELECT to_regclass($1) IS NOT NULL`, oldName).Scan(&exists); err != nil {
		t.Fatalf("check dropped partition: %v", err)
	}
	if exists {
		t.Fatalf("expired partition %s still exists", oldName)
	}

	aheadName := "metric_samples_" + time.Now().UTC().AddDate(0, 0, metricPartitionAheadDays).Format("20060102")
	if err := dbClient.QueryRowContext(ctx, `SELECT to_regclass($1) IS NOT NULL`, aheadName).Scan(&exists); err != nil {
		t.Fatalf("check ahead partition: %v", err)
	}
	if !exists {
		t.Fatalf("ahead partition %s missing", aheadName)
	}
}
