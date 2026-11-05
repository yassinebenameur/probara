package escalation

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func ptr(id uuid.UUID) *uuid.UUID { return &id }

func personStep(pos int, delay time.Duration) Step {
	return Step{Position: pos, Delay: delay, Targets: []Target{{Kind: TargetUser, UserID: ptr(uuid.New())}}}
}

func TestValidate(t *testing.T) {
	ch := uuid.New()
	ok := Policy{Name: "Primary", RepeatAfter: DefaultRepeatAfter, Steps: []Step{
		personStep(0, 0),
		{Position: 1, Delay: 5 * time.Minute, Targets: []Target{{Kind: TargetChannel, ChannelID: &ch, NotifyAfterAck: true}, {Kind: TargetAllAdmins}}},
	}}
	if err := ok.Validate(); err != nil {
		t.Fatalf("valid policy rejected: %v", err)
	}
	cases := map[string]func(p *Policy){
		"name":             func(p *Policy) { p.Name = " " },
		"no steps":         func(p *Policy) { p.Steps = nil },
		"decreasing delay": func(p *Policy) { p.Steps[0].Delay = time.Hour },
		"empty step":       func(p *Policy) { p.Steps[0].Targets = nil },
		"repeat count":     func(p *Policy) { p.RepeatCount = MaxRepeatCount + 1 },
		"repeat after":     func(p *Policy) { p.RepeatAfter = time.Second },
		"user without id":  func(p *Policy) { p.Steps[0].Targets[0].UserID = nil },
		"admins with id":   func(p *Policy) { p.Steps[1].Targets[1].UserID = ptr(uuid.New()) },
		"unknown kind":     func(p *Policy) { p.Steps[0].Targets[0].Kind = "team" },
		"after-ack on person": func(p *Policy) {
			p.Steps[0].Targets[0].NotifyAfterAck = true
		},
		"channel twice": func(p *Policy) {
			p.Steps[0].Targets = append(p.Steps[0].Targets, Target{Kind: TargetChannel, ChannelID: &ch})
		},
		"duplicate target": func(p *Policy) {
			p.Steps[1].Targets = append(p.Steps[1].Targets, Target{Kind: TargetAllAdmins})
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			p := ok
			p.Steps = append([]Step(nil), ok.Steps...)
			for i := range p.Steps {
				p.Steps[i].Targets = append([]Target(nil), ok.Steps[i].Targets...)
			}
			mutate(&p)
			if err := p.Validate(); err == nil {
				t.Fatal("expected a validation error")
			}
		})
	}
}

func TestDueSteps(t *testing.T) {
	ch := uuid.New()
	p := Policy{RepeatCount: 2, RepeatAfter: 10 * time.Minute, Steps: []Step{
		personStep(0, 0),
		// A channel-only step never fires through DueSteps: TargetsSQL pages it.
		{Position: 1, Delay: 5 * time.Minute, Targets: []Target{{Kind: TargetChannel, ChannelID: &ch}}},
		personStep(2, 15*time.Minute),
	}}
	if got := p.CyclePeriod(); got != 25*time.Minute {
		t.Fatalf("CyclePeriod = %v, want 25m", got)
	}
	tests := []struct {
		age     time.Duration
		claimed map[StepKey]bool
		want    []StepKey
	}{
		{age: 0, want: []StepKey{{0, 0}}},
		{age: 14 * time.Minute, claimed: map[StepKey]bool{{0, 0}: true}},
		{age: 15 * time.Minute, claimed: map[StepKey]bool{{0, 0}: true}, want: []StepKey{{0, 2}}},
		// The repeat cycle walks the person steps again after the pause.
		{age: 25 * time.Minute, claimed: map[StepKey]bool{{0, 0}: true, {0, 2}: true}, want: []StepKey{{1, 0}}},
		// Overdue steps (the alerter was down) come back in order.
		{age: 41 * time.Minute, want: []StepKey{{0, 0}, {0, 2}, {1, 0}, {1, 2}}},
		// After the last repeat nothing more is due.
		{age: 10 * time.Hour, claimed: map[StepKey]bool{{0, 0}: true, {0, 2}: true, {1, 0}: true, {1, 2}: true, {2, 0}: true, {2, 2}: true}},
	}
	for _, tt := range tests {
		got := p.DueSteps(tt.age, tt.claimed)
		if !reflect.DeepEqual(got, tt.want) {
			t.Errorf("DueSteps(%v) = %v, want %v", tt.age, got, tt.want)
		}
	}
}

func TestKeys(t *testing.T) {
	if got := PageKey(2, 1, 0); got != "escalation:2:1" {
		t.Fatalf("PageKey = %q", got)
	}
	if got := PageKey(2, 1, 3); got != "escalation:2:1:3" {
		t.Fatalf("PageKey with episode = %q", got)
	}
	if got := NoticeKey(EventResolved, 0); got != "resolved" {
		t.Fatalf("NoticeKey = %q", got)
	}
	if got := NoticeKey(EventAcknowledged, 2); got != "acknowledged:2" {
		t.Fatalf("NoticeKey with episode = %q", got)
	}
	if !strings.HasPrefix(PageKey(0, 0, 0), "escalation:") {
		t.Fatal("page keys must not collide with channel event keys")
	}
}
