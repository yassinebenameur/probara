package present

import (
	"fmt"

	"github.com/yassinebenameur/probara/shared/notifications"
)

// Relapse wording. An alert whose subject fails again within its workspace's
// reopen cooldown is reopened rather than replaced (shared/alertrouting
// relapse.go), and each route that heard the recovery is told so once, with
// a "created" event whose Alert.RelapseCount > 0. Plugins see no new event
// type: a paging provider re-triggers, every other channel shows this
// wording. Every event of a relapsed alert also carries the relapse rows.

// isRelapsePage reports whether this event is the page of a relapse: a
// created event of a reopened, still open alert.
func isRelapsePage(alert notifications.AlertDetails, eventType string, resolved bool) bool {
	return alert.RelapseCount > 0 && eventType == EventCreated && !resolved
}

// relapseLabel is the headline of a relapse page: the kind's own page label,
// marked as a repeat.
func relapseLabel(alert notifications.AlertDetails) string {
	if alert.Kind == "" || alert.Kind == notifications.KindAvailability {
		return "Alert Relapsed"
	}
	firstPage := alert
	firstPage.RelapseCount = 0
	return labelFor(firstPage, EventCreated) + " Again"
}

// relapseAgain says, per kind, what the subject is doing again.
func relapseAgain(alert notifications.AlertDetails) string {
	switch {
	case alert.IsLatencyAnomaly():
		return "degraded again"
	case alert.IsHostMetric():
		return "breaching again"
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
