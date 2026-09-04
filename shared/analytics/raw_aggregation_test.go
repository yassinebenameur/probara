package analytics

import (
	"database/sql"
	"testing"
	"time"

	"github.com/google/uuid"
)

// A tenant with 1,000 monitors checked once per minute generates 1.44M rows
// per day. Fixture allocation is excluded to measure aggregation alone.
func BenchmarkBuildRawResultTenant24h(b *testing.B) {
	now := time.Date(2026, time.September, 5, 12, 0, 0, 0, time.UTC)
	window, _ := ResolveWindow(Range24h, now)
	monitors := make([]uuid.UUID, 1000)
	for i := range monitors {
		monitors[i] = uuid.New()
	}
	rows := make([]rawRow, 0, len(monitors)*1440)
	for minute := 0; minute < 1440; minute++ {
		for i, monitor := range monitors {
			status := "success"
			if minute%100 == 0 && i%10 == 0 {
				status = "failure"
			}
			rows = append(rows, rawRow{MonitorID: monitor, Status: status,
				LatencyMS: sql.NullInt64{Int64: int64((minute*17 + i*23) % 10000), Valid: true},
				CreatedAt: window.Start.Add(time.Duration(minute) * time.Minute)})
		}
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		res := buildRawResult(window, now, rows)
		if !res.Summary.HasData || res.Summary.P95LatencyMS == nil {
			b.Fatal("missing summary")
		}
	}
}

func TestBuildRawResultWeightedMetricsAndDowntime(t *testing.T) {
	now := time.Date(2026, time.September, 5, 12, 0, 0, 0, time.UTC)
	window, _ := ResolveWindow(Range1h, now)
	a, b := uuid.New(), uuid.New()
	latency := func(value int64) sql.NullInt64 { return sql.NullInt64{Int64: value, Valid: true} }
	rows := []rawRow{
		{a, "failure", latency(9000), window.Start},
		{b, "failure", sql.NullInt64{}, window.Start.Add(time.Minute)},
		{a, "success", latency(10), window.Start.Add(2 * time.Minute)},
		{b, "success", latency(100), window.Start.Add(3 * time.Minute)},
		{a, "success", latency(20), window.Start.Add(4 * time.Minute)},
		{a, "success", sql.NullInt64{}, window.Start.Add(5 * time.Minute)},
		{a, "success", latency(30), window.Start.Add(6 * time.Minute)},
		{a, "failure", sql.NullInt64{}, window.Start.Add(7 * time.Minute)},
	}
	res := buildRawResult(window, now, rows)
	assertClose(t, res.Summary.UptimePct, (4.0/6*100+50)/2)
	assertClose(t, *res.Summary.AvgLatencyMS, 60) // Mean of monitor means: (20+100)/2.
	assertClose(t, *res.Summary.MedianLatencyMS, 25)
	assertClose(t, *res.Summary.P95LatencyMS, 89.5)
	if res.IsPartial || res.CoverageStart == nil || !res.CoverageStart.Equal(window.Start) {
		t.Fatalf("incorrect coverage: %+v", res)
	}
	if res.Summary.LatestStatus == nil || *res.Summary.LatestStatus != "failure" || !res.Summary.LatestCheckAt.Equal(rows[7].CreatedAt) {
		t.Fatalf("incorrect latest state: %+v", res.Summary)
	}
	first := res.Series[0]
	assertClose(t, first.UptimePct, (2.0/3*100+50)/2)
	assertClose(t, *first.AvgLatencyMS, 57.5)
	if first.TotalChecks != 5 || !first.HasData {
		t.Fatalf("incorrect series: %+v", first)
	}
	if len(res.Downtime) != 2 || !res.Downtime[0].Start.Equal(window.Start) || !res.Downtime[0].End.Equal(rows[3].CreatedAt) || res.Downtime[0].IsOpen || !res.Downtime[1].Start.Equal(rows[7].CreatedAt) || !res.Downtime[1].End.Equal(now) || !res.Downtime[1].IsOpen {
		t.Fatalf("incorrect merged downtime: %+v", res.Downtime)
	}
}

func TestBuildRawResultEmptyAndMissingLatency(t *testing.T) {
	now := time.Date(2026, time.September, 5, 12, 0, 0, 0, time.UTC)
	window, _ := ResolveWindow(Range1h, now)
	for _, rows := range [][]rawRow{nil, {{MonitorID: uuid.New(), Status: "success", CreatedAt: now.Add(-time.Minute)}}} {
		res := buildRawResult(window, now, rows)
		if len(res.Series) != window.DisplayCount || !res.IsPartial || res.Summary.HasData != (len(rows) > 0) {
			t.Fatalf("incorrect data coverage: %+v", res)
		}
		if res.Summary.AvgLatencyMS != nil || res.Summary.MedianLatencyMS != nil || res.Summary.P95LatencyMS != nil {
			t.Fatalf("missing latency should remain nil: %+v", res.Summary)
		}
	}
}

func TestBuildRawResultPreservesSameTimestampOrder(t *testing.T) {
	now := time.Date(2026, time.September, 5, 12, 0, 0, 0, time.UTC)
	window, _ := ResolveWindow(Range1h, now)
	monitor := uuid.New()
	ts := now.Add(-time.Minute)
	for _, statuses := range [][]string{{"failure", "failure", "success"}, {"success", "failure", "failure"}} {
		rows := make([]rawRow, 0, len(statuses))
		for _, status := range statuses {
			rows = append(rows, rawRow{MonitorID: monitor, Status: status, CreatedAt: ts})
		}
		res := buildRawResult(window, now, rows)
		lastStatus := statuses[len(statuses)-1]
		if res.Summary.LatestStatus == nil || *res.Summary.LatestStatus != lastStatus {
			t.Fatalf("latest status must follow database id order: %+v", res.Summary)
		}
		if len(res.Downtime) != 1 || !res.Downtime[0].Start.Equal(ts) || res.Downtime[0].IsOpen != (lastStatus == "failure") {
			t.Fatalf("incorrect downtime for tied timestamps: %+v", res.Downtime)
		}
		wantEnd := ts
		if lastStatus == "failure" {
			wantEnd = now
		}
		if !res.Downtime[0].End.Equal(wantEnd) {
			t.Fatalf("downtime end = %s, want %s", res.Downtime[0].End, wantEnd)
		}
	}
}
