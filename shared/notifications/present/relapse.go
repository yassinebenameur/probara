package present

import (
	"fmt"

	"github.com/yassinebenameur/probara/shared/notifications"
)

// Relapse wording. An alert whose subject fails again within its workspace's
// reopen cooldown is reopened rather than replaced (shared/alertrouting
// relapse.go), and each route last paged in an earlier episode is told so
// once, with a "created" event carrying Alert.RelapseNotice. Plugins see no
// new event type: a paging provider re-triggers, every other channel shows
// this wording. Any other page of a relapsed alert (an escalation route's
// first page during a relapse, say) is an ordinary page; every event of a
// relapsed alert carries the relapse rows as facts.

// isRelapsePage reports whether this event is the relapse notice: a created
// event the alerter claimed as the relapse, of a still open alert.
func isRelapsePage(alert notifications.AlertDetails, eventType string, resolved bool) bool {
	return alert.RelapseNotice && alert.RelapseCount > 0 && eventType == EventCreated && !resolved
}

// relapseLabel is the headline of a relapse page: the kind's own page label,
// marked as a repeat.
func relapseLabel(alert notifications.AlertDetails) string {
	if alert.Kind == "" || alert.Kind == notifications.KindAvailability {
		return "Alert Relapsed"
	}
	firstPage := alert
	firstPage.RelapseNotice = false
	return labelFor(firstPage, EventCreated) + " Again"
}

// relapseAgain says, per kind, what the subject is doing again.
func relapseAgain(alert notifications.AlertDetails) string {
	switch {
	case alert.IsLatencyAnomaly():
		return "degraded again"
	case alert.IsHostMetric():
		return "breaching again"
	case alert.IsDomainExpiry() && alert.MetricValue == nil:
		return "unreadable again"
	case alert.IsTLSExpiry(), alert.IsDomainExpiry():
		return "back in its warning window"
	case alert.IsMeshEdge():
		return "failing again"
	case alert.IsLocationOffline():
		return "offline again"
	}
	return "down again"
}

// relapseLead is the sentence a relapse page's summary opens with, e.g.
// "Relapsed — down again 4m after recovering, relapse #2."
func relapseLead(alert notifications.AlertDetails) string {
	when := "shortly after recovering"
	if alert.LastRecoveredAt != nil && alert.ReopenedAt != nil {
		if gap := alert.ReopenedAt.Sub(*alert.LastRecoveredAt); gap >= 0 {
			when = HumanDuration(gap) + " after recovering"
		}
	}
	return fmt.Sprintf("Relapsed — %s %s, relapse #%d.", relapseAgain(alert), when, alert.RelapseCount)
}

// relapseRows are the detail rows every event of a relapsed alert carries.
func relapseRows(alert notifications.AlertDetails) []Row {
	if alert.RelapseCount <= 0 {
		return nil
	}
	rows := []Row{{Label: "Relapses", Value: fmt.Sprintf("%d", alert.RelapseCount)}}
	if alert.ReopenedAt != nil {
		rows = append(rows, Row{Label: "Reopened", Value: HumanTime(*alert.ReopenedAt)})
	}
	return rows
}
