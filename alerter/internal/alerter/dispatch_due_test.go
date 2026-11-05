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
	afterAck := channelTarget{channel: alertChannel{IsActive: true}, notifyAfterAck: true}

	open := DispatchGate{AckStops: true}
	acked := DispatchGate{Acknowledged: true, AckStops: true}
	ackedTenantKeepsPaging := DispatchGate{Acknowledged: true, AckStops: false}
	snoozed := DispatchGate{Snoozed: true, AckStops: true}
	resumedRecently := DispatchGate{AckStops: true, ResumedAt: now.Add(-3 * time.Minute)}
	resumedLongAgo := DispatchGate{AckStops: true, ResumedAt: now.Add(-30 * time.Minute)}
	fired := func(ago time.Duration) *notificationState {
		return &notificationState{LastEventType: "created", LastSentAt: now.Add(-ago)}
	}

	tests := []struct {
		name     string
		state    *notificationState
		target   channelTarget
		alertAge time.Duration
		interval time.Duration
		gate     DispatchGate
		want     string
	}{
		{"inactive_channel_never_fires", nil, inactive, time.Hour, interval, open, ""},
		{"inactive_channel_no_reminder", fired(time.Hour), inactive, time.Hour, interval, open, ""},
		{"created_immediate_tier", nil, immediate, 0, interval, open, "created"},
		{"created_before_delay", nil, active, 4 * time.Minute, interval, open, ""},
		{"created_at_delay", nil, active, 5 * time.Minute, interval, open, "created"},
		{"created_without_reminders", nil, active, time.Hour, 0, open, "created"},
		{"fired_reminder_not_due", fired(5 * time.Minute), active, time.Hour, interval, open, ""},
		{"fired_reminder_due", fired(10 * time.Minute), active, time.Hour, interval, open, "reminder"},
		{"reminder_after_reminder", &notificationState{LastEventType: "reminder", LastSentAt: now.Add(-15 * time.Minute)}, active, time.Hour, interval, open, "reminder"},
		{"reminders_disabled", fired(time.Hour), active, time.Hour, 0, open, ""},
		{"resolved_never_reminds", &notificationState{LastEventType: "resolved", LastSentAt: now.Add(-time.Hour)}, active, time.Hour, interval, open, ""},
		{"ack_sent_still_reminds_while_active", &notificationState{LastEventType: "created", LastSentAt: now.Add(-time.Hour), AcknowledgedSentAt: &now}, active, time.Hour, interval, open, "reminder"},

		// Ack gate: an acknowledgement stops unfired tiers and reminders.
		{"ack_stops_unfired_tier", nil, active, time.Hour, interval, acked, ""},
		{"ack_stops_reminder", fired(time.Hour), active, time.Hour, interval, acked, ""},
		{"ack_notify_after_ack_route_tier", nil, afterAck, time.Hour, interval, acked, "created"},
		{"ack_notify_after_ack_route_reminder", fired(time.Hour), afterAck, time.Hour, interval, acked, "reminder"},
		{"ack_tenant_opted_out_keeps_paging", fired(time.Hour), active, time.Hour, interval, ackedTenantKeepsPaging, "reminder"},
		{"ack_tenant_opted_out_tier", nil, active, time.Hour, interval, ackedTenantKeepsPaging, "created"},

		// Snooze mutes every route, notify_after_ack included.
		{"snooze_stops_tier", nil, active, time.Hour, interval, snoozed, ""},
		{"snooze_stops_reminder", fired(time.Hour), afterAck, time.Hour, interval, snoozed, ""},

		// Resume (unack / snooze end): reminders count from it too.
		{"resumed_recently_holds_reminder", fired(time.Hour), active, time.Hour, interval, resumedRecently, ""},
		{"resumed_long_ago_reminds", fired(time.Hour), active, time.Hour, interval, resumedLongAgo, "reminder"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := dueEvent(now, tc.state, tc.target, tc.alertAge, tc.interval, tc.gate)
			if ok != (tc.want != "") || got != tc.want {
				t.Fatalf("dueEvent = (%q, %v), want %q", got, ok, tc.want)
			}
			// The legacy pre-filter must agree with dueEvent whenever the
			// escalation delay is not what holds the event back.
			if tc.target.channel.IsActive && tc.alertAge >= tc.target.delay {
				for _, ev := range []string{"created", "reminder"} {
					if shouldSendNotification(ev, tc.state, now, tc.interval, tc.gate, tc.gate.stopOnAck(tc.target)) != (got == ev) {
						t.Fatalf("shouldSendNotification(%q) disagrees with dueEvent = %q", ev, got)
					}
				}
			}
		})
	}
}

// TestClaimStatusMatchesPreFilter pins the ack/snooze gate of the claim
// (claimStatusAllows, evaluated under the alert row lock) to the in-memory
// pre-filter (shouldSendNotification) and dueEvent: for every open status,
// snooze and route flag, an event whose per-channel state is otherwise due is
// allowed by all three or by none.
func TestClaimStatusMatchesPreFilter(t *testing.T) {
	now := time.Date(2026, 2, 4, 12, 0, 0, 0, time.UTC)
	interval := 10 * time.Minute
	// A state on which every event except "created" is due.
	firedLongAgo := &notificationState{LastEventType: "created", LastSentAt: now.Add(-time.Hour)}

	for _, status := range []string{"active", "acknowledged"} {
		for _, snoozed := range []bool{false, true} {
			for _, ackStops := range []bool{false, true} {
				for _, notifyAfterAck := range []bool{false, true} {
					gate := DispatchGate{Acknowledged: status == "acknowledged", AckStops: ackStops, Snoozed: snoozed}
					target := channelTarget{channel: alertChannel{IsActive: true}, notifyAfterAck: notifyAfterAck}
					stopOnAck := gate.stopOnAck(target)
					for _, ev := range []string{"created", "reminder", "acknowledged", "suspected_cause"} {
						state := firedLongAgo
						if ev == "created" {
							state = nil
						}
						claim := claimStatusAllows(ev, status, snoozed, stopOnAck)
						pre := shouldSendNotification(ev, state, now, interval, gate, stopOnAck)
						if ev == "acknowledged" && status != "acknowledged" {
							// The pre-filter never sees the status for an
							// ack event: the loop selects acked alerts only.
							continue
						}
						if claim != pre {
							t.Fatalf("status=%s snoozed=%v ackStops=%v notifyAfterAck=%v event=%s: claim=%v pre-filter=%v",
								status, snoozed, ackStops, notifyAfterAck, ev, claim, pre)
						}
						if ev == "created" || ev == "reminder" {
							got, ok := dueEvent(now, state, target, time.Hour, interval, gate)
							if (ok && got == ev) != claim {
								t.Fatalf("status=%s snoozed=%v ackStops=%v notifyAfterAck=%v event=%s: dueEvent=%q claim=%v",
									status, snoozed, ackStops, notifyAfterAck, ev, got, claim)
							}
						}
					}
				}
			}
		}
	}

	// Resolves are never held by the gate, and never go to an open alert.
	if !claimStatusAllows("resolved", "resolved", true, true) || claimStatusAllows("resolved", "acknowledged", false, false) {
		t.Fatal("resolved must go to resolved alerts only, regardless of the gate")
	}
	// Suspected-cause follow-ups still reach acknowledged alerts.
	if !claimStatusAllows("suspected_cause", "acknowledged", false, true) {
		t.Fatal("an acknowledged alert must still get its suspected-cause follow-up")
	}
}
