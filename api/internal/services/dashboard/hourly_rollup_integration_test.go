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
