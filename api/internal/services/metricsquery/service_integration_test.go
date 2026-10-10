package metricsquery

// Integration tests for the UI-facing metric query service: discovery,
// bucketed aggregation, server-side counter rates (reset-aware), the rollup
// path for long ranges, and validation/tenant-scoping errors.

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	testcontainers "github.com/testcontainers/testcontainers-go"

	shareddb "github.com/yassinebenameur/probara/shared/db"
	"github.com/yassinebenameur/probara/shared/metricstore"
	"github.com/yassinebenameur/probara/shared/testutil"
)

func seedSeries(ctx context.Context, t *testing.T, db *shareddb.Client, tenantID, monitorID uuid.UUID, name, unit, metricType string, monotonic bool, attrs map[string]string) int64 {
	t.Helper()
	temporality := "unspecified"
	if metricType == "sum" {
		temporality = "cumulative"
	}
	id, err := metricstore.ResolveSeries(ctx, db.DB, metricstore.SeriesKey{
		TenantID: tenantID, MonitorID: monitorID, MetricName: name, Unit: unit,
		MetricType: metricType, IsMonotonic: monotonic, Temporality: temporality, Attributes: attrs,
	})
	if err != nil {
		t.Fatalf("resolve series %s: %v", name, err)
	}
	return id
}

func seedSamples(ctx context.Context, t *testing.T, db *shareddb.Client, seriesID int64, base time.Time, step time.Duration, values ...float64) {
	t.Helper()
	rows := make([]metricstore.Sample, 0, len(values))
	for i, v := range values {
		rows = append(rows, metricstore.Sample{SeriesID: seriesID, TS: base.Add(time.Duration(i) * step), Value: v})
	}
	if _, err := metricstore.InsertSamples(ctx, db.DB, rows); err != nil {
		t.Fatalf("insert samples: %v", err)
	}
}

func TestQueryServiceDiscoveryAndRangeQuery(t *testing.T) {
	testcontainers.SkipIfProviderIsNotHealthy(t)

	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "tenant-mq")
	monitorID := testutil.InsertAgentMonitor(ctx, t, dbClient, tenantID, "mq-host", uuid.New().String(), 60)

	base := time.Now().UTC().Add(-30 * time.Minute).Truncate(time.Minute)
	if err := metricstore.EnsurePartitions(ctx, dbClient.DB, base.Add(-time.Hour), base.Add(time.Hour)); err != nil {
		t.Fatalf("ensure partitions: %v", err)
	}

	cpuID := seedSeries(ctx, t, dbClient, tenantID, monitorID, "system.cpu.utilization", "1", "gauge", false, map[string]string{"state": "used"})
	fsRoot := seedSeries(ctx, t, dbClient, tenantID, monitorID, "system.filesystem.utilization", "1", "gauge", false, map[string]string{"mountpoint": "/", "state": "used"})
	fsData := seedSeries(ctx, t, dbClient, tenantID, monitorID, "system.filesystem.utilization", "1", "gauge", false, map[string]string{"mountpoint": "/data", "state": "used"})
	netID := seedSeries(ctx, t, dbClient, tenantID, monitorID, "system.network.io", "By", "sum", true, map[string]string{"device": "eth0", "direction": "receive"})

	// One sample per minute for 10 minutes.
	seedSamples(ctx, t, dbClient, cpuID, base, time.Minute, 0.2, 0.4, 0.6, 0.4, 0.2, 0.4, 0.6, 0.4, 0.2, 0.4)
	seedSamples(ctx, t, dbClient, fsRoot, base, time.Minute, 0.5, 0.5, 0.5, 0.5, 0.5, 0.5, 0.5, 0.5, 0.5, 0.5)
	seedSamples(ctx, t, dbClient, fsData, base, time.Minute, 0.9, 0.9, 0.9, 0.9, 0.9, 0.9, 0.9, 0.9, 0.9, 0.9)
	// Counter: +60 bytes/min, one reset (300 → 60 contributes 60).
	seedSamples(ctx, t, dbClient, netID, base, time.Minute, 60, 120, 180, 240, 300, 60, 120, 180, 240, 300)

	svc := NewService(dbClient.DB)

	// Discovery lists all four series with the simplified type.
	items, err := svc.ListSeries(ctx, monitorID, tenantID)
	if err != nil {
		t.Fatalf("ListSeries: %v", err)
	}
	if len(items) != 4 {
		t.Fatalf("series items = %d, want 4", len(items))
	}
	byKey := map[string]SeriesItem{}
	for _, it := range items {
		byKey[it.SeriesKey] = it
	}
	if it, ok := byKey["system.network.io{device=eth0,direction=receive}"]; !ok || it.MetricType != "counter" {
		t.Fatalf("network series missing or wrong type: %+v", it)
	}

	// Batch query: cpu avg over 5-minute buckets, per-mount filesystem
	// fan-out, network rate.
	resp, err := svc.Query(ctx, monitorID, tenantID, QueryRequest{
		Start: base, End: base.Add(10 * time.Minute), StepSeconds: 300,
		Queries: []QuerySpec{
			{Ref: "cpu", MetricName: "system.cpu.utilization", AttributeFilters: map[string]string{"state": "used"}, Agg: "avg"},
			{Ref: "fs", MetricName: "system.filesystem.utilization"},
			{Ref: "net", MetricName: "system.network.io", Rate: true},
		},
	})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	results := map[string]QueryRefResult{}
	for _, r := range resp.Results {
		results[r.Ref] = r
	}

	cpu := results["cpu"]
	if len(cpu.Series) != 1 || cpu.Source != "raw" {
		t.Fatalf("cpu result = %+v", cpu)
	}
	if len(cpu.Series[0].Points) == 0 {
		t.Fatal("cpu series has no points")
	}
	for _, p := range cpu.Series[0].Points {
		if p[1] < 0.1 || p[1] > 0.7 {
			t.Fatalf("cpu avg point out of range: %v", p)
		}
	}

	if len(results["fs"].Series) != 2 {
		t.Fatalf("filesystem fan-out = %d series, want 2", len(results["fs"].Series))
	}

	net := results["net"]
	if len(net.Series) != 1 {
		t.Fatalf("net result = %+v", net)
	}
	// +60 bytes per 60s = 1 B/s sustained; the reset bucket also contributes
	// its post-reset value. All rates must be positive and bounded.
	for _, p := range net.Series[0].Points {
		if p[1] <= 0 || p[1] > 2 {
			t.Fatalf("net rate point out of range: %v", p)
		}
	}

	// Rate over a gauge is a validation error, not a 500.
	_, err = svc.Query(ctx, monitorID, tenantID, QueryRequest{
		Start: base, End: base.Add(10 * time.Minute), StepSeconds: 60,
		Queries: []QuerySpec{{Ref: "bad", MetricName: "system.cpu.utilization", Rate: true}},
	})
	if !IsValidationError(err) {
		t.Fatalf("rate-over-gauge err = %v, want validation error", err)
	}

	// Cross-tenant access 404s.
	otherTenant := testutil.InsertTenant(ctx, t, dbClient, "tenant-mq-other")
	if _, err := svc.ListSeries(ctx, monitorID, otherTenant); !errors.Is(err, ErrMonitorNotFound) {
		t.Fatalf("cross-tenant ListSeries err = %v, want ErrMonitorNotFound", err)
	}
}

func TestQueryServiceRollupPath(t *testing.T) {
	testcontainers.SkipIfProviderIsNotHealthy(t)

	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "tenant-mq-rollup")
	monitorID := testutil.InsertAgentMonitor(ctx, t, dbClient, tenantID, "mq-rollup-host", uuid.New().String(), 60)
	cpuID := seedSeries(ctx, t, dbClient, tenantID, monitorID, "system.cpu.utilization", "1", "gauge", false, map[string]string{"state": "used"})

	// Seed hourly rollups directly (raw samples for a week-old range are
	// past raw retention in real deployments).
	start := time.Now().UTC().Add(-72 * time.Hour).Truncate(time.Hour)
	for i := 0; i < 72; i++ {
		bucket := start.Add(time.Duration(i) * time.Hour)
		if _, err := dbClient.ExecContext(ctx, `
			INSERT INTO metric_rollups_hourly (series_id, bucket, sample_count, min_value, max_value, sum_value, first_value, last_value, increase)
			VALUES ($1, $2, 60, 0.1, 0.9, 30, 0.5, 0.5, NULL)
		`, cpuID, bucket); err != nil {
			t.Fatalf("seed rollup: %v", err)
		}
	}

	svc := NewService(dbClient.DB)
	resp, err := svc.Query(ctx, monitorID, tenantID, QueryRequest{
		Start: start, End: start.Add(72 * time.Hour), StepSeconds: 3600,
		Queries: []QuerySpec{{Ref: "cpu", MetricName: "system.cpu.utilization", Agg: "avg"}},
	})
	if err != nil {
		t.Fatalf("rollup Query: %v", err)
	}
	got := resp.Results[0]
	if got.Source != "rollup" {
		t.Fatalf("source = %s, want rollup", got.Source)
	}
	if len(got.Series) != 1 || len(got.Series[0].Points) != 72 {
		t.Fatalf("rollup points = %d, want 72", len(got.Series[0].Points))
	}
	if v := got.Series[0].Points[0][1]; v != 0.5 {
		t.Fatalf("rollup avg = %v, want 0.5 (sum/count)", v)
	}
}
