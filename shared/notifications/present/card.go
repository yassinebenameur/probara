package present

import (
	"github.com/yassinebenameur/probara/shared/notifications"
)

// EventUpdated rewrites a chat card (plugin.CapabilityMessageUpdate) to the
// alert's current state: the alerter sends it when an acknowledgement,
// unacknowledgement, snooze or resolve left the card showing something else.
// It has no wording of its own: the card reads like the page that describes
// the alert's state now (the trigger while active, the acknowledgement, the
// recovery), plus the snooze while one runs.
const EventUpdated = "updated"

// CardEventType is the event whose wording describes the alert's current
// state on a rewritten card.
func CardEventType(alert notifications.AlertDetails) string {
	switch alert.Status {
	case "resolved":
		return EventResolved
	case "acknowledged":
		return EventAcknowledged
	default:
		return EventCreated
	}
}

// buildCard is BuildWith for EventUpdated.
func buildCard(event notifications.AlertEvent, opts Options) Message {
	m := BuildWith(event, CardEventType(event.Alert), opts)
	m.EventType = EventUpdated
	if until := event.Alert.SnoozedUntil; until != nil && !m.Resolved {
		m.StatusWord += " · SNOOZED"
		m.Rows = append(m.Rows, Row{Label: "Snoozed until", Value: HumanTime(*until)})
	}
	return m
}
