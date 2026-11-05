// Package escalation holds the rules of escalation policies (migration
// 000113), shared by the API (CRUD, validation) and the alerter (paging):
// the policy shape and its limits, when each person step is due, the
// delivery-ledger keys of person sends, and the loaders that turn a step's
// targets into the people and contacts to page.
//
// A policy is an ordered list of steps; each step has a delay (counted from
// when the alert became eligible to page, like a channel escalation tier)
// and targets. Channel targets are plain routes (shared/alertrouting emits
// them through TargetsSQL), so they follow the workspace reminders. Person
// targets — a user, whoever a schedule has on call, every workspace admin —
// are paged by the alerter once per step, and again on each repeat cycle:
// after the last step the policy waits RepeatAfter and walks its person
// steps again, RepeatCount more times. Repeat cycles apply to person
// targets only.
package escalation

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/shared/notifications"
)

// TargetKind is what a step pages.
type TargetKind string

const (
	TargetUser      TargetKind = "user"
	TargetSchedule  TargetKind = "schedule"
	TargetChannel   TargetKind = "channel"
	TargetAllAdmins TargetKind = "all_admins"
)

// Valid reports whether k is a known kind.
func (k TargetKind) Valid() bool {
	switch k {
	case TargetUser, TargetSchedule, TargetChannel, TargetAllAdmins:
		return true
	}
	return false
}

// Person reports whether the alerter pages k itself (everything but a
// channel).
func (k TargetKind) Person() bool { return k.Valid() && k != TargetChannel }

// Limits on a policy.
const (
	MaxSteps          = 10
	MaxTargetsPerStep = 20
	MaxRepeatCount    = 10
	MinRepeatAfter    = time.Minute
	MaxRepeatAfter    = 24 * time.Hour
	MaxStepDelay      = 7 * 24 * time.Hour
	MaxNameLength     = 100
	MaxDescription    = 500
	// DefaultRepeatAfter is the pause before a repeat cycle when unset.
	DefaultRepeatAfter = 30 * time.Minute
)

// Target is one recipient of a step. Exactly one ID is set, matching Kind
// (none for TargetAllAdmins). NotifyAfterAck applies to channel targets
// only: the route keeps its tiers and reminders while acknowledged.
type Target struct {
	Kind           TargetKind
	UserID         *uuid.UUID
	ScheduleID     *uuid.UUID
	ChannelID      *uuid.UUID
	NotifyAfterAck bool
}

// Step is one escalation step.
type Step struct {
	Position int
	Delay    time.Duration
	Targets  []Target
}

// HasPeople reports whether the step has a person target.
func (s Step) HasPeople() bool {
	for _, t := range s.Targets {
		if t.Kind.Person() {
			return true
		}
	}
	return false
}

// Policy is an escalation policy.
type Policy struct {
	ID          uuid.UUID
	TenantID    uuid.UUID
	Name        string
	RepeatCount int
	RepeatAfter time.Duration
	Steps       []Step
}

// Validate checks a policy's static shape: 1 to MaxSteps steps whose delays
// never decrease, 1 to MaxTargetsPerStep targets per step with the ID their
// kind needs, no target twice in a step, no channel in two steps (a channel
// is one route with one delay), and the repeat bounds.
func (p Policy) Validate() error {
	name := strings.TrimSpace(p.Name)
	if name == "" {
		return errors.New("name is required")
	}
	if len([]rune(name)) > MaxNameLength {
		return fmt.Errorf("name must be at most %d characters", MaxNameLength)
	}
	if len(p.Steps) == 0 {
		return errors.New("a policy needs at least one step")
	}
	if len(p.Steps) > MaxSteps {
		return fmt.Errorf("a policy has at most %d steps", MaxSteps)
	}
	if p.RepeatCount < 0 || p.RepeatCount > MaxRepeatCount {
		return fmt.Errorf("repeat_count must be between 0 and %d", MaxRepeatCount)
	}
	if p.RepeatAfter < MinRepeatAfter || p.RepeatAfter > MaxRepeatAfter {
		return fmt.Errorf("repeat_after_seconds must be between %d and %d",
			int(MinRepeatAfter.Seconds()), int(MaxRepeatAfter.Seconds()))
	}
	channels := map[uuid.UUID]int{}
	for i, s := range p.Steps {
		n := i + 1
		if s.Delay < 0 || s.Delay > MaxStepDelay {
			return fmt.Errorf("step %d: delay must be between 0 and %d seconds", n, int(MaxStepDelay.Seconds()))
		}
		if s.Delay%time.Second != 0 {
			return fmt.Errorf("step %d: delay must be whole seconds", n)
		}
		if i > 0 && s.Delay < p.Steps[i-1].Delay {
			return fmt.Errorf("step %d: delay must not be shorter than step %d's", n, i)
		}
		if len(s.Targets) == 0 {
			return fmt.Errorf("step %d: add at least one target", n)
		}
		if len(s.Targets) > MaxTargetsPerStep {
			return fmt.Errorf("step %d: at most %d targets", n, MaxTargetsPerStep)
		}
		seen := map[string]bool{}
		for _, t := range s.Targets {
			key, err := t.key()
			if err != nil {
				return fmt.Errorf("step %d: %w", n, err)
			}
			if seen[key] {
				return fmt.Errorf("step %d: the same target is listed twice", n)
			}
			seen[key] = true
			if t.Kind == TargetChannel {
				if prev, ok := channels[*t.ChannelID]; ok {
					return fmt.Errorf("step %d: a channel can be in one step only (it is already in step %d)", n, prev)
				}
				channels[*t.ChannelID] = n
			}
		}
	}
	return nil
}

// key validates t and returns its identity within a step.
func (t Target) key() (string, error) {
	set := 0
	for _, id := range []*uuid.UUID{t.UserID, t.ScheduleID, t.ChannelID} {
		if id != nil {
			set++
		}
	}
	need := func(id *uuid.UUID, field string) (string, error) {
		if id == nil || *id == uuid.Nil || set != 1 {
			return "", fmt.Errorf("a %s target needs %s (and nothing else)", t.Kind, field)
		}
		return string(t.Kind) + ":" + id.String(), nil
	}
	if t.NotifyAfterAck && t.Kind != TargetChannel {
		return "", errors.New("notify_after_ack applies to channel targets only")
	}
	switch t.Kind {
	case TargetUser:
		return need(t.UserID, "user_id")
	case TargetSchedule:
		return need(t.ScheduleID, "schedule_id")
	case TargetChannel:
		return need(t.ChannelID, "channel_id")
	case TargetAllAdmins:
		if set != 0 {
			return "", errors.New("an all_admins target takes no ID")
		}
		return string(t.Kind), nil
	default:
		return "", fmt.Errorf("unknown target kind %q (user, schedule, channel or all_admins)", t.Kind)
	}
}

// HasPeople reports whether any step pages people.
func (p Policy) HasPeople() bool {
	for _, s := range p.Steps {
		if s.HasPeople() {
			return true
		}
	}
	return false
}

// CyclePeriod is the length of one walk of the policy: the last step's
// delay plus RepeatAfter. Cycle c's step s is due at c*CyclePeriod +
// s.Delay.
func (p Policy) CyclePeriod() time.Duration {
	if len(p.Steps) == 0 {
		return p.RepeatAfter
	}
	return p.Steps[len(p.Steps)-1].Delay + p.RepeatAfter
}

// StepKey names one firing of one step: its position in a repeat cycle.
type StepKey struct {
	Cycle    int
	Position int
}

// DueSteps returns the person steps due once the alert has been paging for
// age, in firing order, leaving out those already claimed. Cycle 0 is the
// first walk; cycles 1..RepeatCount repeat it, person steps only. age is
// measured from the alert's dispatch-eligible time or, when later, the
// point paging resumed after an unacknowledge or a snooze, so lifting a
// mute never releases a burst of overdue steps.
func (p Policy) DueSteps(age time.Duration, claimed map[StepKey]bool) []StepKey {
	var due []StepKey
	period := p.CyclePeriod()
	for c := 0; c <= p.RepeatCount; c++ {
		offset := time.Duration(c) * period
		if age < offset {
			break
		}
		for _, s := range p.Steps {
			if !s.HasPeople() {
				continue
			}
			k := StepKey{Cycle: c, Position: s.Position}
			if age >= offset+s.Delay && !claimed[k] {
				due = append(due, k)
			}
		}
	}
	return due
}

// Step returns the step at position.
func (p Policy) Step(position int) (Step, bool) {
	for _, s := range p.Steps {
		if s.Position == position {
			return s, true
		}
	}
	return Step{}, false
}

// Event names of person sends, as recorded in alert_escalation_pages.event.
const (
	EventPage         = "page"
	EventAcknowledged = "acknowledged"
	EventResolved     = "resolved"
)

// PageKey is the delivery-ledger key of a step's page:
// "escalation:<position>:<cycle>", plus ":<episode>" once the alert has
// relapsed (alerts.relapse_count), like the channel keys
// (notifications.RecipientEventKeyFor). The step is retried until every
// contact has it, and the key lets a retry skip the contacts already
// reached; a reopened alert walks its steps again under fresh keys.
func PageKey(position, cycle, relapseCount int) string {
	key := "escalation:" + strconv.Itoa(position) + ":" + strconv.Itoa(cycle)
	if relapseCount > 0 {
		key += ":" + strconv.Itoa(relapseCount)
	}
	return key
}

// NoticeKey is the ledger key of the acknowledged or resolved notice sent to
// everyone a policy paged in the alert's episode relapseCount.
func NoticeKey(event string, relapseCount int) string {
	return notifications.RecipientEventKeyFor(event, time.Time{}, relapseCount)
}
