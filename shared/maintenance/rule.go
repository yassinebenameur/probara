package maintenance

// Recurrence rules (docs/state-semantics.md S-M5). A maintenance window is a
// rule; what covers a monitor is its materialized occurrences
// (maintenance_window_occurrences). Expand is the one place a rule becomes
// occurrences: the API (create/update/preview) and the scheduler's horizon
// roller both call it, so they can never disagree about when a window runs.
//
// Wall-clock rules (daily, weekly, monthly, cron) are evaluated in the rule's
// IANA timezone:
//   - a wall time that a DST gap skips shifts forward by the gap (02:30 on
//     the spring-forward night in Europe/Paris runs at 03:30 CEST);
//   - a wall time that a DST overlap repeats runs once, at its first
//     instance (02:30 on the fall-back night runs at 02:30 CEST);
//   - a monthly day past the end of a shorter month clamps to the month's
//     last day (day 31 runs on Feb 28/29, Apr 30, ...), so "the 31st" means
//     "the last day" and no month is skipped;
//   - the duration is elapsed time: a 2h occurrence that straddles a DST
//     change still lasts 2h.

import (
	"errors"
	"fmt"
	"math/bits"
	"sort"
	"strings"
	"time"
	_ "time/tzdata" // rules name IANA zones; never depend on the host's zoneinfo

	"github.com/robfig/cron/v3"
)

// Frequency is a rule's recurrence kind (maintenance_windows.recurrence).
type Frequency string

const (
	FrequencyNone    Frequency = "none" // a one-off window: exactly one occurrence
	FrequencyDaily   Frequency = "daily"
	FrequencyWeekly  Frequency = "weekly"
	FrequencyMonthly Frequency = "monthly"
	FrequencyCron    Frequency = "cron"
)

const (
	// Horizon is how far ahead occurrences are materialized. The API expands
	// this far on create/update and the scheduler rolls it forward.
	Horizon = 35 * 24 * time.Hour
	// MaxOccurrencesPerHorizon bounds a rule's density: a rule that could
	// start more occurrences than this within any Horizon-long span is
	// rejected (checkDensity), so a cron like "* * * * *" cannot flood the
	// occurrence table.
	MaxOccurrencesPerHorizon = 1000
	// MaxInterval bounds interval_n ("every N days/weeks/months").
	MaxInterval = 366
	// MaxRecurringDuration bounds one occurrence of a recurring rule.
	MaxRecurringDuration = 366 * 24 * time.Hour
	// previewLookahead is how far Next searches for occurrences.
	previewLookahead = 400 * 24 * time.Hour
)

// ErrTooManyOccurrences is returned when a rule fires more often than
// MaxOccurrencesPerHorizon allows.
var ErrTooManyOccurrences = fmt.Errorf("recurrence fires too often: more than %d occurrences in %d days", MaxOccurrencesPerHorizon, int(Horizon/(24*time.Hour)))

// Rule is a maintenance window's schedule. StartsAt/Duration describe the
// template occurrence: for calendar frequencies StartsAt's wall clock in
// Timezone is every occurrence's time of day and its date anchors the
// interval count; for cron it is only a lower bound. No occurrence starts
// before StartsAt or after Until.
type Rule struct {
	StartsAt  time.Time
	Duration  time.Duration
	Frequency Frequency
	Interval  int            // every N days/weeks/months (calendar frequencies)
	Weekdays  []time.Weekday // weekly: days of the week (0 = Sunday)
	MonthDay  int            // monthly: 1-31, clamped to short months
	Cron      string         // cron: standard 5-field expression
	Timezone  string         // IANA zone; "" means UTC
	Until     *time.Time     // no occurrence starts after this instant
}

// Occurrence is one concrete [Start, End) maintenance span.
type Occurrence struct {
	Start time.Time `json:"starts_at"`
	End   time.Time `json:"ends_at"`
}

// Normalize validates the rule and fills its defaults: interval 1, timezone
// UTC, weekly on StartsAt's weekday, monthly on StartsAt's day. Stored rules
// are always normalized, so reading one back never re-derives a default.
func (r Rule) Normalize() (Rule, error) {
	if r.Frequency == "" {
		r.Frequency = FrequencyNone
	}
	if r.Duration <= 0 {
		return r, errors.New("ends_at must be after starts_at")
	}
	if r.Timezone == "" {
		r.Timezone = "UTC"
	}
	loc, err := time.LoadLocation(r.Timezone)
	if err != nil || strings.EqualFold(r.Timezone, "local") {
		return r, fmt.Errorf("unknown timezone %q", r.Timezone)
	}
	if r.Interval == 0 {
		r.Interval = 1
	}
	if r.Interval < 1 || r.Interval > MaxInterval {
		return r, fmt.Errorf("recurrence interval must be between 1 and %d", MaxInterval)
	}
	if r.Frequency != FrequencyWeekly && len(r.Weekdays) > 0 {
		return r, errors.New("weekdays apply to weekly recurrence only")
	}
	if r.Frequency != FrequencyMonthly && r.MonthDay != 0 {
		return r, errors.New("month_day applies to monthly recurrence only")
	}
	if r.Frequency != FrequencyCron && strings.TrimSpace(r.Cron) != "" {
		return r, errors.New("cron applies to cron recurrence only")
	}
	local := r.StartsAt.In(loc)
	switch r.Frequency {
	case FrequencyNone:
		if r.Interval != 1 {
			return r, errors.New("recurrence interval applies to repeating windows only")
		}
		if r.Until != nil {
			return r, errors.New("recurrence until applies to repeating windows only")
		}
		return r, nil
	case FrequencyDaily:
	case FrequencyWeekly:
		days, err := normalizeWeekdays(r.Weekdays)
		if err != nil {
			return r, err
		}
		if len(days) == 0 {
			days = []time.Weekday{local.Weekday()}
		}
		r.Weekdays = days
	case FrequencyMonthly:
		if r.MonthDay == 0 {
			r.MonthDay = local.Day()
		}
		if r.MonthDay < 1 || r.MonthDay > 31 {
			return r, errors.New("month_day must be between 1 and 31")
		}
	case FrequencyCron:
		r.Cron = strings.Join(strings.Fields(r.Cron), " ")
		if r.Interval != 1 {
			return r, errors.New("recurrence interval does not apply to cron recurrence")
		}
		if _, err := parseCron(r.Cron); err != nil {
			return r, err
		}
	default:
		return r, fmt.Errorf("unknown recurrence frequency %q", r.Frequency)
	}
	if r.Duration > MaxRecurringDuration {
		return r, fmt.Errorf("a repeating window lasts at most %d days", int(MaxRecurringDuration/(24*time.Hour)))
	}
	if r.Until != nil && !r.Until.After(r.StartsAt) {
		return r, errors.New("recurrence until must be after starts_at")
	}
	if err := checkDensity(r); err != nil {
		return r, err
	}
	return r, nil
}

// densityDays is how many local calendar days a Horizon-long span can
// touch: 35 whole days plus a partial one at each end, plus one for a
// DST-shortened day inside it.
const densityDays = int(Horizon/(24*time.Hour)) + 2

// gregorianCycleDays is the length of the 400-year Gregorian cycle, after
// which every (month, day of month, weekday) pattern repeats exactly.
const gregorianCycleDays = 146097

// checkDensity rejects a rule that could start more than
// MaxOccurrencesPerHorizon occurrences in some Horizon-long span, wherever
// that span falls. It is the one definition of density: Expand caps the
// starts in [from, to) with the same bound, so a rule Normalize accepts
// always expands, for every from the roller or an edit uses.
//
// Calendar rules start at most one occurrence per day (daily, weekly) or
// per month, so they can never reach the cap. A cron rule starts the same
// number of wall times on every day it matches; its bound is that number
// times the most days it matches in any densityDays-long stretch of the
// Gregorian cycle, an upper bound that holds whatever the start date.
func checkDensity(r Rule) error {
	if r.Frequency != FrequencyCron {
		return nil
	}
	spec, err := parseCron(r.Cron)
	if err != nil {
		return err
	}
	perDay := bits.OnesCount64(spec.Hour&^cronStar) * bits.OnesCount64(spec.Minute&^cronStar)
	if perDay*densityDays <= MaxOccurrencesPerHorizon {
		return nil
	}
	if perDay > MaxOccurrencesPerHorizon {
		return ErrTooManyOccurrences
	}
	// Sliding count of matching days across one full cycle (plus the window
	// length, so stretches that wrap the cycle are counted too).
	match := make([]bool, gregorianCycleDays+densityDays)
	run := 0
	for d := range match {
		_, m, dd := dayDate(int64(d))
		match[d] = spec.Month&(1<<uint(m)) != 0 && cronDayMatches(spec, dd, dayWeekday(int64(d)))
		if match[d] {
			run++
		}
		if d >= densityDays && match[d-densityDays] {
			run--
		}
		if run*perDay > MaxOccurrencesPerHorizon {
			return ErrTooManyOccurrences
		}
	}
	return nil
}

func normalizeWeekdays(in []time.Weekday) ([]time.Weekday, error) {
	seen := map[time.Weekday]bool{}
	out := make([]time.Weekday, 0, len(in))
	for _, d := range in {
		if d < time.Sunday || d > time.Saturday {
			return nil, errors.New("weekdays must be between 0 (Sunday) and 6 (Saturday)")
		}
		if !seen[d] {
			seen[d] = true
			out = append(out, d)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out, nil
}

// cronStar marks a '*' (or '?') field in robfig's bit sets; the day rule
// below depends on it exactly as robfig's own dayMatches does.
const cronStar = uint64(1) << 63

func parseCron(expr string) (*cron.SpecSchedule, error) {
	if expr == "" {
		return nil, errors.New("cron expression is required for cron recurrence")
	}
	if strings.Contains(expr, "TZ=") {
		return nil, errors.New("cron expression must not carry a timezone; use the timezone field")
	}
	sched, err := cron.ParseStandard(expr)
	if err != nil {
		return nil, fmt.Errorf("invalid cron expression: %v", err)
	}
	spec, ok := sched.(*cron.SpecSchedule)
	if !ok {
		return nil, errors.New("invalid cron expression: @every is not supported")
	}
	return spec, nil
}

// Expand returns the rule's occurrences that overlap [from, to) (End > from
// and Start < to), ordered by start. It is pure: same rule and range, same
// answer. It returns ErrTooManyOccurrences instead of an unbounded list.
func Expand(r Rule, from, to time.Time) ([]Occurrence, error) {
	if !to.After(from) || r.Duration <= 0 {
		return nil, nil
	}
	if r.Frequency == "" || r.Frequency == FrequencyNone {
		occ := Occurrence{Start: r.StartsAt, End: r.StartsAt.Add(r.Duration)}
		if occ.End.After(from) && occ.Start.Before(to) {
			return []Occurrence{occ}, nil
		}
		return nil, nil
	}
	tz := r.Timezone
	if tz == "" {
		tz = "UTC"
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return nil, fmt.Errorf("unknown timezone %q", tz)
	}
	var spec *cron.SpecSchedule
	if r.Frequency == FrequencyCron {
		if spec, err = parseCron(r.Cron); err != nil {
			return nil, err
		}
	}
	interval := r.Interval
	if interval < 1 {
		interval = 1
	}

	anchor := r.StartsAt.In(loc)
	anchorDay := dayNumber(anchor)
	hh, mm, ss := anchor.Clock()

	// Candidate local days: an occurrence overlapping from may have started
	// up to Duration earlier; one extra day each side absorbs zone offsets.
	first := dayNumber(from.Add(-r.Duration).In(loc)) - 1
	if first < anchorDay {
		first = anchorDay
	}
	last := dayNumber(to.In(loc)) + 1

	var starts []time.Time
	for d := first; d <= last; d++ {
		y, m, dd := dayDate(d)
		switch r.Frequency {
		case FrequencyDaily:
			if (d-anchorDay)%int64(interval) == 0 {
				starts = append(starts, wallTime(y, m, dd, hh, mm, ss, loc))
			}
		case FrequencyWeekly:
			if weekIndex(d, anchorDay)%int64(interval) == 0 && containsWeekday(r.Weekdays, dayWeekday(d)) {
				starts = append(starts, wallTime(y, m, dd, hh, mm, ss, loc))
			}
		case FrequencyMonthly:
			months := int64((y-anchor.Year())*12 + int(m) - int(anchor.Month()))
			day := r.MonthDay
			if day == 0 {
				day = anchor.Day()
			}
			if months%int64(interval) == 0 && dd == clampDay(y, m, day) {
				starts = append(starts, wallTime(y, m, dd, hh, mm, ss, loc))
			}
		case FrequencyCron:
			if spec.Month&(1<<uint(m)) == 0 || !cronDayMatches(spec, dd, dayWeekday(d)) {
				continue
			}
			for h := 0; h < 24; h++ {
				if spec.Hour&(1<<uint(h)) == 0 {
					continue
				}
				for mi := 0; mi < 60; mi++ {
					if spec.Minute&(1<<uint(mi)) != 0 {
						starts = append(starts, wallTime(y, m, dd, h, mi, 0, loc))
					}
				}
			}
		default:
			return nil, fmt.Errorf("unknown recurrence frequency %q", r.Frequency)
		}
	}

	// A DST gap can shift a wall time onto (or past) a later one: sort and
	// drop duplicates so each instant runs once.
	sort.Slice(starts, func(i, j int) bool { return starts[i].Before(starts[j]) })
	out := make([]Occurrence, 0, len(starts))
	started := 0
	for i, s := range starts {
		if i > 0 && s.Equal(starts[i-1]) {
			continue
		}
		if s.Before(r.StartsAt) || (r.Until != nil && s.After(*r.Until)) {
			continue
		}
		e := s.Add(r.Duration)
		if !e.After(from) || !s.Before(to) {
			continue
		}
		out = append(out, Occurrence{Start: s, End: e})
		if !s.Before(from) {
			started++
		}
	}
	// The cap counts starts in [from, to) only, the quantity checkDensity
	// bounds; occurrences still running from before from (a long duration)
	// are not new rows for any caller.
	horizons := int((to.Sub(from) + Horizon - 1) / Horizon)
	if started > MaxOccurrencesPerHorizon*horizons {
		return nil, ErrTooManyOccurrences
	}
	return out, nil
}

// Next returns up to n of the rule's occurrences that end after `after`, in
// order, searching at most about 400 days past the later of `after` and the
// series start (a sparse cron may yield fewer). It backs the preview endpoint and the "next occurrences" lists.
func Next(r Rule, after time.Time, n int) ([]Occurrence, error) {
	if n <= 0 {
		return nil, nil
	}
	if r.Frequency == "" || r.Frequency == FrequencyNone {
		return upcoming(r, after)
	}
	var out []Occurrence
	from := after
	if r.StartsAt.After(from) {
		from = r.StartsAt // nothing starts earlier; search from the series start
	}
	limit := from.Add(previewLookahead)
	for len(out) < n && from.Before(limit) {
		to := from.Add(Horizon)
		occ, err := Expand(r, from, to)
		if err != nil {
			return nil, err
		}
		for _, o := range occ {
			if len(out) > 0 && !o.Start.After(out[len(out)-1].Start) {
				continue // already returned by the previous chunk
			}
			out = append(out, o)
			if len(out) == n {
				break
			}
		}
		if r.Until != nil && to.After(*r.Until) {
			break
		}
		from = to
	}
	return out, nil
}

// wallTime is the instant a local wall-clock time names in loc, resolving
// DST the way the package doc states: a skipped time shifts forward by the
// gap (it is read with the offset in force before the gap), a repeated time
// takes its first instance.
func wallTime(y int, m time.Month, d, hh, mm, ss int, loc *time.Location) time.Time {
	naive := time.Date(y, m, d, hh, mm, ss, 0, time.UTC)
	_, before := naive.Add(-24 * time.Hour).In(loc).Zone()
	_, after := naive.Add(24 * time.Hour).In(loc).Zone()
	var best time.Time
	found := false
	for _, off := range []int{before, after} {
		inst := naive.Add(-time.Duration(off) * time.Second)
		l := inst.In(loc)
		ly, lm, ld := l.Date()
		lh, lmi, ls := l.Clock()
		if ly == y && lm == m && ld == d && lh == hh && lmi == mm && ls == ss {
			if !found || inst.Before(best) {
				best, found = inst, true
			}
		}
	}
	if found {
		return best
	}
	return naive.Add(-time.Duration(before) * time.Second)
}

// Civil-day arithmetic: day numbers count days since 1970-01-01 (Thursday).
func dayNumber(t time.Time) int64 {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC).Unix() / 86400
}

func dayDate(n int64) (int, time.Month, int) {
	return time.Unix(n*86400, 0).UTC().Date()
}

func dayWeekday(n int64) time.Weekday {
	return time.Weekday(((n % 7) + 7 + 4) % 7)
}

// weekIndex counts ISO (Monday-start) weeks from the anchor's week to day d.
func weekIndex(d, anchor int64) int64 {
	monday := func(n int64) int64 { return n - int64((dayWeekday(n)+6)%7) }
	return (monday(d) - monday(anchor)) / 7
}

func containsWeekday(days []time.Weekday, d time.Weekday) bool {
	for _, x := range days {
		if x == d {
			return true
		}
	}
	return false
}

func clampDay(y int, m time.Month, day int) int {
	last := time.Date(y, m+1, 0, 0, 0, 0, 0, time.UTC).Day()
	if day > last {
		return last
	}
	return day
}

// cronDayMatches is robfig's day rule: when either day field is '*' both
// must match, otherwise either may.
func cronDayMatches(s *cron.SpecSchedule, dom int, dow time.Weekday) bool {
	domMatch := s.Dom&(1<<uint(dom)) != 0
	dowMatch := s.Dow&(1<<uint(dow)) != 0
	if s.Dom&cronStar != 0 || s.Dow&cronStar != 0 {
		return domMatch && dowMatch
	}
	return domMatch || dowMatch
}
