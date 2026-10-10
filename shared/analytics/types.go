package analytics

import (
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
)

type Range string

const (
	Range1h   Range = "1h"
	Range6h   Range = "6h"
	Range24h  Range = "24h"
	Range7d   Range = "7d"
	Range30d  Range = "30d"
	Range90d  Range = "90d"
	Range365d Range = "365d"
)

type Source string

const (
	SourceRaw    Source = "raw"
	SourceRollup Source = "rollup"
)

type Summary struct {
	// HasData is false when no monitor in scope had a single check in the
	// window; the percentage fields are then meaningless zeros and must
	// render as no-data, never as 0% or 100% (S-D1, docs/state-semantics.md).
	HasData bool
	// Method labels how AvailabilityPct was computed (S-U5): "interval" =
	// time integration over monitor_state_intervals; "sampled" = the legacy
	// success/total count (used when any monitor's timeline starts after the
	// window start). Never silently mixed.
	Method          string
	AvailabilityPct float64
	// CoveragePct is the observed share of the window (unknown and paused
	// time excluded from the denominator show up here instead — S-U2). Only
	// set for Method "interval".
	CoveragePct     *float64
	UptimePct       float64
	SLAPct          float64
	DowntimePct     float64
	AvgLatencyMS    *float64
	MedianLatencyMS *float64
	P95LatencyMS    *float64
	LatestStatus    *string
	LatestCheckAt   *time.Time
}

type SeriesPoint struct {
	BucketStart  time.Time
	UptimePct    float64
	AvgLatencyMS *float64
	TotalChecks  int
	HasData      bool
}

type DowntimePeriod struct {
	Start  time.Time
	End    time.Time
	IsOpen bool
}

type Result struct {
	Range         Range
	GeneratedAt   time.Time
	Source        Source
	CoverageStart *time.Time
	IsPartial     bool
	Summary       Summary
	Series        []SeriesPoint
	Downtime      []DowntimePeriod
}

type Window struct {
	Range        Range
	Start        time.Time
	End          time.Time
	Bucket       time.Duration
	UseRollup    bool
	LabelFormat  string
	DisplayCount int
}

func (r Range) IsLongRange() bool {
	switch r {
	case Range7d, Range30d, Range90d, Range365d:
		return true
	default:
		return false
	}
}

func ResolveWindow(rangeValue Range, now time.Time) (Window, error) {
	now = now.UTC()
	switch rangeValue {
	case Range1h:
		return Window{Range: rangeValue, Start: now.Add(-1 * time.Hour), End: now, Bucket: 5 * time.Minute, DisplayCount: 12, LabelFormat: "15:04"}, nil
	case Range6h:
		return Window{Range: rangeValue, Start: now.Add(-6 * time.Hour), End: now, Bucket: 30 * time.Minute, DisplayCount: 12, LabelFormat: "15:04"}, nil
	case Range24h:
		return Window{Range: rangeValue, Start: now.Add(-24 * time.Hour), End: now, Bucket: time.Hour, DisplayCount: 24, LabelFormat: "15:04"}, nil
	case Range7d:
		return newDailyWindow(rangeValue, now, 7), nil
	case Range30d:
		return newDailyWindow(rangeValue, now, 30), nil
	case Range90d:
		return newDailyWindow(rangeValue, now, 90), nil
	case Range365d:
		return newDailyWindow(rangeValue, now, 365), nil
	default:
		return Window{}, fmt.Errorf("invalid analytics range: %s", rangeValue)
	}
}

func newDailyWindow(rangeValue Range, now time.Time, days int) Window {
	dayEnd := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	start := dayEnd.AddDate(0, 0, -(days - 1))
	return Window{
		Range:        rangeValue,
		Start:        start,
		End:          now,
		Bucket:       24 * time.Hour,
		UseRollup:    true,
		DisplayCount: days,
		LabelFormat:  "2006-01-02",
	}
}

func BucketStarts(window Window) []time.Time {
	starts := make([]time.Time, 0, window.DisplayCount)
	cursor := window.Start
	for !cursor.After(window.End) {
		starts = append(starts, cursor)
		if window.UseRollup {
			cursor = cursor.AddDate(0, 0, 1)
		} else {
			cursor = cursor.Add(window.Bucket)
		}
	}
	if len(starts) > window.DisplayCount {
		starts = starts[:window.DisplayCount]
	}
	return starts
}

func PtrFloat64(v float64) *float64 {
	return &v
}

func cloneTimePtr(v *time.Time) *time.Time {
	if v == nil {
		return nil
	}
	copy := *v
	return &copy
}

func cloneStringPtr(v *string) *string {
	if v == nil {
		return nil
	}
	copy := *v
	return &copy
}

func percentile(sortedValues []float64, p float64) *float64 {
	if len(sortedValues) == 0 {
		return nil
	}
	if len(sortedValues) == 1 {
		return PtrFloat64(sortedValues[0])
	}
	idx := p * float64(len(sortedValues)-1)
	lower := int(idx)
	upper := lower + 1
	if upper >= len(sortedValues) {
		return PtrFloat64(sortedValues[len(sortedValues)-1])
	}
	frac := idx - float64(lower)
	value := sortedValues[lower] + frac*(sortedValues[upper]-sortedValues[lower])
	return PtrFloat64(value)
}

func median(values []float64) *float64 {
	if len(values) == 0 {
		return nil
	}
	sorted := append([]float64(nil), values...)
	sort.Float64s(sorted)
	return percentile(sorted, 0.5)
}

func aggregateStatus(statuses []string) *string {
	if len(statuses) == 0 {
		return nil
	}
	hasFailure := false
	hasError := false
	hasSuccess := false
	for _, status := range statuses {
		switch status {
		case "error":
			hasError = true
		case "failure", "down":
			hasFailure = true
		case "success", "up":
			hasSuccess = true
		}
	}
	var result string
	switch {
	case hasError:
		result = "error"
	case hasFailure:
		result = "failure"
	case hasSuccess:
		result = "success"
	default:
		result = statuses[0]
	}
	return &result
}

func dedupeUUIDs(ids []uuid.UUID) []uuid.UUID {
	if len(ids) == 0 {
		return nil
	}
	seen := make(map[uuid.UUID]struct{}, len(ids))
	out := make([]uuid.UUID, 0, len(ids))
	for _, id := range ids {
		if id == uuid.Nil {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}
