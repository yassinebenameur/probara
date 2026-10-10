package analytics

// Calendar reporting periods (docs/state-semantics.md S-U7): SLA periods are
// calendar weeks (ISO, Monday start), months or quarters in an IANA
// timezone, so their length follows the local calendar — a DST month is 743
// or 745 hours long, never 744.

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

type PeriodKind string

const (
	PeriodWeekly    PeriodKind = "weekly"
	PeriodMonthly   PeriodKind = "monthly"
	PeriodQuarterly PeriodKind = "quarterly"
)

func (k PeriodKind) Valid() bool {
	switch k {
	case PeriodWeekly, PeriodMonthly, PeriodQuarterly:
		return true
	}
	return false
}

// Period is a half-open calendar span. Key identifies it: "2026-09",
// "2026-Q3" or "2026-W39"; custom ranges use "YYYY-MM-DD..YYYY-MM-DD".
type Period struct {
	Kind  PeriodKind
	Key   string
	Start time.Time
	End   time.Time
}

// Range is the period as a TimeRange.
func (p Period) Range() TimeRange { return TimeRange{Start: p.Start, End: p.End} }

// PeriodContaining returns the period of kind that contains t, in loc.
func PeriodContaining(kind PeriodKind, loc *time.Location, t time.Time) (Period, error) {
	t = t.In(loc)
	switch kind {
	case PeriodMonthly:
		return monthPeriod(t.Year(), t.Month(), loc), nil
	case PeriodQuarterly:
		return quarterPeriod(t.Year(), (int(t.Month())-1)/3+1, loc), nil
	case PeriodWeekly:
		y, w := t.ISOWeek()
		return weekPeriod(y, w, loc), nil
	}
	return Period{}, fmt.Errorf("unknown period kind %q", kind)
}

// ParsePeriod parses a period key of the given kind.
func ParsePeriod(kind PeriodKind, loc *time.Location, key string) (Period, error) {
	key = strings.TrimSpace(key)
	switch kind {
	case PeriodMonthly:
		t, err := time.ParseInLocation("2006-01", key, loc)
		if err != nil {
			return Period{}, fmt.Errorf("period must look like 2026-09")
		}
		return monthPeriod(t.Year(), t.Month(), loc), nil
	case PeriodQuarterly:
		y, q, ok := splitKey(key, "-Q")
		if !ok || q < 1 || q > 4 {
			return Period{}, fmt.Errorf("period must look like 2026-Q3")
		}
		return quarterPeriod(y, q, loc), nil
	case PeriodWeekly:
		y, w, ok := splitKey(key, "-W")
		if !ok || w < 1 || w > 53 {
			return Period{}, fmt.Errorf("period must look like 2026-W39")
		}
		p := weekPeriod(y, w, loc)
		// Week 53 exists only in long ISO years.
		if gy, gw := p.Start.ISOWeek(); gy != y || gw != w {
			return Period{}, fmt.Errorf("%d has no ISO week %d", y, w)
		}
		return p, nil
	}
	return Period{}, fmt.Errorf("unknown period kind %q", kind)
}

// CustomPeriod spans whole local days from..to inclusive (YYYY-MM-DD).
func CustomPeriod(loc *time.Location, from, to string) (Period, error) {
	f, err := time.ParseInLocation("2006-01-02", strings.TrimSpace(from), loc)
	if err != nil {
		return Period{}, fmt.Errorf("from must be a date (YYYY-MM-DD)")
	}
	t, err := time.ParseInLocation("2006-01-02", strings.TrimSpace(to), loc)
	if err != nil {
		return Period{}, fmt.Errorf("to must be a date (YYYY-MM-DD)")
	}
	if t.Before(f) {
		return Period{}, fmt.Errorf("to must not be before from")
	}
	return Period{
		Key:   f.Format("2006-01-02") + ".." + t.Format("2006-01-02"),
		Start: f,
		End:   time.Date(t.Year(), t.Month(), t.Day()+1, 0, 0, 0, 0, loc),
	}, nil
}

// Previous / Next step one period of the same kind.
func (p Period) Previous(loc *time.Location) Period {
	prev, _ := PeriodContaining(p.Kind, loc, p.Start.Add(-time.Second))
	return prev
}

func (p Period) Next(loc *time.Location) Period {
	next, _ := PeriodContaining(p.Kind, loc, p.End)
	return next
}

// DayBuckets splits [start, end) into local calendar days in loc; the first
// and last buckets are clipped to the span.
func DayBuckets(loc *time.Location, start, end time.Time) []TimeRange {
	var out []TimeRange
	cur := start.In(loc)
	day := time.Date(cur.Year(), cur.Month(), cur.Day(), 0, 0, 0, 0, loc)
	for day.Before(end) {
		next := time.Date(day.Year(), day.Month(), day.Day()+1, 0, 0, 0, 0, loc)
		s, e := day, next
		if s.Before(start) {
			s = start
		}
		if e.After(end) {
			e = end
		}
		if e.After(s) {
			out = append(out, TimeRange{Start: s, End: e})
		}
		day = next
	}
	return out
}

func monthPeriod(y int, m time.Month, loc *time.Location) Period {
	start := time.Date(y, m, 1, 0, 0, 0, 0, loc)
	return Period{Kind: PeriodMonthly, Key: start.Format("2006-01"), Start: start, End: start.AddDate(0, 1, 0)}
}

func quarterPeriod(y, q int, loc *time.Location) Period {
	start := time.Date(y, time.Month((q-1)*3+1), 1, 0, 0, 0, 0, loc)
	return Period{Kind: PeriodQuarterly, Key: fmt.Sprintf("%d-Q%d", y, q), Start: start, End: start.AddDate(0, 3, 0)}
}

func weekPeriod(y, w int, loc *time.Location) Period {
	// ISO week 1 contains January 4th; weeks start on Monday.
	jan4 := time.Date(y, time.January, 4, 0, 0, 0, 0, loc)
	offset := (int(jan4.Weekday()) + 6) % 7
	start := time.Date(y, time.January, 4-offset+(w-1)*7, 0, 0, 0, 0, loc)
	return Period{Kind: PeriodWeekly, Key: fmt.Sprintf("%d-W%02d", y, w), Start: start, End: start.AddDate(0, 0, 7)}
}

func splitKey(key, sep string) (int, int, bool) {
	parts := strings.SplitN(key, sep, 2)
	if len(parts) != 2 {
		return 0, 0, false
	}
	y, err1 := strconv.Atoi(parts[0])
	n, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil || y < 1970 || y > 9999 {
		return 0, 0, false
	}
	return y, n, true
}
