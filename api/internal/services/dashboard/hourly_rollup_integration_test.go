package dashboard

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/google/uuid"
	testcontainers "github.com/testcontainers/testcontainers-go"

	"github.com/yassinebenameur/probara/shared/testutil"
)

func TestLoadExactRolling24hSummary_SplitsLeadingEdgeRollupAndRawTail(t *testing.T) {
	testcontainers.SkipIfProviderIsNotHealthy(t)

	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "rolling-summary")
	monitorID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "rolling-mon")

	// Anchor "now" at 12:30 UTC so the rolling window starts at yesterday 12:30
	// and the leading hour (12:00-13:00 yesterday) is a partial bucket.
	now := time.Date(2026, time.March, 6, 12, 30, 0, 0, time.UTC)

	// --- Rollup-eligible region: yesterday 13:00 through today 11:00 (23 hourly rows).
	for h := 0; h < 23; h++ {
		bucket := time.Date(2026, time.March, 5, 13, 0, 0, 0, time.UTC).Add(time.Duration(h) * time.Hour)
		// Use a fixed pattern: 10 total / 9 success per hour, 900ms latency sum, 9 latency count.
		testutil.InsertHourlyRollup(ctx, t, dbClient, tenantID, monitorID, bucket, 10, 9, 900, 9, "success", bucket.Add(59*time.Minute))
	}

	// --- Mark 5 of the 23 rollup buckets as carrying their 1 bad check as an
	//     ERROR (error_checks = 1): those must surface in ErrorChecks while the
	//     other 18 rollup-era bad checks stay in FailureChecks.
	if _, err := dbClient.ExecContext(ctx, `
		UPDATE monitor_hourly_rollups SET error_checks = 1
		WHERE tenant_id = $1 AND monitor_id = $2 AND bucket_hour < $3
	`, tenantID, monitorID, time.Date(2026, time.March, 5, 18, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("set rollup error_checks: %v", err)
	}

	// --- Leading-edge raw: yesterday 12:35 (inside window, before leading_edge_end 13:00).
	leadingTime := time.Date(2026, time.March, 5, 12, 35, 0, 0, time.UTC)
	testutil.InsertCheckResult(ctx, t, dbClient, tenantID, monitorID, leadingTime, "failure", "monitor", nil)

	// --- A platform row in the leading edge must be excluded.
	testutil.InsertCheckResult(ctx, t, dbClient, tenantID, monitorID, leadingTime, "failure", "platform", nil)

	// --- A pre-window row (yesterday 12:00) must be excluded.
	preWindow := time.Date(2026, time.March, 5, 12, 0, 0, 0, time.UTC)
	testutil.InsertCheckResult(ctx, t, dbClient, tenantID, monitorID, preWindow, "success", "monitor", testutil.IntPtr(50))

	// --- Rollup cursor is set so rollup_end = today 12:00 (the 23 hourly buckets 13:00..11:00
	//     all fall in [leading_edge_end=13:00, rollup_end=12:00)). The cursor check-result row is
	//     placed after now so it is excluded from the raw window (created_at >= w_end).
	cursorTime := time.Date(2026, time.March, 6, 12, 35, 0, 0, time.UTC)
	cursorID := testutil.InsertCheckResult(ctx, t, dbClient, tenantID, monitorID, cursorTime, "success", "monitor", testutil.IntPtr(100))
	testutil.InsertRollupJobState(ctx, t, dbClient, "monitor_daily_rollups", cursorTime, cursorID)

	// --- Trailing-edge raw: two rows in today 12:00-12:30 (past cursor, before now).
	trail1 := time.Date(2026, time.March, 6, 12, 5, 0, 0, time.UTC)
	trail2 := time.Date(2026, time.March, 6, 12, 20, 0, 0, time.UTC)
	testutil.InsertCheckResult(ctx, t, dbClient, tenantID, monitorID, trail1, "success", "monitor", testutil.IntPtr(120))
	testutil.InsertCheckResult(ctx, t, dbClient, tenantID, monitorID, trail2, "failure", "monitor", nil)

	// --- A post-now row must be excluded.
	postNow := time.Date(2026, time.March, 6, 12, 45, 0, 0, time.UTC)
	testutil.InsertCheckResult(ctx, t, dbClient, tenantID, monitorID, postNow, "success", "monitor", testutil.IntPtr(200))

	totals, err := loadExactRolling24hSummary(ctx, dbClient, tenantID, []uuid.UUID{monitorID}, now)
	if err != nil {
		t.Fatalf("loadExactRolling24hSummary() error = %v", err)
	}
	got, ok := totals[monitorID]
	if !ok {
		t.Fatalf("monitor totals missing")
	}

	// Expected aggregation:
	//   rollup region: 23 * 10 = 230 total, 23 * 9 = 207 success, 23 * 900 = 20700 latency_sum, 23 * 9 = 207 latency_count
	//   leading raw:   1 total, 0 success, 0 latency
	//   trailing raw:  2 total, 1 success, 120 latency_sum, 1 latency_count
	// Total: 233 total, 208 success, 20820 latency_sum, 208 latency_count
	if got.TotalChecks != 233 {
		t.Fatalf("TotalChecks = %d, want 233", got.TotalChecks)
	}
	if got.SuccessChecks != 208 {
		t.Fatalf("SuccessChecks = %d, want 208", got.SuccessChecks)
	}
	// Bad checks: 23 rollup-era (10-9 per bucket) of which 5 are errors
	// (error_checks=1 on 5 buckets), plus 1 leading raw failure and 1 trailing
	// raw failure. ErrorChecks must surface the 5 rollup errors; FailureChecks
	// must exclude them: 18 rollup + 2 raw = 20.
	if got.FailureChecks != 20 {
		t.Fatalf("FailureChecks = %d, want 20 (rollup errors excluded)", got.FailureChecks)
	}
	if got.ErrorChecks != 5 {
		t.Fatalf("ErrorChecks = %d, want 5 (from rollup error_checks)", got.ErrorChecks)
	}
	if math.Abs(got.LatencySumMS-20820) > 0.01 {
		t.Fatalf("LatencySumMS = %f, want 20820", got.LatencySumMS)
	}
	if got.LatencyCount != 208 {
		t.Fatalf("LatencyCount = %d, want 208", got.LatencyCount)
	}
	// LatestCheckAt should be trail2 (most recent monitor row in window).
	if got.LatestCheckAt == nil || !got.LatestCheckAt.Equal(trail2) {
		t.Fatalf("LatestCheckAt = %v, want %v", got.LatestCheckAt, trail2)
	}
	if got.LatestStatus == nil || *got.LatestStatus != "failure" {
		t.Fatalf("LatestStatus = %v, want failure", got.LatestStatus)
	}
}

func TestLoadExactRolling24hSummary_NullCursorServesEntireWindowFromRaw(t *testing.T) {
	testcontainers.SkipIfProviderIsNotHealthy(t)

	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "rolling-null-cursor")
	monitorID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "null-cursor-mon")

	now := time.Date(2026, time.March, 6, 12, 30, 0, 0, time.UTC)

	// No rollup_job_state row exists: rollup_end collapses to leading_edge_end,
	// so the rollup region is empty and the two raw ranges meet at
	// leading_edge_end (yesterday 13:00) to cover the whole window.
	// An hourly rollup inside the would-be rollup region must be IGNORED.
	rollupBucket := time.Date(2026, time.March, 6, 3, 0, 0, 0, time.UTC)
	testutil.InsertHourlyRollup(ctx, t, dbClient, tenantID, monitorID, rollupBucket, 10, 9, 900, 9, "success", rollupBucket.Add(59*time.Minute))

	// Raw rows spread across the window: leading partial hour, middle, trailing partial hour.
	leading := time.Date(2026, time.March, 5, 12, 45, 0, 0, time.UTC)
	middle := time.Date(2026, time.March, 6, 3, 15, 0, 0, time.UTC)
	trailing := time.Date(2026, time.March, 6, 12, 10, 0, 0, time.UTC)
	testutil.InsertCheckResult(ctx, t, dbClient, tenantID, monitorID, leading, "failure", "monitor", nil)
	testutil.InsertCheckResult(ctx, t, dbClient, tenantID, monitorID, middle, "success", "monitor", testutil.IntPtr(80))
	testutil.InsertCheckResult(ctx, t, dbClient, tenantID, monitorID, trailing, "success", "monitor", testutil.IntPtr(120))

	// Out-of-window rows must be excluded.
	testutil.InsertCheckResult(ctx, t, dbClient, tenantID, monitorID, now.Add(-25*time.Hour), "success", "monitor", testutil.IntPtr(50))
	testutil.InsertCheckResult(ctx, t, dbClient, tenantID, monitorID, now.Add(15*time.Minute), "success", "monitor", testutil.IntPtr(60))

	totals, err := loadExactRolling24hSummary(ctx, dbClient, tenantID, []uuid.UUID{monitorID}, now)
	if err != nil {
		t.Fatalf("loadExactRolling24hSummary() error = %v", err)
	}
	got, ok := totals[monitorID]
	if !ok {
		t.Fatalf("monitor totals missing")
	}

	// Raw-only: 3 total, 2 success, 1 failure, latency 80+120 over 2 samples.
	if got.TotalChecks != 3 {
		t.Fatalf("TotalChecks = %d, want 3", got.TotalChecks)
	}
	if got.SuccessChecks != 2 {
		t.Fatalf("SuccessChecks = %d, want 2", got.SuccessChecks)
	}
	if got.FailureChecks != 1 {
		t.Fatalf("FailureChecks = %d, want 1", got.FailureChecks)
	}
	if math.Abs(got.LatencySumMS-200) > 0.01 {
		t.Fatalf("LatencySumMS = %f, want 200", got.LatencySumMS)
	}
	if got.LatencyCount != 2 {
		t.Fatalf("LatencyCount = %d, want 2", got.LatencyCount)
	}
	if got.LatestCheckAt == nil || !got.LatestCheckAt.Equal(trailing) {
		t.Fatalf("LatestCheckAt = %v, want %v", got.LatestCheckAt, trailing)
	}
}

func TestLoadHourlyBucketSeries24h_RollupPlusCurrentHourRawSplice(t *testing.T) {
	testcontainers.SkipIfProviderIsNotHealthy(t)

	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "bucket-series")
	monitorID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "bucket-mon")

	now := time.Date(2026, time.March, 6, 12, 30, 0, 0, time.UTC)

	// Past hour rollups: today 9:00 (5/10) and today 11:00 (10/10).
	hour9 := time.Date(2026, time.March, 6, 9, 0, 0, 0, time.UTC)
	hour11 := time.Date(2026, time.March, 6, 11, 0, 0, 0, time.UTC)
	testutil.InsertHourlyRollup(ctx, t, dbClient, tenantID, monitorID, hour9, 10, 5, 500, 5, "failure", hour9.Add(59*time.Minute))
	testutil.InsertHourlyRollup(ctx, t, dbClient, tenantID, monitorID, hour11, 10, 10, 1000, 10, "success", hour11.Add(59*time.Minute))

	// Cursor at 11:59:30 so today 12 is unrolled. Insert the cursor row itself
	// as a real check_result, then point rollup_job_state at it. The strict
	// tuple comparison (cr.created_at, cr.id) > (cursor_ts, cursor_id) must
	// exclude THIS row while still including the later lagTime row.
	cursorTime := time.Date(2026, time.March, 6, 11, 59, 30, 0, time.UTC)
	cursorID := testutil.InsertCheckResult(ctx, t, dbClient, tenantID, monitorID, cursorTime, "success", "monitor", testutil.IntPtr(200))
	testutil.InsertRollupJobState(ctx, t, dbClient, "monitor_daily_rollups", cursorTime, cursorID)

	// Raw rows in today 12:xx (current incomplete hour, past cursor).
	for _, m := range []int{5, 10, 15, 20, 25} {
		ts := time.Date(2026, time.March, 6, 12, m, 0, 0, time.UTC)
		testutil.InsertCheckResult(ctx, t, dbClient, tenantID, monitorID, ts, "success", "monitor", testutil.IntPtr(100))
	}

	// A raw row in an OLDER hour past the cursor should also be picked up by the
	// splice (cursor lag tolerance).
	lagTime := time.Date(2026, time.March, 6, 11, 59, 45, 0, time.UTC) // past cursor, in hour 11
	testutil.InsertCheckResult(ctx, t, dbClient, tenantID, monitorID, lagTime, "failure", "monitor", nil)

	series, err := loadHourlyBucketSeries24h(ctx, dbClient, tenantID, []uuid.UUID{monitorID}, now)
	if err != nil {
		t.Fatalf("loadHourlyBucketSeries24h() error = %v", err)
	}
	if len(series) != 24 {
		t.Fatalf("len(series) = %d, want 24", len(series))
	}
	// Buckets are hour-aligned: [now.Truncate(1h) - 23h, ..., now.Truncate(1h)].
	wantFirstBucket := time.Date(2026, time.March, 5, 13, 0, 0, 0, time.UTC)
	if !series[0].BucketStart.Equal(wantFirstBucket) {
		t.Fatalf("series[0] = %v, want %v", series[0].BucketStart, wantFirstBucket)
	}
	wantLastBucket := time.Date(2026, time.March, 6, 12, 0, 0, 0, time.UTC)
	if !series[23].BucketStart.Equal(wantLastBucket) {
		t.Fatalf("series[23] = %v, want %v", series[23].BucketStart, wantLastBucket)
	}

	// Bucket for hour 9: 10 total / 5 success (rollup only).
	pHour9 := findBucket(t, series, hour9)
	if pHour9.TotalChecks != 10 || pHour9.SuccessChecks != 5 {
		t.Fatalf("hour9 totals = %d/%d, want 5/10", pHour9.SuccessChecks, pHour9.TotalChecks)
	}

	// Bucket for hour 11: 10 total / 10 success (rollup) + 1 failure (raw past-cursor splice) = 11/10.
	pHour11 := findBucket(t, series, hour11)
	if pHour11.TotalChecks != 11 || pHour11.SuccessChecks != 10 {
		t.Fatalf("hour11 totals = %d/%d (success/total), want 10/11", pHour11.SuccessChecks, pHour11.TotalChecks)
	}

	// Bucket for hour 12 (current incomplete): 0 rollup + 5 raw → 5/5.
	pHour12 := findBucket(t, series, wantLastBucket)
	if pHour12.TotalChecks != 5 || pHour12.SuccessChecks != 5 {
		t.Fatalf("hour12 totals = %d/%d, want 5/5", pHour12.SuccessChecks, pHour12.TotalChecks)
	}

	// Empty bucket (no data anywhere).
	emptyBucket := time.Date(2026, time.March, 6, 8, 0, 0, 0, time.UTC)
	pEmpty := findBucket(t, series, emptyBucket)
	if pEmpty.TotalChecks != 0 {
		t.Fatalf("empty bucket TotalChecks = %d, want 0", pEmpty.TotalChecks)
	}
}

func findBucket(t *testing.T, series []HourlyBucketPoint, bucketStart time.Time) HourlyBucketPoint {
	t.Helper()
	for _, p := range series {
		if p.BucketStart.Equal(bucketStart) {
			return p
		}
	}
	t.Fatalf("bucket %v not found in series", bucketStart)
	return HourlyBucketPoint{}
}
