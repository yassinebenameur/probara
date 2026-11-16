package maintenance

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// Tests cite docs/state-semantics.md S-M5 (recurring coverage).

func utc(y int, m time.Month, d, hh, mm int) time.Time {
	return time.Date(y, m, d, hh, mm, 0, 0, time.UTC)
}

func mustRule(t *testing.T, r Rule) Rule {
	t.Helper()
	n, err := r.Normalize()
	if err != nil {
		t.Fatalf("Normalize(%+v): %v", r, err)
	}
	return n
}

func starts(occ []Occurrence) []time.Time {
	out := make([]time.Time, len(occ))
	for i, o := range occ {
		out[i] = o.Start.UTC()
	}
	return out
}

func assertStarts(t *testing.T, got []Occurrence, want ...time.Time) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %d occurrences %v, want %d %v", len(got), starts(got), len(want), want)
	}
	for i := range want {
		if !got[i].Start.Equal(want[i]) {
			t.Errorf("occurrence %d starts %v, want %v", i, got[i].Start.UTC(), want[i])
		}
	}
}

// S-M5: a weekly rule stays at its local start time across DST changes.
func TestExpand_StaysOnLocalTimeAcrossDST_EuropeParis(t *testing.T) {
	r := mustRule(t, Rule{
		Weekdays:    []time.Weekday{time.Tuesday, time.Thursday},
		StartMinute: 2 * 60,
		Duration:    90 * time.Minute,
		Timezone:    "Europe/Paris",
	})
	occ, err := r.Expand(utc(2026, 3, 23, 0, 0), utc(2026, 4, 3, 0, 0))
	if err != nil {
		t.Fatal(err)
	}
	// CET (+1) before 29 March, CEST (+2) after: 02:00 local each time.
	assertStarts(t, occ,
		utc(2026, 3, 24, 1, 0), utc(2026, 3, 26, 1, 0),
		utc(2026, 3, 31, 0, 0), utc(2026, 4, 2, 0, 0))
	for _, o := range occ {
		if o.End.Sub(o.Start) != 90*time.Minute {
			t.Errorf("occurrence %v lasts %v, want 90m", o.Start, o.End.Sub(o.Start))
		}
		if l := o.Start.In(mustZone(t, "Europe/Paris")); l.Hour() != 2 || l.Minute() != 0 {
			t.Errorf("occurrence starts %v local, want 02:00", l)
		}
	}
}

func mustZone(t *testing.T, name string) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(name)
	if err != nil {
		t.Fatal(err)
	}
	return loc
}

// S-M5: a start time the spring-forward gap skips runs at the next valid
// instant, the end of the gap; the duration is elapsed time.
func TestExpand_SpringForwardGapShiftsToNextValidInstant(t *testing.T) {
	for _, tc := range []struct {
		name   string
		zone   string
		minute int
		day    time.Time // a day before the transition Sunday, UTC
		want   time.Time
	}{
		// Paris: 02:00 CET -> 03:00 CEST at 01:00Z on 29 March 2026.
		{"paris 02:30", "Europe/Paris", 2*60 + 30, utc(2026, 3, 28, 0, 0), utc(2026, 3, 29, 1, 0)},
		{"paris 02:00", "Europe/Paris", 2 * 60, utc(2026, 3, 28, 0, 0), utc(2026, 3, 29, 1, 0)},
		// New York: 02:00 EST -> 03:00 EDT at 07:00Z on 8 March 2026.
		{"new york 02:30", "America/New_York", 2*60 + 30, utc(2026, 3, 7, 0, 0), utc(2026, 3, 8, 7, 0)},
		// Adelaide (+9:30 -> +10:30): 02:00 -> 03:00 at 16:30Z on 3 October.
		{"adelaide 02:15", "Australia/Adelaide", 2*60 + 15, utc(2026, 10, 3, 0, 0), utc(2026, 10, 3, 16, 30)},
		// Lord Howe shifts by half an hour: 02:00 -> 02:30 at 15:30Z.
		{"lord howe 02:15", "Australia/Lord_Howe", 2*60 + 15, utc(2026, 10, 2, 0, 0), utc(2026, 10, 3, 15, 30)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := mustRule(t, Rule{Weekdays: []time.Weekday{time.Sunday}, StartMinute: tc.minute, Duration: time.Hour, Timezone: tc.zone})
			occ, err := r.Expand(tc.day, tc.day.Add(3*24*time.Hour))
			if err != nil {
				t.Fatal(err)
			}
			assertStarts(t, occ, tc.want)
			if occ[0].End.Sub(occ[0].Start) != time.Hour {
				t.Errorf("lasts %v, want 1h elapsed", occ[0].End.Sub(occ[0].Start))
			}
		})
	}
}

// S-M5: a start time the fall-back overlap repeats runs once, at its first
// instance, and lasts its duration in elapsed time.
func TestExpand_FallBackOverlapTakesFirstInstance(t *testing.T) {
	for _, tc := range []struct {
		name   string
		zone   string
		minute int
		day    time.Time
		want   time.Time
	}{
		// Paris: 03:00 CEST -> 02:00 CET at 01:00Z on 25 October 2026;
		// 02:30 happens at 00:30Z (CEST) and 01:30Z (CET).
		{"paris 02:30", "Europe/Paris", 2*60 + 30, utc(2026, 10, 24, 0, 0), utc(2026, 10, 25, 0, 30)},
		// New York: 02:00 EDT -> 01:00 EST at 06:00Z on 1 November 2026;
		// 01:30 happens at 05:30Z (EDT) and 06:30Z (EST).
		{"new york 01:30", "America/New_York", 60 + 30, utc(2026, 10, 31, 0, 0), utc(2026, 11, 1, 5, 30)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := mustRule(t, Rule{Weekdays: []time.Weekday{time.Sunday}, StartMinute: tc.minute, Duration: 90 * time.Minute, Timezone: tc.zone})
			occ, err := r.Expand(tc.day, tc.day.Add(3*24*time.Hour))
			if err != nil {
				t.Fatal(err)
			}
			assertStarts(t, occ, tc.want)
			if got := occ[0].End.Sub(occ[0].Start); got != 90*time.Minute {
				t.Errorf("lasts %v, want 90m elapsed", got)
			}
		})
	}
}

// S-M5: zones without DST and with half-hour offsets.
func TestExpand_FixedAndHalfHourOffsetZones(t *testing.T) {
	tokyo := mustRule(t, Rule{Weekdays: []time.Weekday{time.Monday}, StartMinute: 9 * 60, Duration: time.Hour, Timezone: "Asia/Tokyo"})
	occ, err := tokyo.Expand(utc(2026, 3, 1, 0, 0), utc(2026, 4, 1, 0, 0))
	if err != nil {
		t.Fatal(err)
	}
	// Mondays of March 2026, 09:00 JST = 00:00Z, no DST shift.
	assertStarts(t, occ, utc(2026, 3, 2, 0, 0), utc(2026, 3, 9, 0, 0), utc(2026, 3, 16, 0, 0),
		utc(2026, 3, 23, 0, 0), utc(2026, 3, 30, 0, 0))

	kolkata := mustRule(t, Rule{Weekdays: []time.Weekday{time.Wednesday}, StartMinute: 10*60 + 30, Duration: 45 * time.Minute, Timezone: "Asia/Kolkata"})
	occ, err = kolkata.Expand(utc(2026, 6, 1, 0, 0), utc(2026, 6, 15, 0, 0))
	if err != nil {
		t.Fatal(err)
	}
	// 10:30 IST (+5:30) = 05:00Z.
	assertStarts(t, occ, utc(2026, 6, 3, 5, 0), utc(2026, 6, 10, 5, 0))

	// Adelaide (+9:30/+10:30) keeps 23:00 local across its October change.
	adelaide := mustRule(t, Rule{Weekdays: []time.Weekday{time.Saturday}, StartMinute: 23 * 60, Duration: time.Hour, Timezone: "Australia/Adelaide"})
	occ, err = adelaide.Expand(utc(2026, 9, 25, 0, 0), utc(2026, 10, 12, 0, 0))
	if err != nil {
		t.Fatal(err)
	}
	assertStarts(t, occ, utc(2026, 9, 26, 13, 30), utc(2026, 10, 3, 13, 30), utc(2026, 10, 10, 12, 30))
}

// S-M5: until is the last local date an occurrence may start on.
func TestExpand_UntilIsInclusiveLocalDate(t *testing.T) {
	r := mustRule(t, Rule{
		Weekdays:    []time.Weekday{time.Monday, time.Tuesday, time.Wednesday, time.Thursday, time.Friday, time.Saturday, time.Sunday},
		StartMinute: 23*60 + 30,
		Duration:    time.Hour,
		Timezone:    "America/New_York",
		Until:       "2026-06-10",
	})
	occ, err := r.Expand(utc(2026, 6, 8, 12, 0), utc(2026, 6, 20, 0, 0))
	if err != nil {
		t.Fatal(err)
	}
	// 23:30 EDT is 03:30Z the next UTC day; the last start is local 10 June.
	assertStarts(t, occ, utc(2026, 6, 9, 3, 30), utc(2026, 6, 10, 3, 30), utc(2026, 6, 11, 3, 30))

	next, err := r.Next(utc(2026, 6, 11, 4, 0), 3)
	if err != nil {
		t.Fatal(err)
	}
	assertStarts(t, next, utc(2026, 6, 11, 3, 30)) // still running at 04:00Z, then nothing

	next, err = r.Next(utc(2026, 7, 1, 0, 0), 3)
	if err != nil || len(next) != 0 {
		t.Fatalf("Next after until = %v, %v; want none", starts(next), err)
	}
}

// S-M5: occurrences longer than the gap to the next one overlap; Expand
// returns each (readers union them), including one already running at from.
func TestExpand_OverlappingOccurrences(t *testing.T) {
	r := mustRule(t, Rule{
		Weekdays:    []time.Weekday{time.Monday, time.Tuesday},
		StartMinute: 12 * 60,
		Duration:    30 * time.Hour,
		Timezone:    "UTC",
	})
	occ, err := r.Expand(utc(2026, 6, 2, 13, 0), utc(2026, 6, 2, 14, 0))
	if err != nil {
		t.Fatal(err)
	}
	// Monday's runs until Tuesday 18:00, overlapping Tuesday's from 12:00.
	assertStarts(t, occ, utc(2026, 6, 1, 12, 0), utc(2026, 6, 2, 12, 0))
}

func TestNext_SpansChunksInOrder(t *testing.T) {
	r := mustRule(t, Rule{Weekdays: []time.Weekday{time.Friday}, StartMinute: 22 * 60, Duration: time.Hour, Timezone: "Europe/Paris"})
	after := utc(2026, 1, 1, 0, 0)
	occ, err := r.Next(after, 12)
	if err != nil {
		t.Fatal(err)
	}
	if len(occ) != 12 {
		t.Fatalf("got %d occurrences, want 12", len(occ))
	}
	for i := 1; i < len(occ); i++ {
		if gap := occ[i].Start.Sub(occ[i-1].Start); gap < 167*time.Hour || gap > 169*time.Hour {
			t.Errorf("gap %d = %v, want one week", i, gap)
		}
	}
}

// S-M5: density is bounded by starts per horizon. A weekly rule starts at
// most once a local day, so even a daily rule stays under the bound; the
// check itself refuses a rule over a tighter limit with a clear message.
func TestDensityBound(t *testing.T) {
	daily := Rule{
		Weekdays:    []time.Weekday{0, 1, 2, 3, 4, 5, 6},
		StartMinute: 0,
		Duration:    time.Minute,
		Timezone:    "Europe/Paris",
	}
	daily = mustRule(t, daily)
	// The worst horizon includes a DST change.
	for _, from := range []time.Time{utc(2026, 3, 10, 0, 0), utc(2026, 10, 10, 0, 0)} {
		occ, err := daily.Expand(from, from.Add(Horizon))
		if err != nil {
			t.Fatal(err)
		}
		if len(occ) > MaxStartsPerHorizon || len(occ) < 35 {
			t.Errorf("daily rule from %v: %d starts per horizon", from, len(occ))
		}
	}
	err := checkDensity(daily, 30)
	if err == nil || !strings.Contains(err.Error(), "more than 30 occurrences in 35 days") {
		t.Fatalf("checkDensity(daily, 30) = %v; want a refusal naming the bound", err)
	}
	if err := checkDensity(Rule{Weekdays: []time.Weekday{time.Monday}}, 5); err == nil {
		t.Fatal("one weekday touches 6 days in a 37-day span; a limit of 5 must refuse it")
	}
	if err := checkDensity(Rule{Weekdays: []time.Weekday{time.Monday}}, 6); err != nil {
		t.Fatalf("checkDensity(weekly, 6) = %v", err)
	}
	// Expand enforces the same bound on what it returns.
	if _, err := daily.Expand(utc(2026, 1, 1, 0, 0), utc(2026, 1, 1, 0, 0).Add(Horizon)); errors.Is(err, ErrTooManyOccurrences) {
		t.Fatal("a valid daily rule must expand")
	}
}

func TestNormalize(t *testing.T) {
	ok := Rule{Weekdays: []time.Weekday{4, 2, 2}, StartMinute: 90, Duration: time.Hour, Timezone: " Europe/Paris "}
	n, err := ok.Normalize()
	if err != nil {
		t.Fatal(err)
	}
	if len(n.Weekdays) != 2 || n.Weekdays[0] != 2 || n.Weekdays[1] != 4 || n.Timezone != "Europe/Paris" {
		t.Errorf("normalized = %+v", n)
	}
	if !n.Equal(mustRule(t, Rule{Weekdays: []time.Weekday{2, 4}, StartMinute: 90, Duration: time.Hour, Timezone: "Europe/Paris"})) {
		t.Error("equal rules compare unequal")
	}
	for _, tc := range []struct {
		r    Rule
		want string
	}{
		{Rule{StartMinute: 0, Duration: time.Hour, Timezone: "UTC"}, "at least one weekday"},
		{Rule{Weekdays: []time.Weekday{7}, Duration: time.Hour, Timezone: "UTC"}, "weekdays must be between"},
		{Rule{Weekdays: []time.Weekday{1}, StartMinute: 24 * 60, Duration: time.Hour, Timezone: "UTC"}, "start_time"},
		{Rule{Weekdays: []time.Weekday{1}, Duration: 0, Timezone: "UTC"}, "duration_minutes"},
		{Rule{Weekdays: []time.Weekday{1}, Duration: MaxDuration + time.Minute, Timezone: "UTC"}, "duration_minutes"},
		{Rule{Weekdays: []time.Weekday{1}, Duration: time.Hour}, "timezone is required"},
		{Rule{Weekdays: []time.Weekday{1}, Duration: time.Hour, Timezone: "Mars/Olympus"}, "unknown timezone"},
		{Rule{Weekdays: []time.Weekday{1}, Duration: time.Hour, Timezone: "Local"}, "unknown timezone"},
		{Rule{Weekdays: []time.Weekday{1}, Duration: time.Hour, Timezone: "UTC", Until: "10/06/2026"}, "until must be a date"},
	} {
		if _, err := tc.r.Normalize(); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("Normalize(%+v) = %v; want error containing %q", tc.r, err, tc.want)
		}
	}
}

func TestParseStartTime(t *testing.T) {
	for in, want := range map[string]int{"00:00": 0, "02:30": 150, "23:59": 1439} {
		got, err := ParseStartTime(in)
		if err != nil || got != want {
			t.Errorf("ParseStartTime(%q) = %d, %v; want %d", in, got, err, want)
		}
		if FormatStartTime(want) != in {
			t.Errorf("FormatStartTime(%d) = %q; want %q", want, FormatStartTime(want), in)
		}
	}
	for _, in := range []string{"", "2:30", "24:00", "12:60", "12-30", "ab:cd"} {
		if _, err := ParseStartTime(in); err == nil {
			t.Errorf("ParseStartTime(%q) accepted", in)
		}
	}
}

func TestScheduleSameAs(t *testing.T) {
	r := mustRule(t, Rule{Weekdays: []time.Weekday{1}, StartMinute: 60, Duration: time.Hour, Timezone: "UTC"})
	other := r
	other.Until = "2027-01-01"
	start := utc(2026, 1, 1, 0, 0)
	if !(Schedule{Start: start, End: start.Add(time.Hour)}).SameAs(Schedule{Start: start, End: start.Add(time.Hour)}) {
		t.Error("identical one-offs differ")
	}
	if (Schedule{Start: start, End: start.Add(time.Hour)}).SameAs(Schedule{Repeat: &r}) {
		t.Error("one-off equals repeating")
	}
	// A repeating window's stored first occurrence is informational.
	if !(Schedule{Start: start, Repeat: &r}).SameAs(Schedule{Repeat: &r}) {
		t.Error("same rule differs by stored first occurrence")
	}
	if (Schedule{Repeat: &r}).SameAs(Schedule{Repeat: &other}) {
		t.Error("different until compares equal")
	}
}
