package alerter

import (
	"time"

	"github.com/yassinebenameur/probara/shared/alertrouting"
)

// DispatchGate carries the alert-level conditions that can hold back a due
// event for every channel of an alert: acknowledgement (when the tenant stops
// paging on ack) and snooze. dueEvent, shouldSendNotification and the claim in
// deliverNotification (claimStatusAllows) all apply it through gateHolds, so
// the open-alert loops, the legacy pre-filter and the authoritative claim
// cannot disagree.
type DispatchGate struct {
	// Acknowledged is true while the alert's status is 'acknowledged'.
	Acknowledged bool
	// AckStops is the tenant's ack_stops_notifications.
	AckStops bool
	// Snoozed is true while snoozed_until is in the future. The open-alert
	// loops filter snoozed alerts in SQL (alertrouting.SnoozedPredicate), so
	// they leave it false; the claim re-reads it under the row lock.
	Snoozed bool
	// SnoozedUntil and UnacknowledgedAt are the alert's columns (zero for
	// NULL). The route's resume point (resumedAt) derives from them:
	// tiers and reminders count from it.
	SnoozedUntil     time.Time
	UnacknowledgedAt time.Time
}

// resumedAt is when paging on a route with this stopOnAck resumed after a
// mute (alertrouting.ResumedAt): the snooze end on every route, the unack
// only where the ack held the route. The claim derives its reminder resume
// point from the same function.
func (g DispatchGate) resumedAt(stopOnAck bool) time.Time {
	return alertrouting.ResumedAt(g.SnoozedUntil, g.UnacknowledgedAt, stopOnAck)
}

// stopOnAck reports whether an acknowledgement silences created/reminder on
// target: the tenant stops paging on ack and the route did not opt out with
// notify_after_ack. It is what deliverNotification and the delivery worker
// receive (DispatchEnvelope.StopOnAck) to re-check under the alert row lock.
func (g DispatchGate) stopOnAck(target channelTarget) bool {
	return g.AckStops && !target.notifyAfterAck
}

// gateHolds is the one ack/snooze rule (alertrouting.MuteHolds, shared with
// the delivery worker). A snooze mutes escalation tiers, reminders and
// suspected-cause follow-ups; an acknowledgement mutes tiers and reminders on
// stopOnAck routes. Suspected-cause follow-ups still go to acknowledged alerts
// (they help whoever is on it), and acknowledged / resolved notifications are
// never held.
func gateHolds(eventType string, acknowledged, snoozed, stopOnAck bool) bool {
	return alertrouting.MuteHolds(eventType, acknowledged, snoozed, stopOnAck)
}

// dueEvent is the single created/reminder due rule for one (alert, channel
// target) pair, shared by dispatchOpenAlerts, dispatchOpenMeshAlerts and
// dispatchOpenLocationOfflineAlerts. It returns the event to fire, if any:
//
//   - inactive channel: nothing.
//   - held by the gate (gateHolds): nothing.
//   - never fired for this channel: "created" once the alert has been eligible
//     for at least the target's escalation delay, counted from the route's
//     resume point when that is later (gate.resumedAt).
//   - fired and not resolved: "reminder" once reminderInterval (> 0) has
//     elapsed since the last send and since the route's resume point.
//
// shouldSendNotification (the legacy loop's pre-filter) and the claim
// predicates in claimNotificationTx / claimStatusAllows encode the same rule;
// change them together. The claim stays authoritative across replicas — this
// only decides whether to try.
func dueEvent(now time.Time, state *notificationState, target channelTarget, alertAge, reminderInterval time.Duration, gate DispatchGate) (string, bool) {
	if !target.channel.IsActive {
		return "", false
	}
	stopOnAck := gate.stopOnAck(target)
	resumed := gate.resumedAt(stopOnAck)
	if state == nil {
		if !resumed.IsZero() && now.Sub(resumed) < alertAge {
			alertAge = now.Sub(resumed)
		}
		if alertAge >= target.delay && !gateHolds("created", gate.Acknowledged, gate.Snoozed, stopOnAck) {
			return "created", true
		}
		return "", false
	}
	if reminderDue(state, now, reminderInterval, resumed) && !gateHolds("reminder", gate.Acknowledged, gate.Snoozed, stopOnAck) {
		return "reminder", true
	}
	return "", false
}

// reminderDue reports whether a reminder is due on an existing notification
// state: not resolved, reminders enabled, and the interval elapsed since the
// last send and since resumedAt (zero = never muted). It mirrors the reminder
// claim predicate in claimNotificationTx.
func reminderDue(state *notificationState, now time.Time, reminderInterval time.Duration, resumedAt time.Time) bool {
	if state == nil || state.LastEventType == "resolved" || reminderInterval <= 0 {
		return false
	}
	return now.Sub(state.LastSentAt) >= reminderInterval && now.Sub(resumedAt) >= reminderInterval
}
