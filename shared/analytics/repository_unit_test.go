package analytics

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

// D2 regression tests: summary SLA/latency must pool checks across the whole
// window per monitor (sum/sum), not average per-day rates, while the per-day
// series keeps day-scoped rates.

func TestBuildRollupResult_SLAUsesWholeWindowPerMonitorRate(t *testing.T) {
	now := time.Date(2026, time.March, 6, 12, 0, 0, 0, time.UTC)
	window, err := ResolveWindow(Range7d, now)
	if err != nil {
		t.Fatalf("ResolveWindow() error = %v", err)
	}
	monitorID := uuid.New()
	dayA := time.Date(2026, time.March, 4, 0, 0, 0, 0, time.UTC)
	dayB := time.Date(2026, time.March, 5, 0, 0, 0, 0, time.UTC)

	rows := []rollupRow{
		// 1 check on day A, failing.
		{MonitorID: monitorID, BucketDay: dayA, TotalChecks: 1, SuccessChecks: 0},
		// 1000 checks on day B, all succeeding.
		{MonitorID: monitorID, BucketDay: dayB, TotalChecks: 1000, SuccessChecks: 1000},
	}

	res := buildRollupResult(window, now, rows)

	// Whole-window rate: 1000/1001 successes — NOT average(0%, 100%) = 50%.
	want := 1000.0 / 1001.0 * 100
	assertClose(t, res.Summary.SLAPct, want)
	assertClose(t, res.Summary.UptimePct, want)
	assertClose(t, res.Summary.DowntimePct, 100-want)

	// The series must stay day-scoped: each point is that day's own rate.
	assertClose(t, findSeriesPoint(t, res.Series, dayA).UptimePct, 0)
	assertClose(t, findSeriesPoint(t, res.Series, dayB).UptimePct, 100)
}

func TestBuildRollupResult_LatencyUsesWholeWindowPerMonitorAverage(t *testing.T) {
	now := time.Date(2026, time.March, 6, 12, 0, 0, 0, time.UTC)
	window, err := ResolveWindow(Range7d, now)
	if err != nil {
		t.Fatalf("ResolveWindow() error = %v", err)
	}
	monitorID := uuid.New()
	dayA := time.Date(2026, time.March, 4, 0, 0, 0, 0, time.UTC)
	dayB := time.Date(2026, time.March, 5, 0, 0, 0, 0, time.UTC)

	rows := []rollupRow{
		// 1 successful check on day A at 1000ms.
		{MonitorID: monitorID, BucketDay: dayA, TotalChecks: 1, SuccessChecks: 1, LatencySuccessSumMS: 1000, LatencySuccessCount: 1},
		// 1000 successful checks on day B averaging 100ms.
		{MonitorID: monitorID, BucketDay: dayB, TotalChecks: 1000, SuccessChecks: 1000, LatencySuccessSumMS: 100000, LatencySuccessCount: 1000},
	}

	res := buildRollupResult(window, now, rows)

	// Whole-window mean: (1000 + 100000) / 1001 — NOT average(1000ms, 100ms) = 550ms.
	if res.Summary.AvgLatencyMS == nil {
		t.Fatalf("AvgLatencyMS = nil, want value")
	}
	assertClose(t, *res.Summary.AvgLatencyMS, 101000.0/1001.0)

	// Series latency points stay day-scoped averages.
	pointA := findSeriesPoint(t, res.Series, dayA)
	if pointA.AvgLatencyMS == nil {
		t.Fatalf("day A AvgLatencyMS = nil, want value")
	}
	assertClose(t, *pointA.AvgLatencyMS, 1000)
	pointB := findSeriesPoint(t, res.Series, dayB)
	if pointB.AvgLatencyMS == nil {
		t.Fatalf("day B AvgLatencyMS = nil, want value")
	}
	assertClose(t, *pointB.AvgLatencyMS, 100)
}

func TestBuildRollupResult_OuterAggregationIsMonitorWeighted(t *testing.T) {
	now := time.Date(2026, time.March, 6, 12, 0, 0, 0, time.UTC)
	window, err := ResolveWindow(Range7d, now)
	if err != nil {
		t.Fatalf("ResolveWindow() error = %v", err)
	}
	monitorA := uuid.New()
	monitorB := uuid.New()
	dayA := time.Date(2026, time.March, 4, 0, 0, 0, 0, time.UTC)
	dayB := time.Date(2026, time.March, 5, 0, 0, 0, 0, time.UTC)

	rows := []rollupRow{
		// Monitor A: whole-window 1000/1001 ≈ 99.9%.
		{MonitorID: monitorA, BucketDay: dayA, TotalChecks: 1, SuccessChecks: 0},
		{MonitorID: monitorA, BucketDay: dayB, TotalChecks: 1000, SuccessChecks: 1000},
		// Monitor B: whole-window 5/10 = 50%, despite far fewer checks.
		{MonitorID: monitorB, BucketDay: dayB, TotalChecks: 10, SuccessChecks: 5},
	}

	res := buildRollupResult(window, now, rows)

	// Each monitor contributes one data point regardless of check volume.
	want := (1000.0/1001.0*100 + 50.0) / 2
	assertClose(t, res.Summary.SLAPct, want)
}
