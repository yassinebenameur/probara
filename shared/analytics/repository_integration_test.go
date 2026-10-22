//go:build !unit

package analytics

import (
	"context"
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
	// Downtime comes from the state timeline, not the failed check rows.
	insertInterval(ctx, t, dbClient, tenantID, monitorID, "up", now.Add(-25*time.Hour), ptrTime(now.Add(-30*time.Minute)))
	insertInterval(ctx, t, dbClient, tenantID, monitorID, "down", now.Add(-30*time.Minute), ptrTime(now.Add(-10*time.Minute)))
	insertInterval(ctx, t, dbClient, tenantID, monitorID, "up", now.Add(-10*time.Minute), nil)

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

// TestRepository_GetScopeAnalytics_RawDowntimeFollowsStateTimeline pins the
// short-range downtime list to the state timeline: failed check rows that
// never moved the monitor to 'down' (a degraded quorum, a suspect blip, a
// paused monitor) list nothing, and the list agrees with the interval
// headline. Before, every failed row opened a period until the next
// success from ANY location, so one failing location of three produced a
// period per check.
func TestRepository_GetScopeAnalytics_RawDowntimeFollowsStateTimeline(t *testing.T) {
	testcontainers.SkipIfProviderIsNotHealthy(t)

	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	repo := NewRepository(dbClient)
	tenantID := testutil.InsertTenant(ctx, t, dbClient, "analytics-raw-timeline")
	degraded := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "degraded")
	flaky := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "flaky")
	down := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "down")
	paused := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "paused")
	maint := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "maintenance")
	now := time.Date(2026, time.March, 6, 12, 0, 0, 0, time.UTC)
	at := func(min int) time.Time { return now.Add(time.Duration(min) * time.Minute) }

	// Degraded: three locations each minute, one of them failing (quorum 2).
	insertInterval(ctx, t, dbClient, tenantID, degraded, "degraded", at(-25*60), nil)
	for m := -60; m < 0; m++ {
		testutil.InsertCheckResult(ctx, t, dbClient, tenantID, degraded, at(m), "success", "monitor", testutil.IntPtr(80))
		testutil.InsertCheckResult(ctx, t, dbClient, tenantID, degraded, at(m), "failure", "monitor", nil)
		testutil.InsertCheckResult(ctx, t, dbClient, tenantID, degraded, at(m), "success", "monitor", testutil.IntPtr(90))
	}
	// Flaky: one failed check, confirmation never reached down.
	insertInterval(ctx, t, dbClient, tenantID, flaky, "up", at(-25*60), ptrTime(at(-20)))
	insertInterval(ctx, t, dbClient, tenantID, flaky, "suspect", at(-20), ptrTime(at(-19)))
	insertInterval(ctx, t, dbClient, tenantID, flaky, "up", at(-19), nil)
	testutil.InsertCheckResult(ctx, t, dbClient, tenantID, flaky, at(-20), "failure", "monitor", nil)
	testutil.InsertCheckResult(ctx, t, dbClient, tenantID, flaky, at(-19), "success", "monitor", testutil.IntPtr(50))
	// Down since before the window: one open period from the window start.
	insertInterval(ctx, t, dbClient, tenantID, down, "down", at(-30*60), nil)
	testutil.InsertCheckResult(ctx, t, dbClient, tenantID, down, at(-5), "failure", "monitor", nil)
	// Paused while failing: unknown time is never downtime (S-P3).
	insertInterval(ctx, t, dbClient, tenantID, paused, "down", at(-25*60), ptrTime(at(-40)))
	insertReasonInterval(ctx, t, dbClient, tenantID, paused, "unknown", "pause", at(-40), nil)
	// Down inside a maintenance window: still listed, as the rollup
	// projection lists it; the headline counts it as planned (S-M3).
	insertInterval(ctx, t, dbClient, tenantID, maint, "up", at(-25*60), ptrTime(at(-50)))
	insertInterval(ctx, t, dbClient, tenantID, maint, "down", at(-50), ptrTime(at(-45)))
	insertInterval(ctx, t, dbClient, tenantID, maint, "up", at(-45), nil)
	insertMaintenance(ctx, t, dbClient, tenantID, maint, at(-55), at(-40))

	get := func(ids ...uuid.UUID) *Result {
		t.Helper()
		res, err := repo.GetScopeAnalytics(ctx, tenantID, ids, Range24h, now)
		if err != nil {
			t.Fatalf("GetScopeAnalytics: %v", err)
		}
		if res.Source != SourceRaw {
			t.Fatalf("Source = %s, want raw", res.Source)
		}
		return res
	}

	for name, id := range map[string]uuid.UUID{"degraded": degraded, "flaky": flaky} {
		res := get(id)
		if len(res.Downtime) != 0 {
			t.Fatalf("%s: downtime = %+v, want none", name, res.Downtime)
		}
		if res.Summary.Method != "interval" {
			t.Fatalf("%s: method = %q, want interval", name, res.Summary.Method)
		}
		assertClose(t, res.Summary.AvailabilityPct, 100)
	}

	res := get(down)
	if len(res.Downtime) != 1 || !res.Downtime[0].Start.Equal(at(-24*60)) || !res.Downtime[0].End.Equal(now) || !res.Downtime[0].IsOpen {
		t.Fatalf("down: downtime = %+v, want one open period [%v, %v]", res.Downtime, at(-24*60), now)
	}
	assertClose(t, res.Summary.AvailabilityPct, 0)

	res = get(paused)
	if len(res.Downtime) != 1 || !res.Downtime[0].Start.Equal(at(-24*60)) || !res.Downtime[0].End.Equal(at(-40)) || res.Downtime[0].IsOpen {
		t.Fatalf("paused: downtime = %+v, want one closed period ending at the pause", res.Downtime)
	}

	res = get(maint)
	if len(res.Downtime) != 1 || !res.Downtime[0].Start.Equal(at(-50)) || !res.Downtime[0].End.Equal(at(-45)) || res.Downtime[0].IsOpen {
		t.Fatalf("maintenance: downtime = %+v, want [%v, %v]", res.Downtime, at(-50), at(-45))
	}
	assertClose(t, res.Summary.AvailabilityPct, 100) // planned, not unavailability

	// A scope (a group's leaf members) lists the union of its members' periods.
	res = get(degraded, flaky, maint)
	if len(res.Downtime) != 1 || !res.Downtime[0].Start.Equal(at(-50)) {
		t.Fatalf("scope: downtime = %+v, want only the maintenance monitor's period", res.Downtime)
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
