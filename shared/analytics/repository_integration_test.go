package analytics

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/google/uuid"
	testcontainers "github.com/testcontainers/testcontainers-go"

	"github.com/yassinebenameur/probara/shared/testutil"
)

func TestRepository_GetScopeAnalytics_RawSourceExcludesPlatformAndComputesPercentiles(t *testing.T) {
	testcontainers.SkipIfProviderIsNotHealthy(t)

	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	repo := NewRepository(dbClient)
	tenantID := testutil.InsertTenant(ctx, t, dbClient, "analytics-raw")
	monitorID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "raw-monitor")
	now := time.Date(2026, time.March, 6, 12, 0, 0, 0, time.UTC)

	testutil.InsertCheckResult(ctx, t, dbClient, tenantID, monitorID, now.Add(-50*time.Minute), "success", "monitor", testutil.IntPtr(100))
	testutil.InsertCheckResult(ctx, t, dbClient, tenantID, monitorID, now.Add(-40*time.Minute), "failure", "platform", nil)
	testutil.InsertCheckResult(ctx, t, dbClient, tenantID, monitorID, now.Add(-30*time.Minute), "failure", "monitor", nil)
	testutil.InsertCheckResult(ctx, t, dbClient, tenantID, monitorID, now.Add(-10*time.Minute), "success", "monitor", testutil.IntPtr(200))

	result, err := repo.GetScopeAnalytics(ctx, tenantID, []uuid.UUID{monitorID}, Range24h, now)
	if err != nil {
		t.Fatalf("GetScopeAnalytics() error = %v", err)
	}

	if result.Source != SourceRaw {
		t.Fatalf("Source = %s, want %s", result.Source, SourceRaw)
	}
	assertClose(t, result.Summary.SLAPct, 66.6666667)
	assertClose(t, result.Summary.UptimePct, 66.6666667)
	if result.Summary.AvgLatencyMS == nil {
		t.Fatalf("AvgLatencyMS = nil, want value")
	}
	assertClose(t, *result.Summary.AvgLatencyMS, 150)
	if result.Summary.MedianLatencyMS == nil {
		t.Fatalf("MedianLatencyMS = nil, want value")
	}
	assertClose(t, *result.Summary.MedianLatencyMS, 150)
	if result.Summary.P95LatencyMS == nil {
		t.Fatalf("P95LatencyMS = nil, want value")
	}
	assertClose(t, *result.Summary.P95LatencyMS, 195)
	if result.CoverageStart == nil || !result.CoverageStart.Equal(now.Add(-50*time.Minute)) {
		t.Fatalf("CoverageStart = %v, want %v", result.CoverageStart, now.Add(-50*time.Minute))
	}
	if !result.IsPartial {
		t.Fatalf("IsPartial = false, want true")
	}
	if len(result.Downtime) != 1 {
		t.Fatalf("Downtime periods = %d, want 1", len(result.Downtime))
	}
	if !result.Downtime[0].Start.Equal(now.Add(-30*time.Minute)) || !result.Downtime[0].End.Equal(now.Add(-10*time.Minute)) {
		t.Fatalf("Downtime period = %#v, want [%v, %v]", result.Downtime[0], now.Add(-30*time.Minute), now.Add(-10*time.Minute))
	}
}

func TestRepository_GetScopeAnalytics_RollupSourceUsesSharedAggregationAndPersistedDowntime(t *testing.T) {
	testcontainers.SkipIfProviderIsNotHealthy(t)

	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	repo := NewRepository(dbClient)
	tenantID := testutil.InsertTenant(ctx, t, dbClient, "analytics-rollup")
	monitorA := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "monitor-a")
	monitorB := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "monitor-b")
	now := time.Date(2026, time.March, 6, 12, 0, 0, 0, time.UTC)
	day1 := time.Date(2026, time.March, 5, 0, 0, 0, 0, time.UTC)
	day2 := time.Date(2026, time.March, 6, 0, 0, 0, 0, time.UTC)

	testutil.InsertDailyRollup(ctx, t, dbClient, tenantID, monitorA, day1, 10, 8, 1000, 10, "failure", day1.Add(20*time.Hour))
	testutil.InsertDailyRollup(ctx, t, dbClient, tenantID, monitorA, day2, 10, 10, 2000, 10, "success", day2.Add(11*time.Hour))
	testutil.InsertDailyRollup(ctx, t, dbClient, tenantID, monitorB, day1, 10, 5, 3000, 10, "failure", day1.Add(18*time.Hour))
	// Monitor B intentionally has no day2 data so the SLA average excludes that day.

	testutil.InsertDowntimePeriod(ctx, t, dbClient, tenantID, monitorA, day1.Add(8*time.Hour), day1.Add(9*time.Hour))
	testutil.InsertOpenDowntime(ctx, t, dbClient, tenantID, monitorB, day2.Add(10*time.Hour), day2.Add(11*time.Hour))

	result, err := repo.GetScopeAnalytics(ctx, tenantID, []uuid.UUID{monitorA, monitorB}, Range30d, now)
	if err != nil {
		t.Fatalf("GetScopeAnalytics() error = %v", err)
	}

	if result.Source != SourceRollup {
		t.Fatalf("Source = %s, want %s", result.Source, SourceRollup)
	}
	if result.Summary.P95LatencyMS != nil {
		t.Fatalf("P95LatencyMS = %v, want nil for rollup-backed analytics", result.Summary.P95LatencyMS)
	}
	if result.Summary.MedianLatencyMS != nil {
		t.Fatalf("MedianLatencyMS = %v, want nil for rollup-backed analytics", result.Summary.MedianLatencyMS)
	}
	assertClose(t, result.Summary.SLAPct, 70)
	assertClose(t, result.Summary.UptimePct, 70)
	assertClose(t, result.Summary.DowntimePct, 30)
	if result.Summary.AvgLatencyMS == nil {
		t.Fatalf("AvgLatencyMS = nil, want value")
	}
	assertClose(t, *result.Summary.AvgLatencyMS, 225)
	if result.Summary.LatestStatus == nil || *result.Summary.LatestStatus != "success" {
		t.Fatalf("LatestStatus = %v, want success", result.Summary.LatestStatus)
	}
	if result.Summary.LatestCheckAt == nil || !result.Summary.LatestCheckAt.Equal(day2.Add(11*time.Hour)) {
		t.Fatalf("LatestCheckAt = %v, want %v", result.Summary.LatestCheckAt, day2.Add(11*time.Hour))
	}

	pointDay1 := findSeriesPoint(t, result.Series, day1)
	if !pointDay1.HasData {
		t.Fatalf("day1 HasData = false, want true")
	}
	assertClose(t, pointDay1.UptimePct, 65)
	if pointDay1.AvgLatencyMS == nil {
		t.Fatalf("day1 AvgLatencyMS = nil, want value")
	}
	assertClose(t, *pointDay1.AvgLatencyMS, 200)

	pointDay2 := findSeriesPoint(t, result.Series, day2)
	if !pointDay2.HasData {
		t.Fatalf("day2 HasData = false, want true")
	}
	assertClose(t, pointDay2.UptimePct, 100)
	if pointDay2.AvgLatencyMS == nil {
		t.Fatalf("day2 AvgLatencyMS = nil, want value")
	}
	assertClose(t, *pointDay2.AvgLatencyMS, 200)

	if len(result.Downtime) != 2 {
		t.Fatalf("Downtime periods = %d, want 2", len(result.Downtime))
	}
	if !result.Downtime[0].Start.Equal(day1.Add(8*time.Hour)) || !result.Downtime[0].End.Equal(day1.Add(9*time.Hour)) || result.Downtime[0].IsOpen {
		t.Fatalf("Closed downtime period = %#v, want closed [%v, %v]", result.Downtime[0], day1.Add(8*time.Hour), day1.Add(9*time.Hour))
	}
	if !result.Downtime[1].Start.Equal(day2.Add(10*time.Hour)) || !result.Downtime[1].End.Equal(now) || !result.Downtime[1].IsOpen {
		t.Fatalf("Open downtime period = %#v, want open [%v, %v]", result.Downtime[1], day2.Add(10*time.Hour), now)
	}
}

func findSeriesPoint(t *testing.T, series []SeriesPoint, bucketStart time.Time) SeriesPoint {
	t.Helper()
	for _, point := range series {
		if point.BucketStart.Equal(bucketStart) {
			return point
		}
	}
	t.Fatalf("series point for %v not found", bucketStart)
	return SeriesPoint{}
}

func assertClose(t *testing.T, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 0.0001 {
		t.Fatalf("value = %.6f, want %.6f", got, want)
	}
}
