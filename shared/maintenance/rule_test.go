package maintenance

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func mustLoc(t *testing.T, name string) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(name)
	if err != nil {
		t.Fatal(err)
	}
	return loc
}

func starts(occ []Occurrence, loc *time.Location) []string {
	out := make([]string, len(occ))
	for i, o := range occ {
		out[i] = o.Start.In(loc).Format("2006-01-02 15:04 MST")
	}
	return out
}

func assertStarts(t *testing.T, got []Occurrence, loc *time.Location, want ...string) {
	t.Helper()
	g := starts(got, loc)
	if strings.Join(g, "|") != strings.Join(want, "|") {
		t.Fatalf("starts:\n got  %v\n want %v", g, want)
	}
}

func normalized(t *testing.T, r Rule) Rule {
	t.Helper()
	n, err := r.Normalize()
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	return n
}

func TestExpand_OneOff(t *testing.T) {
	start := time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)
	r := normalized(t, Rule{StartsAt: start, Duration: time.Hour})
	occ, err := Expand(r, start.Add(30*time.Minute), start.Add(48*time.Hour))
	if err != nil || len(occ) != 1 || !occ[0].Start.Equal(start) || !occ[0].End.Equal(start.Add(time.Hour)) {
		t.Fatalf("one-off overlapping from: %v %v", occ, err)
	}
	occ, _ = Expand(r, start.Add(time.Hour), start.Add(48*time.Hour))
	if len(occ) != 0 {
		t.Fatalf("one-off that ended at from must not be returned: %v", occ)
	}
}

// Europe/Paris springs forward on 2026-03-29 (02:00 CET -> 03:00 CEST) and
// falls back on 2026-10-25 (03:00 CEST -> 02:00 CET).
func TestExpand_DailyAcrossParisDST(t *testing.T) {
	paris := mustLoc(t, "Europe/Paris")
	r := normalized(t, Rule{
		StartsAt:  time.Date(2026, 3, 27, 2, 30, 0, 0, paris),
		Duration:  30 * time.Minute,
		Frequency: FrequencyDaily,
		Timezone:  "Europe/Paris",
	})
	occ, err := Expand(r, time.Date(2026, 3, 27, 0, 0, 0, 0, paris), time.Date(2026, 3, 31, 0, 0, 0, 0, paris))
	if err != nil {
		t.Fatal(err)
	}
	// 02:30 does not exist on the 29th: it shifts forward by the gap.
	assertStarts(t, occ, paris,
		"2026-03-27 02:30 CET", "2026-03-28 02:30 CET", "2026-03-29 03:30 CEST", "2026-03-30 02:30 CEST")

	r.StartsAt = time.Date(2026, 10, 24, 2, 30, 0, 0, paris)
	occ, err = Expand(r, time.Date(2026, 10, 24, 0, 0, 0, 0, paris), time.Date(2026, 10, 27, 0, 0, 0, 0, paris))
	if err != nil {
		t.Fatal(err)
	}
	// 02:30 happens twice on the 25th: only the first instance runs.
	assertStarts(t, occ, paris, "2026-10-24 02:30 CEST", "2026-10-25 02:30 CEST", "2026-10-26 02:30 CET")
	if got := occ[1].Start.UTC(); !got.Equal(time.Date(2026, 10, 25, 0, 30, 0, 0, time.UTC)) {
		t.Fatalf("overlap must take the first instance, got %v", got)
	}
}

func TestExpand_DailyInterval(t *testing.T) {
	r := normalized(t, Rule{
		StartsAt:  time.Date(2026, 1, 1, 22, 0, 0, 0, time.UTC),
		Duration:  2 * time.Hour,
		Frequency: FrequencyDaily,
		Interval:  3,
	})
	occ, err := Expand(r, time.Date(2026, 1, 5, 0, 0, 0, 0, time.UTC), time.Date(2026, 1, 11, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	assertStarts(t, occ, time.UTC, "2026-01-07 22:00 UTC", "2026-01-10 22:00 UTC")

	// An occurrence that started before from but is still running overlaps it.
	occ, _ = Expand(r, time.Date(2026, 1, 7, 23, 0, 0, 0, time.UTC), time.Date(2026, 1, 8, 0, 0, 0, 0, time.UTC))
	assertStarts(t, occ, time.UTC, "2026-01-07 22:00 UTC")
}

func TestExpand_WeeklyWeekdaysAndInterval(t *testing.T) {
	ny := mustLoc(t, "America/New_York")
	// Tuesday 2026-01-06, every 2 weeks on Mon/Wed/Fri: the anchor week's
	// Monday precedes StartsAt, so the first occurrence is Wednesday.
	r := normalized(t, Rule{
		StartsAt:  time.Date(2026, 1, 6, 23, 0, 0, 0, ny),
		Duration:  time.Hour,
		Frequency: FrequencyWeekly,
		Interval:  2,
		Weekdays:  []time.Weekday{time.Friday, time.Monday, time.Wednesday, time.Monday},
		Timezone:  "America/New_York",
	})
	if len(r.Weekdays) != 3 || r.Weekdays[0] != time.Monday {
		t.Fatalf("weekdays not normalized: %v", r.Weekdays)
	}
	occ, err := Expand(r, time.Date(2026, 1, 1, 0, 0, 0, 0, ny), time.Date(2026, 1, 24, 0, 0, 0, 0, ny))
	if err != nil {
		t.Fatal(err)
	}
	assertStarts(t, occ, ny,
		"2026-01-07 23:00 EST", "2026-01-09 23:00 EST",
		"2026-01-19 23:00 EST", "2026-01-21 23:00 EST", "2026-01-23 23:00 EST")

	// Default weekday is the anchor's own.
	d := normalized(t, Rule{StartsAt: r.StartsAt, Duration: time.Hour, Frequency: FrequencyWeekly, Timezone: "America/New_York"})
	if len(d.Weekdays) != 1 || d.Weekdays[0] != time.Tuesday {
		t.Fatalf("default weekday: %v", d.Weekdays)
	}
}

func TestExpand_MonthlyDay31ClampsToLastDay(t *testing.T) {
	r := normalized(t, Rule{
		StartsAt:  time.Date(2026, 1, 31, 1, 0, 0, 0, time.UTC),
		Duration:  time.Hour,
		Frequency: FrequencyMonthly,
	})
	if r.MonthDay != 31 {
		t.Fatalf("default month day: %d", r.MonthDay)
	}
	occ, err := Expand(r, r.StartsAt, time.Date(2028, 3, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	got := starts(occ, time.UTC)
	want := map[int]string{0: "2026-01-31 01:00 UTC", 1: "2026-02-28 01:00 UTC", 2: "2026-03-31 01:00 UTC", 3: "2026-04-30 01:00 UTC", 25: "2028-02-29 01:00 UTC"}
	if len(got) != 26 {
		t.Fatalf("want one occurrence per month (26), got %d: %v", len(got), got)
	}
	for i, w := range want {
		if got[i] != w {
			t.Fatalf("occurrence %d = %s, want %s", i, got[i], w)
		}
	}

	q := normalized(t, Rule{StartsAt: r.StartsAt, Duration: time.Hour, Frequency: FrequencyMonthly, Interval: 3, MonthDay: 15})
	occ, _ = Expand(q, q.StartsAt, time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC))
	assertStarts(t, occ, time.UTC, "2026-04-15 01:00 UTC", "2026-07-15 01:00 UTC", "2026-10-15 01:00 UTC")
}

func TestExpand_Cron(t *testing.T) {
	paris := mustLoc(t, "Europe/Paris")
	r := normalized(t, Rule{
		StartsAt:  time.Date(2026, 3, 1, 0, 0, 0, 0, paris),
		Duration:  15 * time.Minute,
		Frequency: FrequencyCron,
		Cron:      "30  2 * *   sun",
		Timezone:  "Europe/Paris",
	})
	if r.Cron != "30 2 * * sun" {
		t.Fatalf("cron not normalized: %q", r.Cron)
	}
	occ, err := Expand(r, r.StartsAt, time.Date(2026, 4, 6, 0, 0, 0, 0, paris))
	if err != nil {
		t.Fatal(err)
	}
	assertStarts(t, occ, paris,
		"2026-03-01 02:30 CET", "2026-03-08 02:30 CET", "2026-03-15 02:30 CET", "2026-03-22 02:30 CET",
		"2026-03-29 03:30 CEST", "2026-04-05 02:30 CEST")

	// A gap-shifted time that lands on another match runs once.
	g := normalized(t, Rule{StartsAt: time.Date(2026, 3, 29, 0, 0, 0, 0, paris), Duration: time.Minute,
		Frequency: FrequencyCron, Cron: "30 2,3 29 3 *", Timezone: "Europe/Paris"})
	occ, _ = Expand(g, g.StartsAt, g.StartsAt.Add(24*time.Hour))
	assertStarts(t, occ, paris, "2026-03-29 03:30 CEST")

	// Day-of-month and day-of-week both restricted: either matches.
	o := normalized(t, Rule{StartsAt: time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC), Duration: time.Minute,
		Frequency: FrequencyCron, Cron: "0 0 1 * mon"})
	occ, _ = Expand(o, o.StartsAt, time.Date(2026, 6, 16, 0, 0, 0, 0, time.UTC))
	assertStarts(t, occ, time.UTC, "2026-06-01 00:00 UTC", "2026-06-08 00:00 UTC", "2026-06-15 00:00 UTC")
}

func TestExpand_UntilAndStartsAt(t *testing.T) {
	until := time.Date(2026, 1, 3, 9, 0, 0, 0, time.UTC)
	r := normalized(t, Rule{
		StartsAt:  time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC),
		Duration:  time.Hour,
		Frequency: FrequencyDaily,
		Until:     &until,
	})
	occ, _ := Expand(r, time.Date(2025, 12, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC))
	assertStarts(t, occ, time.UTC, "2026-01-01 09:00 UTC", "2026-01-02 09:00 UTC", "2026-01-03 09:00 UTC")
}

func TestNormalize_Rejects(t *testing.T) {
	base := Rule{StartsAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), Duration: time.Hour}
	past := base.StartsAt.Add(-time.Hour)
	cases := map[string]Rule{
		"zero duration":     {StartsAt: base.StartsAt},
		"bad timezone":      {StartsAt: base.StartsAt, Duration: time.Hour, Frequency: FrequencyDaily, Timezone: "Mars/Olympus"},
		"bad frequency":     {StartsAt: base.StartsAt, Duration: time.Hour, Frequency: "hourly"},
		"weekdays on daily": {StartsAt: base.StartsAt, Duration: time.Hour, Frequency: FrequencyDaily, Weekdays: []time.Weekday{1}},
		"bad weekday":       {StartsAt: base.StartsAt, Duration: time.Hour, Frequency: FrequencyWeekly, Weekdays: []time.Weekday{7}},
		"month day 32":      {StartsAt: base.StartsAt, Duration: time.Hour, Frequency: FrequencyMonthly, MonthDay: 32},
		"bad cron":          {StartsAt: base.StartsAt, Duration: time.Hour, Frequency: FrequencyCron, Cron: "61 * * * *"},
		"cron every":        {StartsAt: base.StartsAt, Duration: time.Hour, Frequency: FrequencyCron, Cron: "@every 1h"},
		"cron tz":           {StartsAt: base.StartsAt, Duration: time.Hour, Frequency: FrequencyCron, Cron: "CRON_TZ=UTC 0 * * * *"},
		"until on one-off":  {StartsAt: base.StartsAt, Duration: time.Hour, Until: &past},
		"until before":      {StartsAt: base.StartsAt, Duration: time.Hour, Frequency: FrequencyDaily, Until: &past},
		"interval 0 cron":   {StartsAt: base.StartsAt, Duration: time.Hour, Frequency: FrequencyCron, Cron: "0 * * * *", Interval: 2},
	}
	for name, r := range cases {
		if _, err := r.Normalize(); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
	dense := Rule{StartsAt: base.StartsAt, Duration: time.Minute, Frequency: FrequencyCron, Cron: "*/2 * * * *"}
	if _, err := dense.Normalize(); !errors.Is(err, ErrTooManyOccurrences) {
		t.Fatalf("dense cron: %v", err)
	}
	hourly := Rule{StartsAt: base.StartsAt, Duration: time.Minute, Frequency: FrequencyCron, Cron: "0 * * * *"}
	if _, err := hourly.Normalize(); err != nil {
		t.Fatalf("hourly cron must be allowed: %v", err)
	}
}

func TestNext(t *testing.T) {
	r := normalized(t, Rule{
		StartsAt:  time.Date(2026, 1, 1, 3, 0, 0, 0, time.UTC),
		Duration:  time.Hour,
		Frequency: FrequencyCron,
		Cron:      "0 3 1 */4 *", // Jan, May, Sep: sparser than one horizon
	})
	occ, err := Next(r, time.Date(2026, 1, 1, 3, 30, 0, 0, time.UTC), 3)
	if err != nil {
		t.Fatal(err)
	}
	assertStarts(t, occ, time.UTC, "2026-01-01 03:00 UTC", "2026-05-01 03:00 UTC", "2026-09-01 03:00 UTC")

	one := normalized(t, Rule{StartsAt: time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC), Duration: time.Hour})
	occ, _ = Next(one, time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), 5)
	if len(occ) != 1 {
		t.Fatalf("one-off beyond the horizon: %v", occ)
	}
}
