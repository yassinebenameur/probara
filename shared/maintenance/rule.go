package maintenance

// Weekly recurrence rules (docs/state-semantics.md S-M5). A repeating window
// runs on chosen weekdays at a local start time in an IANA timezone, for a
// fixed duration, optionally until a local date. Rule.Expand is the one place
// a rule becomes occurrences: the API (create, edit, preview) and the
// scheduler's horizon roller both call it, so they never disagree about when
// a window runs.
//
// Wall-clock times are read in the rule's timezone, so "Tuesday 02:00
// Europe/Paris" stays at 02:00 local across daylight-saving changes:
//   - a start time that a spring-forward gap skips runs at the next valid
//     instant, the moment the gap ends (02:30 on the Paris spring-forward
//     night runs at 03:00 CEST);
//   - a start time that a fall-back overlap repeats runs once, at its first
//     instance (02:30 on the Paris fall-back night runs at 02:30 CEST);
//   - the duration is elapsed time: a 90-minute occurrence lasts 90 minutes
//     even when a clock change falls inside it.
// At most one occurrence starts per local day. Occurrences longer than the
// gap to the next one overlap; every reader unions them (S-M3), so overlap
// never counts twice.

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata" // rules name IANA zones; never depend on the host's zoneinfo
)

const (
	// Horizon is how far ahead occurrences are materialized. The API expands
	// this far on create and edit, and the scheduler rolls it forward.
	Horizon = 35 * 24 * time.Hour
	// MaxDuration bounds one occurrence of a repeating window. A weekly rule
	// with a longer duration never stops covering; that is a one-off window.
	MaxDuration = 7 * 24 * time.Hour
	// MaxStartsPerHorizon bounds a rule's density: a rule that could start
	// more occurrences than this in some Horizon-long span is refused
	// (checkDensity), and Expand refuses to produce more (ErrTooManyOccurrences).
	MaxStartsPerHorizon = 40
	// searchLimit is how far Next looks ahead for occurrences.
	searchLimit = 400 * 24 * time.Hour
	dateLayout  = "2006-01-02"
)

// ErrTooManyOccurrences is returned for a rule that starts more than
// MaxStartsPerHorizon occurrences in a horizon.
var ErrTooManyOccurrences = fmt.Errorf("the recurrence starts more than %d occurrences in %d days",
	MaxStartsPerHorizon, int(Horizon/(24*time.Hour)))

// Rule is a weekly maintenance schedule.
type Rule struct {
	// Weekdays the window runs on (0 = Sunday ... 6 = Saturday); all seven
	// is daily.
	Weekdays []time.Weekday
	// StartMinute is the local start time, in minutes after midnight.
	StartMinute int
	// Duration is each occurrence's elapsed length.
	Duration time.Duration
	// Timezone is the IANA zone the weekdays and start time are read in.
	Timezone string
	// Until, when set (YYYY-MM-DD), is the last local date an occurrence
	// may start on.
	Until string
}

// Occurrence is one concrete [Start, End) maintenance span.
type Occurrence struct {
	Start time.Time
	End   time.Time
}

// ParseStartTime reads a 24-hour "HH:MM" local time as minutes after
// midnight.
func ParseStartTime(s string) (int, error) {
	bad := errors.New("start_time must be a 24-hour time, HH:MM")
	hh, mm, ok := strings.Cut(strings.TrimSpace(s), ":")
	if !ok || len(hh) != 2 || len(mm) != 2 {
		return 0, bad
	}
	h, err1 := strconv.Atoi(hh)
	m, err2 := strconv.Atoi(mm)
	if err1 != nil || err2 != nil || h < 0 || h > 23 || m < 0 || m > 59 {
		return 0, bad
	}
	return h*60 + m, nil
}

// FormatStartTime renders minutes after midnight as "HH:MM".
func FormatStartTime(minute int) string {
	return fmt.Sprintf("%02d:%02d", minute/60, minute%60)
}

// Normalize validates the rule and returns it in canonical form (weekdays
// sorted and deduplicated, fields trimmed). Stored rules are always
// normalized, so Equal can compare them field by field.
func (r Rule) Normalize() (Rule, error) {
	if len(r.Weekdays) == 0 {
		return r, errors.New("a repeating window needs at least one weekday")
	}
	seen := map[time.Weekday]bool{}
	days := make([]time.Weekday, 0, len(r.Weekdays))
	for _, d := range r.Weekdays {
		if d < time.Sunday || d > time.Saturday {
			return r, errors.New("weekdays must be between 0 (Sunday) and 6 (Saturday)")
		}
		if !seen[d] {
			seen[d] = true
			days = append(days, d)
		}
	}
	sort.Slice(days, func(i, j int) bool { return days[i] < days[j] })
	r.Weekdays = days
	if r.StartMinute < 0 || r.StartMinute >= 24*60 {
		return r, errors.New("start_time must be a 24-hour time, HH:MM")
	}
	if r.Duration < time.Minute || r.Duration > MaxDuration || r.Duration%time.Minute != 0 {
		return r, fmt.Errorf("duration_minutes must be between 1 and %d", int(MaxDuration/time.Minute))
	}
	r.Timezone = strings.TrimSpace(r.Timezone)
	if r.Timezone == "" {
		return r, errors.New("timezone is required for a repeating window")
	}
	if _, err := loadZone(r.Timezone); err != nil {
		return r, err
	}
	r.Until = strings.TrimSpace(r.Until)
	if r.Until != "" {
		if _, err := time.Parse(dateLayout, r.Until); err != nil {
			return r, errors.New("until must be a date, YYYY-MM-DD")
		}
	}
	if err := checkDensity(r, MaxStartsPerHorizon); err != nil {
		return r, err
	}
	return r, nil
}

// Equal reports whether two normalized rules describe the same schedule.
func (r Rule) Equal(o Rule) bool {
	if r.StartMinute != o.StartMinute || r.Duration != o.Duration ||
		r.Timezone != o.Timezone || r.Until != o.Until || len(r.Weekdays) != len(o.Weekdays) {
		return false
	}
	for i := range r.Weekdays {
		if r.Weekdays[i] != o.Weekdays[i] {
			return false
		}
	}
	return true
}

func loadZone(name string) (*time.Location, error) {
	// "Local" would follow whatever host runs the code.
	if strings.EqualFold(name, "local") {
		return nil, fmt.Errorf("unknown timezone %q", name)
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return nil, fmt.Errorf("unknown timezone %q", name)
	}
	return loc, nil
}

// densitySpanDays is how many local calendar days a Horizon-long span can
// touch: the whole days, a partial day at each end, and one more for a
// DST-shortened day inside it.
const densitySpanDays = int(Horizon/(24*time.Hour)) + 2

// checkDensity refuses a rule that could start more than limit occurrences
// in some Horizon-long span, wherever the span falls. A weekly rule starts
// at most once per local day, so its worst case is the most matching
// weekdays in densitySpanDays consecutive days. It is the bound Expand
// enforces too, so a rule Normalize accepts always expands.
func checkDensity(r Rule, limit int) error {
	match := map[time.Weekday]bool{}
	for _, d := range r.Weekdays {
		match[d] = true
	}
	worst := 0
	for first := time.Sunday; first <= time.Saturday; first++ {
		n := 0
		for i := 0; i < densitySpanDays; i++ {
			if match[(first+time.Weekday(i))%7] {
				n++
			}
		}
		if n > worst {
			worst = n
		}
	}
	if worst > limit {
		return fmt.Errorf("the recurrence starts more than %d occurrences in %d days",
			limit, int(Horizon/(24*time.Hour)))
	}
	return nil
}

// Expand returns the rule's occurrences that overlap [from, to) (End > from
// and Start < to), ordered by start. It is pure: same rule and range, same
// answer. It reads only a normalized rule's fields and returns
// ErrTooManyOccurrences rather than an unbounded list.
func (r Rule) Expand(from, to time.Time) ([]Occurrence, error) {
	if !to.After(from) || r.Duration <= 0 {
		return nil, nil
	}
	loc, err := loadZone(r.Timezone)
	if err != nil {
		return nil, err
	}
	match := map[time.Weekday]bool{}
	for _, d := range r.Weekdays {
		match[d] = true
	}
	// Candidate local days: an occurrence overlapping from may have started
	// up to Duration earlier; one extra day each side absorbs zone offsets
	// and a start shifted across midnight by a gap.
	first := dayNumber(from.Add(-r.Duration).In(loc)) - 1
	last := dayNumber(to.In(loc)) + 1
	if r.Until != "" {
		u, err := time.Parse(dateLayout, r.Until)
		if err != nil {
			return nil, errors.New("until must be a date, YYYY-MM-DD")
		}
		if ud := dayNumber(u); ud < last {
			last = ud
		}
	}
	var out []Occurrence
	started := 0
	for d := first; d <= last; d++ {
		if !match[dayWeekday(d)] {
			continue
		}
		y, m, dd := dayDate(d)
		s := wallTime(y, m, dd, r.StartMinute, loc)
		e := s.Add(r.Duration)
		if !e.After(from) || !s.Before(to) {
			continue
		}
		if len(out) > 0 && !s.After(out[len(out)-1].Start) {
			continue // never two starts at one instant
		}
		out = append(out, Occurrence{Start: s, End: e})
		if !s.Before(from) {
			started++
		}
	}
	// The cap counts starts inside [from, to), the quantity checkDensity
	// bounds; an occurrence still running from before from is no new row.
	horizons := int((to.Sub(from) + Horizon - 1) / Horizon)
	if started > MaxStartsPerHorizon*horizons {
		return nil, ErrTooManyOccurrences
	}
	return out, nil
}

// Next returns up to n of the rule's occurrences that end after `after`, in
// order, looking at most searchLimit ahead. It backs the preview endpoint
// and the "is there anything left" check on save.
func (r Rule) Next(after time.Time, n int) ([]Occurrence, error) {
	if n <= 0 {
		return nil, nil
	}
	var untilEnd time.Time
	if r.Until != "" {
		u, err := time.Parse(dateLayout, r.Until)
		if err != nil {
			return nil, errors.New("until must be a date, YYYY-MM-DD")
		}
		// Past the until date in every zone: nothing can start later.
		untilEnd = u.Add(48 * time.Hour)
	}
	var out []Occurrence
	limit := after.Add(searchLimit)
	for from := after; len(out) < n && from.Before(limit); from = from.Add(Horizon) {
		occ, err := r.Expand(from, from.Add(Horizon))
		if err != nil {
			return nil, err
		}
		for _, o := range occ {
			if len(out) > 0 && !o.Start.After(out[len(out)-1].Start) {
				continue // returned by the previous chunk
			}
			out = append(out, o)
			if len(out) == n {
				break
			}
		}
		if !untilEnd.IsZero() && from.Add(Horizon).After(untilEnd) {
			break
		}
	}
	return out, nil
}

// wallTime is the instant a local wall-clock time names in loc, resolving
// daylight saving the way the package doc states: a time inside a gap runs
// at the end of the gap, a repeated time at its first instance.
func wallTime(y int, m time.Month, d, minute int, loc *time.Location) time.Time {
	naive := time.Date(y, m, d, minute/60, minute%60, 0, 0, time.UTC)
	_, before := naive.Add(-24 * time.Hour).In(loc).Zone()
	var offsets []int
	for _, probe := range []time.Duration{-24 * time.Hour, 0, 24 * time.Hour} {
		_, off := naive.Add(probe).In(loc).Zone()
		dup := false
		for _, o := range offsets {
			dup = dup || o == off
		}
		if !dup {
			offsets = append(offsets, off)
		}
	}
	var best time.Time
	found := false
	for _, off := range offsets {
		inst := naive.Add(-time.Duration(off) * time.Second)
		l := inst.In(loc)
		ly, lm, ld := l.Date()
		if ly == y && lm == m && ld == d && l.Hour()*60+l.Minute() == minute && l.Second() == 0 {
			if !found || inst.Before(best) {
				best, found = inst, true
			}
		}
	}
	if found {
		return best
	}
	// The wall time falls in a gap. Read with the offset in force before
	// the gap it lands past it; the zone period it lands in began exactly
	// when the gap ended, the next instant that exists.
	past := naive.Add(-time.Duration(before) * time.Second).In(loc)
	if start, _ := past.ZoneBounds(); !start.IsZero() {
		return start
	}
	return past
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
