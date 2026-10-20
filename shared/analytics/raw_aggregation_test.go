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

func TestBuildRawResultWeightedMetrics(t *testing.T) {
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
	// Failed check rows are evidence, never downtime: the raw path reads the
	// downtime list from the state timeline (loadTimelineDowntime).
	if res.Downtime != nil {
		t.Fatalf("raw builder derived downtime from check rows: %+v", res.Downtime)
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
	}
}

func TestDowntimeFromOutages_PerMonitorSpansMergedCompositeSkipped(t *testing.T) {
	end := time.Date(2026, time.September, 5, 12, 0, 0, 0, time.UTC)
	at := func(min int) time.Time { return end.Add(time.Duration(min-60) * time.Minute) }
	a, b := uuid.New(), uuid.New()
	outages := []Outage{
		// Serial composite (unplanned only): never listed on its own.
		{MonitorID: nil, Start: at(0), End: at(5)},
		{MonitorID: &a, Start: at(0), End: at(10), PlannedSeconds: 300},
		// Overlaps A's span: one merged period for the scope.
		{MonitorID: &b, Start: at(8), End: at(15)},
		// Still down: runs to the window end, flagged open.
		{MonitorID: &a, Start: at(50), End: at(59), Ongoing: true},
	}
	got := downtimeFromOutages(outages, end)
	want := []DowntimePeriod{
		{Start: at(0), End: at(15)},
		{Start: at(50), End: end, IsOpen: true},
	}
	if len(got) != len(want) {
		t.Fatalf("periods = %+v, want %+v", got, want)
	}
	for i := range want {
		if !got[i].Start.Equal(want[i].Start) || !got[i].End.Equal(want[i].End) || got[i].IsOpen != want[i].IsOpen {
			t.Fatalf("period %d = %+v, want %+v", i, got[i], want[i])
		}
	}
	if got := downtimeFromOutages(nil, end); got != nil {
		t.Fatalf("no outages = %+v, want nil", got)
	}
}
