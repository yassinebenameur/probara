// Package oncall resolves on-call schedules: who is on call at an instant
// (OnCallAt) and the shifts over a window (Preview). It is pure (no
// database, no clock) so the API and, later, the alerter page from one rule.
//
// A schedule has a time zone, ordered rotation layers and overrides:
//
//   - A rotation hands off daily or weekly at a local wall-clock time in the
//     schedule's zone, so a 09:00 handoff stays at 09:00 across DST changes
//     (shifts are then 23 or 25 hours long). Its first member is on call from
//     StartAt to the first handoff after it, then the members take turns in
//     order. A rotation is active from StartAt until EndAt (exclusive).
//   - Layers stack: of the rotations active at an instant, the last one in
//     the list wins, so a new layer can take over from a future date.
//   - Overrides win over every rotation. Of overlapping overrides the most
//     recently created wins.
//
// Every interval is half-open, [start, end).
package oncall

import (
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
)

// RotationKind is how often a rotation hands off.
type RotationKind string

const (
	Daily  RotationKind = "daily"
	Weekly RotationKind = "weekly"
)

// Source says what put someone on call.
type Source string

const (
	SourceRotation Source = "rotation"
	SourceOverride Source = "override"
)

// Rotation is one layer of a schedule.
type Rotation struct {
	ID   uuid.UUID
	Kind RotationKind
	// HandoffMinute is the local handoff time, minutes after midnight.
	HandoffMinute int
	// HandoffWeekday is the handoff day of a weekly rotation.
	HandoffWeekday time.Weekday
	StartAt        time.Time
	// EndAt, when set, ends the rotation (exclusive).
	EndAt *time.Time
	// Members take turns in order; a user may appear more than once.
	Members []uuid.UUID
}

// Override puts UserID on call over [Start, End).
type Override struct {
	ID        uuid.UUID
	UserID    uuid.UUID
	Start     time.Time
	End       time.Time
	CreatedAt time.Time
}

// Schedule is the input of the resolver.
type Schedule struct {
	Location  *time.Location
	Rotations []Rotation
	Overrides []Override
}

// Assignment is who is on call and why.
type Assignment struct {
	UserID uuid.UUID
	Source Source
	// RotationID or OverrideID names the layer or override.
	RotationID uuid.UUID
	OverrideID uuid.UUID
}

// Shift is an Assignment over [Start, End).
type Shift struct {
	Assignment
	Start time.Time
	End   time.Time
}

// MaxPreview bounds a Preview window.
const MaxPreview = 93 * 24 * time.Hour

// Validate checks a rotation's static fields.
func (r Rotation) Validate() error {
	switch r.Kind {
	case Daily, Weekly:
	default:
		return fmt.Errorf("rotation kind must be %q or %q", Daily, Weekly)
	}
	if r.HandoffMinute < 0 || r.HandoffMinute >= 24*60 {
		return errors.New("handoff time must be between 00:00 and 23:59")
	}
	if r.Kind == Weekly && (r.HandoffWeekday < time.Sunday || r.HandoffWeekday > time.Saturday) {
		return errors.New("handoff weekday must be 0 (Sunday) to 6 (Saturday)")
	}
	if r.StartAt.IsZero() {
		return errors.New("rotation start is required")
	}
	if r.EndAt != nil && !r.EndAt.After(r.StartAt) {
		return errors.New("rotation end must be after its start")
	}
	return nil
}

func (s Schedule) loc() *time.Location {
	if s.Location == nil {
		return time.UTC
	}
	return s.Location
}

// OnCallAt returns who is on call at t. ok is false when nobody is: no
// override covers t and no rotation with members is active.
func (s Schedule) OnCallAt(t time.Time) (Assignment, bool) {
	var best *Override
	for i := range s.Overrides {
		o := &s.Overrides[i]
		if t.Before(o.Start) || !t.Before(o.End) {
			continue
		}
		if best == nil || o.CreatedAt.After(best.CreatedAt) ||
			(o.CreatedAt.Equal(best.CreatedAt) && o.ID.String() > best.ID.String()) {
			best = o
		}
	}
	if best != nil {
		return Assignment{UserID: best.UserID, Source: SourceOverride, OverrideID: best.ID}, true
	}
	loc := s.loc()
	for i := len(s.Rotations) - 1; i >= 0; i-- {
		r := s.Rotations[i]
		if !r.activeAt(t) {
			continue
		}
		k := r.shiftIndex(t, loc)
		return Assignment{UserID: r.Members[k%len(r.Members)], Source: SourceRotation, RotationID: r.ID}, true
	}
	return Assignment{}, false
}

func (r Rotation) activeAt(t time.Time) bool {
	if len(r.Members) == 0 || t.Before(r.StartAt) {
		return false
	}
	return r.EndAt == nil || t.Before(*r.EndAt)
}

// Preview returns the shifts over [from, to), adjacent equal assignments
// merged and gaps (nobody on call) left out. The window is capped at
// MaxPreview.
func (s Schedule) Preview(from, to time.Time) []Shift {
	if !to.After(from) {
		return nil
	}
	if to.Sub(from) > MaxPreview {
		to = from.Add(MaxPreview)
	}
	loc := s.loc()
	bounds := []time.Time{from, to}
	add := func(t time.Time) {
		if t.After(from) && t.Before(to) {
			bounds = append(bounds, t)
		}
	}
	for _, o := range s.Overrides {
		add(o.Start)
		add(o.End)
	}
	for _, r := range s.Rotations {
		if len(r.Members) == 0 {
			continue
		}
		add(r.StartAt)
		if r.EndAt != nil {
			add(*r.EndAt)
		}
		for _, h := range r.handoffs(from, to, loc) {
			add(h)
		}
	}
	sort.Slice(bounds, func(i, j int) bool { return bounds[i].Before(bounds[j]) })

	var out []Shift
	for i := 0; i+1 < len(bounds); i++ {
		start, end := bounds[i], bounds[i+1]
		if !end.After(start) {
			continue
		}
		a, ok := s.OnCallAt(start)
		if !ok {
			continue
		}
		if n := len(out); n > 0 && out[n-1].Assignment == a && out[n-1].End.Equal(start) {
			out[n-1].End = end
			continue
		}
		out = append(out, Shift{Assignment: a, Start: start, End: end})
	}
	return out
}

// civil is a calendar date, kept as midnight UTC so date arithmetic never
// meets a DST transition.
type civil time.Time

func civilOf(t time.Time, loc *time.Location) civil {
	y, m, d := t.In(loc).Date()
	return civil(time.Date(y, m, d, 0, 0, 0, 0, time.UTC))
}

func (c civil) addDays(n int) civil { return civil(time.Time(c).AddDate(0, 0, n)) }

func (c civil) weekday() time.Weekday { return time.Time(c).Weekday() }

func daysBetween(a, b civil) int {
	return int(time.Time(b).Sub(time.Time(a)).Hours() / 24)
}

func (r Rotation) period() int {
	if r.Kind == Weekly {
		return 7
	}
	return 1
}

// handoffOn is the handoff instant on a local date. A handoff inside a
// spring-forward gap lands at the equivalent instant time.Date picks.
func (r Rotation) handoffOn(c civil, loc *time.Location) time.Time {
	t := time.Time(c)
	return time.Date(t.Year(), t.Month(), t.Day(), r.HandoffMinute/60, r.HandoffMinute%60, 0, 0, loc)
}

// lastHandoff returns the latest handoff at or before t and its local date.
func (r Rotation) lastHandoff(t time.Time, loc *time.Location) (civil, time.Time) {
	c := civilOf(t, loc)
	if r.Kind == Weekly {
		c = c.addDays(-((int(c.weekday()) - int(r.HandoffWeekday) + 7) % 7))
	}
	// Two steps back cover a handoff a DST shift pushed past t's date.
	for range 3 {
		if h := r.handoffOn(c, loc); !h.After(t) {
			return c, h
		}
		c = c.addDays(-r.period())
	}
	return c, r.handoffOn(c, loc)
}

// firstHandoff returns the first handoff strictly after StartAt.
func (r Rotation) firstHandoff(loc *time.Location) (civil, time.Time) {
	c, _ := r.lastHandoff(r.StartAt, loc)
	c = c.addDays(r.period())
	return c, r.handoffOn(c, loc)
}

// shiftIndex counts the handoffs in (StartAt, t]: member index before the
// modulo.
func (r Rotation) shiftIndex(t time.Time, loc *time.Location) int {
	firstDay, first := r.firstHandoff(loc)
	if t.Before(first) {
		return 0
	}
	lastDay, _ := r.lastHandoff(t, loc)
	return 1 + daysBetween(firstDay, lastDay)/r.period()
}

// handoffs lists the handoffs after StartAt that fall in [from, to).
func (r Rotation) handoffs(from, to time.Time, loc *time.Location) []time.Time {
	c, h := r.lastHandoff(from, loc)
	var out []time.Time
	for guard := 0; h.Before(to) && guard < 10000; guard++ {
		if !h.Before(from) && h.After(r.StartAt) {
			out = append(out, h)
		}
		c = c.addDays(r.period())
		h = r.handoffOn(c, loc)
	}
	return out
}
