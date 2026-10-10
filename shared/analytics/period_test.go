package analytics

// Calendar period tests (docs/state-semantics.md S-U7).

import (
	"testing"
	"time"
)

func mustLoc(t *testing.T, name string) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(name)
	if err != nil {
		t.Fatalf("load %s: %v", name, err)
	}
	return loc
}

func TestPeriods_DSTMonthsFollowTheLocalCalendar(t *testing.T) {
	paris := mustLoc(t, "Europe/Paris")
	for key, hours := range map[string]float64{"2026-10": 745, "2026-03": 743, "2026-09": 720} {
		p, err := ParsePeriod(PeriodMonthly, paris, key)
		if err != nil {
			t.Fatalf("parse %s: %v", key, err)
		}
		if got := p.End.Sub(p.Start).Hours(); got != hours {
			t.Fatalf("%s is %vh, want %vh", key, got, hours)
		}
	}

	oct, _ := ParsePeriod(PeriodMonthly, paris, "2026-10")
	days := DayBuckets(paris, oct.Start, oct.End)
	if len(days) != 31 {
		t.Fatalf("October has %d day buckets", len(days))
	}
	if got := days[24].Seconds() / 3600; got != 25 {
		t.Fatalf("Oct 25 (DST end) is %vh, want 25h", got)
	}
}

func TestPeriods_KeysAndStepping(t *testing.T) {
	utc := time.UTC
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, utc)

	w, _ := PeriodContaining(PeriodWeekly, utc, now)
	if w.Key != "2026-W40" || !w.Start.Equal(time.Date(2026, 9, 28, 0, 0, 0, 0, utc)) {
		t.Fatalf("week = %s from %v", w.Key, w.Start)
	}
	if prev := w.Previous(utc); prev.Key != "2026-W39" {
		t.Fatalf("previous week = %s", prev.Key)
	}

	q, _ := PeriodContaining(PeriodQuarterly, utc, now)
	if q.Key != "2026-Q3" || q.Next(utc).Key != "2026-Q4" {
		t.Fatalf("quarter = %s, next %s", q.Key, q.Next(utc).Key)
	}

	m, _ := PeriodContaining(PeriodMonthly, utc, time.Date(2026, 1, 15, 0, 0, 0, 0, utc))
	if m.Previous(utc).Key != "2025-12" {
		t.Fatalf("previous month = %s", m.Previous(utc).Key)
	}

	// 2026 starts on a Thursday, so it has an ISO week 53; 2027 does not.
	if _, err := ParsePeriod(PeriodWeekly, utc, "2026-W53"); err != nil {
		t.Fatalf("2026-W53: %v", err)
	}
	if _, err := ParsePeriod(PeriodWeekly, utc, "2027-W53"); err == nil {
		t.Fatal("2027-W53 should not parse")
	}
	if p, err := ParsePeriod(PeriodWeekly, utc, "2026-W01"); err != nil || !p.Start.Equal(time.Date(2025, 12, 29, 0, 0, 0, 0, utc)) {
		t.Fatalf("2026-W01 = %v (%v)", p.Start, err)
	}

	c, err := CustomPeriod(utc, "2026-09-01", "2026-09-30")
	if err != nil || c.End.Sub(c.Start) != 30*24*time.Hour {
		t.Fatalf("custom = %v..%v (%v)", c.Start, c.End, err)
	}
}
