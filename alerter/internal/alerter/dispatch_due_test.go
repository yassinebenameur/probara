package alerter

import (
	"testing"
	"time"
)

func TestDueEvent(t *testing.T) {
	now := time.Date(2026, 2, 4, 12, 0, 0, 0, time.UTC)
	interval := 10 * time.Minute
	active := channelTarget{channel: alertChannel{IsActive: true}, delay: 5 * time.Minute}
	inactive := channelTarget{channel: alertChannel{IsActive: false}}
	immediate := channelTarget{channel: alertChannel{IsActive: true}}

	tests := []struct {
		name     string
		state    *notificationState
		target   channelTarget
		alertAge time.Duration
		interval time.Duration
		want     string
	}{
		{"inactive_channel_never_fires", nil, inactive, time.Hour, interval, ""},
		{"inactive_channel_no_reminder", &notificationState{LastEventType: "created", LastSentAt: now.Add(-time.Hour)}, inactive, time.Hour, interval, ""},
		{"created_immediate_tier", nil, immediate, 0, interval, "created"},
		{"created_before_delay", nil, active, 4 * time.Minute, interval, ""},
		{"created_at_delay", nil, active, 5 * time.Minute, interval, "created"},
		{"created_without_reminders", nil, active, time.Hour, 0, "created"},
		{"fired_reminder_not_due", &notificationState{LastEventType: "created", LastSentAt: now.Add(-5 * time.Minute)}, active, time.Hour, interval, ""},
		{"fired_reminder_due", &notificationState{LastEventType: "created", LastSentAt: now.Add(-10 * time.Minute)}, active, time.Hour, interval, "reminder"},
		{"reminder_after_reminder", &notificationState{LastEventType: "reminder", LastSentAt: now.Add(-15 * time.Minute)}, active, time.Hour, interval, "reminder"},
		{"reminders_disabled", &notificationState{LastEventType: "created", LastSentAt: now.Add(-time.Hour)}, active, time.Hour, 0, ""},
		{"resolved_never_reminds", &notificationState{LastEventType: "resolved", LastSentAt: now.Add(-time.Hour)}, active, time.Hour, interval, ""},
		{"ack_sent_still_reminds", &notificationState{LastEventType: "created", LastSentAt: now.Add(-time.Hour), AcknowledgedSentAt: &now}, active, time.Hour, interval, "reminder"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := dueEvent(now, tc.state, tc.target, tc.alertAge, tc.interval, DispatchGate{})
			if ok != (tc.want != "") || got != tc.want {
				t.Fatalf("dueEvent = (%q, %v), want %q", got, ok, tc.want)
			}
			// The legacy pre-filter must agree with dueEvent whenever the
			// escalation delay is not what holds the event back.
			if tc.target.channel.IsActive && tc.alertAge >= tc.target.delay {
				for _, ev := range []string{"created", "reminder"} {
					if shouldSendNotification(ev, tc.state, now, tc.interval) != (got == ev) {
						t.Fatalf("shouldSendNotification(%q) disagrees with dueEvent = %q", ev, got)
					}
				}
			}
		})
	}
}
