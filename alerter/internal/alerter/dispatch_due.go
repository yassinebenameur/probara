package alerter

import "time"

// DispatchGate carries alert-level conditions that can hold back a due
// created/reminder event for every channel of an alert. It is empty today:
// dueEvent's decision depends only on the per-channel state and timing. Later
// gates (for example "acknowledged alerts stop paging") extend this struct and
// dueEvent together, so both open-alert loops pick the rule up at once.
type DispatchGate struct{}

// dueEvent is the single created/reminder due rule for one (alert, channel
// target) pair, shared by dispatchOpenAlerts and dispatchOpenMeshAlerts. It
// returns the event to fire, if any:
//
//   - inactive channel: nothing.
//   - never fired for this channel: "created" once the alert has been eligible
//     for at least the target's escalation delay.
//   - fired and not resolved: "reminder" once reminderInterval (> 0) has
//     elapsed since the last send.
//
// shouldSendNotification (the legacy loop's pre-filter) and the claim
// predicates in claimNotificationTx encode the same rule; change them
// together. The claim stays authoritative across replicas — this only decides
// whether to try.
func dueEvent(now time.Time, state *notificationState, target channelTarget, alertAge, reminderInterval time.Duration, gate DispatchGate) (string, bool) {
	_ = gate
	if !target.channel.IsActive {
		return "", false
	}
	if state == nil {
		if alertAge >= target.delay {
			return "created", true
		}
		return "", false
	}
	if reminderDue(state, now, reminderInterval) {
		return "reminder", true
	}
	return "", false
}

// reminderDue reports whether a reminder is due on an existing notification
// state: not resolved, reminders enabled, and the interval elapsed since the
// last send. It mirrors the reminder claim predicate in claimNotificationTx.
func reminderDue(state *notificationState, now time.Time, reminderInterval time.Duration) bool {
	if state == nil || state.LastEventType == "resolved" || reminderInterval <= 0 {
		return false
	}
	return now.Sub(state.LastSentAt) >= reminderInterval
}
